# Secondary-device registration protocol

Status: reviewed clean-room partial specification, with Android comparison
addendum, 2026-09-23.

This document describes behavior established from logged-out static analysis of
an authorized KakaoTalk for macOS 26.8.0 client. It contains no binary addresses,
proprietary implementation text, account values, QR values, credentials, or
captured traffic. Unknown wire details are deliberately left unknown.

## Protocol boundary

Registration is an HTTPS-oriented control plane that precedes the persistent LOCO
session. It has two presentations:

- a four-character passcode flow;
- a QR flow with a second device-authorization step for an unregistered device.

Successful approval is not itself a working chat session. Registration hands the
result to the normal login coordinator, which may offer history restoration and
then enters session bootstrap. Transient challenges and device-auth codes are not
long-lived session credentials.

## Operation inventory

The current client contains these registration routes:

```text
/mac/account/passcodeLogin/generate
/mac/account/passcodeLogin/registerDevice
/mac/account/passcodeLogin/cancel
/mac/account/qrCodeLogin/generate
/mac/account/qrCodeLogin/cancel
/mac/account/qrCodeLogin/login
/mac/account/qrCodeLogin/passwordCheck
```

The common HTTP layer also contains ordinary account login and destruction routes.
Their presence does not establish that registration uses them, and an implementation
must not treat account destruction as device revocation.

## Common model

A registration session has:

- one method: passcode or QR;
- one requested mode: permanent or temporary, where applicable;
- an active challenge identifier or presentation;
- a display-expiry deadline;
- an independently scheduled approval poll;
- optionally, a four-character device-authorization code and its own deadline.

Implementations must keep display expiry, approval polling, and device-authorization
expiry independent. Closing or cancelling is locally idempotent: timers stop and
transient values clear even when the remote cancellation request fails.

Unknown statuses, missing identifiers, malformed deadlines, and incomplete success
values fail closed. A client must never infer approval from an unrecognized result.

## Passcode lifecycle

```text
idle
  -> generating
  -> awaiting approval(code, display expiry)
       -> pending: schedule another poll
       -> approved: stop timers; enter the success handoff
       -> display expired: stop polling; show expired
       -> cancelled/closed: request cancellation; clear transient state
       -> terminal failure: stop timers; show failure
```

The visible challenge is four characters. The official client receives absolute,
expiry-like values and computes a remaining display interval against its current
clock, but the epoch and original wire units are not yet established. Preserve the
server representation at the wire boundary until they are confirmed.

Approval polling uses a timer distinct from the visible countdown. Delayed polling
is established; its exact interval, backoff, and retry ceiling are not. Those
values must be versioned policy rather than hard-coded protocol facts.

## QR lifecycle

```text
idle
  -> generating
  -> awaiting approval(qr identifier, display expiry)
       -> pending: schedule another poll
       -> unregistered device:
            awaiting device authorization(short code, auth expiry)
              -> pending: continue approval checks
              -> approved: enter the success handoff
              -> auth expired: cancel and clear transient state
       -> approved: enter the success handoff
       -> rejected/expired/unsupported/suspended/restricted/invalid/unknown:
            stop relevant timers; enter a terminal state
```

Polling without a QR identifier is forbidden. Refresh after expiry first clears
the old identifier and device-auth state, then generates a new challenge.

The semantic QR result categories established by control flow are:

- pending main-device approval;
- unregistered device requiring device authorization;
- main-device rejection;
- expired QR challenge;
- unsupported device version;
- suspended user;
- restricted account;
- invalid response;
- unknown failure with an associated error;
- success.

The QR numeric mapping is specified below. Passcode numeric status values remain
unresolved and must not be borrowed from historical clients.

## Success handoff

Temporary success marks the session as temporary, skips restoration, and begins
ordinary login. Permanent success marks it non-temporary and checks restore
eligibility. An ineligible or skipped restore begins ordinary login directly; an
eligible restore first enters the backup/restore coordinator and then converges on
the same login boundary.

The QR identifier and displayed device-auth code are cleared before this handoff.
The downstream login path consumes at least numeric user identity, access token,
and foreground/background state. This does not establish the final LOCO carriage
`LOGIN` schema or token transformation.

Successful QR enrollment can return longer-lived auto-login material. The client
stores it separately from transient QR state through the device-bound encrypted
preferences boundary documented under `../credential-storage/`. The exact flag
combination controlling persistence, especially for temporary sessions, is not
fully mapped. A clean implementation must default to keeping temporary-session
material in memory only.

## Confirmed request model

Static request builders directly associate these fields with the seven operations:

| Operation | Fields | Device object |
| --- | --- | --- |
| Passcode generate | `email`, `password`, `permanent`, `device` | `name`, `uuid`, `osVersion`, `model` |
| Passcode register | `email`, `password`, `device` | `uuid` |
| Passcode cancel | `email`, `password`, `device` | `uuid` |
| QR generate | `previousId`, `device` | `name`, `uuid`, `osVersion`, `model` |
| QR cancel | `id`, `device` | `uuid` |
| QR login/poll | `id`, `device` | `uuid` |
| QR password check | `password` | none |

`permanent` is Boolean. All seven operations are HTTPS `POST` requests to the
reviewed `https://katalk.kakao.com` registration service. Their parameter
dictionaries are encoded into the HTTP body with Alamofire `URLEncoding.httpBody`:
the body content type is `application/x-www-form-urlencoded; charset=utf-8`,
nested device keys use bracket notation such as `device[uuid]`, spaces use `%20`,
and Boolean values use `1` or `0`. Top-level keys are sorted by Alamofire; nested
dictionary order is not a protocol semantic, so the Go codec may canonicalize all
pairs for deterministic fixtures.

These encoding semantics combine direct current-client evidence of the
`URLEncoding.httpBody` call with Alamofire's published implementation contract:
<https://github.com/Alamofire/Alamofire/blob/master/Source/Core/ParameterEncoding.swift>.
The meaning and accepted forms of `email`, empty-field omission behavior, common
headers, cookies, and any request signing remain unresolved. This is therefore not
yet a complete live network contract.

Password checking is a distinct QR-family operation. Evidence does not yet prove
whether it gates device authorization, permanent enrollment, or another transition,
so it remains a typed but unresolved protocol boundary.

## QR payload and response mapping

QR generation returns an opaque server-provided string. The client validates the
generation result, parses that string as URL components, extracts query parameter
`id` for cancel and poll requests, and renders the original complete string as the
QR content. It does not assemble the displayed QR from local device fields in the
observed path. The scheme, host, other query fields, and check-key validation recipe
remain unresolved; neither the full string nor extracted ID may be logged.

The QR login/poll error code maps as follows:

| Code | Semantic result |
| ---: | --- |
| `1` | Unregistered device; device authorization required |
| `5` | Suspended user |
| `13` | Unsupported device version |
| `14` | Main-device approval pending |
| `15` | Main-device rejection |
| `16` | Expired QR challenge |
| `20` | Restricted account |
| `29` | Invalid response |
| other | Unknown terminal failure |

Relevant error-response keys are `response`, `nextRequestIntervalInSeconds`,
`passcode`, `remainingSeconds`, `status`, and `message`. Result `1` requires the
next delay, four-character passcode, and remaining lifetime to enter device
authorization. Result `29` with nested `response.status == -404` and an existing
QR identifier has a special recovery/presentation branch whose protocol meaning
is not yet established; it remains an explicit unknown special case.

### Shared Mac authentication-response boundary

Static inspection of the macOS 26.8.0 shared account-login response handler
directly identifies these dictionary keys:

```text
userId, countryIso, accountId,
access_token, refresh_token, token_type, server_time,
autoLoginAccountId, displayAccountId
```

This is a shared/general authentication decoder inventory, not direct proof that
the QR `/login` response carries every key. The handler gates its success
callback on object presence/validity, but the required-versus-optional matrix
and wire scalar types remain unresolved. The QR controller still only has direct
evidence for consuming a complete server URL string, extracting its `id`, and
using a server-supplied expiry duration. Do not infer Android's `nonce` field or
token casing on the Mac path from this inventory.

## Android comparison (not Mac wire proof)

Static inspection of the current Android 26.8.2 client provides an
implementation-neutral comparison for the same QR family. It must not be read
as direct evidence of Mac 26.8.0 serialization or host selection.

The Android QR-generate model is:

```text
request:  { device: { name, uuid, model, osVersion }, previousId? }
response: { status, url, remainingSeconds }
```

The Android QR-login/poll success model adds these fields to `status`:

```text
nextRequestIntervalInSeconds?, passcode?, remainingSeconds?,
user?: { userId, countryIso, accountId?, displayAccountId? },
accessToken?, refreshToken?, tokenType?, nonce?
```

The Android scanner recognizes the path
`/talk/account/qrCodeLogin/info.json?id=...`, extracts the raw identifier
suffix, and calls its account-side `qrCodeLogin/info` operation. The Android
primary client then authorizes the identifier by decoding its challenge payload,
computing an HMAC-SHA256 response with its authenticated account secret, and
posting `{id, macResponse, forceLogin}` to its account-side
`qrCodeLogin/authorize` operation. This establishes that QR authorization is
performed by the authenticated primary device; it does not establish the Mac
client's local check-key validator or the full URL host.

For an unregistered PC, the Android primary-side confirmation model is
`{id, passcode, forced?, permanent?}` sent to `qrCodeLogin/confirm`, with a
status-only response. This is complementary to, and does not replace, the Mac
polling client's own `qrCodeLogin/passwordCheck` operation.

The Android comparison narrows the expected success handoff fields, but the Mac
mapping of `userId`, `accessToken`, `refreshToken`, `tokenType`, and `nonce` into
its ordinary login and persistence state remains unconfirmed. Until that Mac
mapping is recovered, clients must treat all values as opaque and fail closed
on incomplete success objects.

## Polling policy

- Visible passcode, QR, and device-auth countdowns tick once per second.
- Passcode polling uses the server/controller delay but clamps it to a minimum of
  three seconds.
- QR polling begins three seconds after generation.
- Later positive QR delays are server-directed; non-positive values fall back to
  three seconds.
- When result `1` enters device authorization, a delay below one second is replaced
  with three seconds.
- No exponential backoff, jitter, or fixed retry ceiling was observed in these UI
  schedulers. Expiry or a terminal result ends polling.

## Cancellation versus revocation

Passcode and QR cancellation abandon in-progress challenges. That is distinct from
logging out an established session or remotely revoking a registered secondary
device. Current-device unregister during logout uses
`/mac/account/destroy.json`; it is not account deletion. Its body, response schema,
and cleanup ordering after remote failure remain unresolved. No Mac route was
attributable to listing all devices or revoking a selected other device.

The LOCO layer receives server kickout/displacement notifications with a numeric
reason, but the reason mapping is unresolved.

A terminal server result stops local timers and clears transient state, but this
specification does not establish that the client sends an additional cancellation
request after every rejection or server-reported failure. The conformance model
emits remote-cancel effects only for explicit cancellation and local expiry paths.

## Go conformance model

The first implementation is deliberately a pure reducer: semantic events in,
state plus side-effect requests out. It performs no HTTP, credential storage, or
logging and therefore cannot accidentally treat the still-unresolved transport
layer as a proven wire implementation.

Required synthetic cases:

1. passcode pending to approved, with polling and expiry stopped;
2. passcode display expiry preventing further polls;
3. QR pending to device authorization to auth expiry;
4. refresh clearing an old QR identifier before generation;
5. every known terminal QR result failing closed;
6. permanent success exposing restore or skip;
7. temporary success bypassing persistent credential decisions;
8. cancellation being safe twice and after expiry;
9. stale timer and callback events not reviving an ended session;
10. invalid events and incomplete generated challenges being rejected.

## Open protocol work

- passcode numeric result mapping and complete success schemas;
- Mac QR URL grammar and check-key validation recipe (the Android path
  comparison above is not sufficient);
- request signing, shared headers, cookies, and authentication state;
- password-check ownership and result schema;
- exact auto-login persistence flag behavior and Mac mapping of QR token fields;
- final LOCO `LOGIN` schema and transformations;
- current-device unregister body/response and cleanup ordering;
- device listing, selected-device revocation, and kickout reason mapping.

# Session-login protocol

Status: reviewed clean-room partial specification, 2026-09-20.

This document specifies the current macOS 26.8.0 registration HTTP transport,
final LOCO carriage login, and reconnect behavior established through offline
static analysis. It contains no live identifiers, tokens, endpoints, captures,
binary addresses, internal names, or proprietary implementation text.

## End-to-end sequence

```text
registration HTTP succeeds
  -> install user identity and access-token state
  -> GETCONF when routing is absent/stale
  -> CHECKIN against a ticket endpoint
  -> connect secure carriage transport
  -> LOGINLIST
  -> install session, endpoint, chat-list, and cursor state
```

Transient QR identifiers and device-authorization codes are cleared before this
sequence. They are not carriage credentials.

## Registration HTTP profile

All seven current registration operations use:

- base URL `https://katalk.kakao.com`;
- HTTP `POST`;
- URL-form parameters in the request body;
- nested `device` dictionaries rather than flattened peer fields;
- JSON response/error bodies with top-level dictionaries;
- HTTP-status validation through a shared session.

The application starts these requests with an empty explicit header collection.
The registration builders add no authorization header, signature, nonce, digest,
or per-request transform. Platform HTTP defaults may still produce ordinary
transport headers, form content type, and cookie behavior; unobserved defaults must
not be hard-coded as application protocol.

| Route | Form-body fields |
| --- | --- |
| `/mac/account/passcodeLogin/generate` | `email`, `password`, `permanent`, full `device` |
| `/mac/account/passcodeLogin/registerDevice` | `email`, `password`, UUID-only `device` |
| `/mac/account/passcodeLogin/cancel` | `email`, `password`, UUID-only `device` |
| `/mac/account/qrCodeLogin/generate` | `previousId`, full `device` |
| `/mac/account/qrCodeLogin/cancel` | `id`, UUID-only `device` |
| `/mac/account/qrCodeLogin/login` | `id`, UUID-only `device` |
| `/mac/account/qrCodeLogin/passwordCheck` | `password` |

A full device contains `name`, `uuid`, `osVersion`, and `model`; a UUID-only
device contains only `uuid`. `permanent` is Boolean at the semantic builder
boundary. Exact form escaping is delegated to the form encoder and byte ordering
is not protocol-significant.

Malformed request construction fails before submission. Cancellation is distinct
from ordinary transport failure. If no HTTP response exists, the shared failure
callback substitutes numeric code `500`. Server error JSON may contain `reason`,
`detailCode`, and `status`. Shared infrastructure recognizes `-950`, but current
evidence does not establish it as a registration-specific result.

### Password check

The QR coordinator owns the password-only request. It returns success only when:

1. the HTTP status is `200`;
2. a response dictionary exists;
3. `status` is an integer;
4. `status == 0`.

Every other shape or status fails closed.

### QR validation and results

QR generation returns an opaque server string. The client validates it, parses its
URL query parameter `id` for later poll/cancel calls, and renders the original full
string. A separate check-key gate exists, but its algorithm remains unresolved;
an implementation must not accept an unchecked payload.

| Code | Result |
| ---: | --- |
| `1` | Unregistered device; device authorization required |
| `5` | Suspended user |
| `13` | Unsupported client/device version |
| `14` | Main-device approval pending |
| `15` | Main-device rejection |
| `16` | QR expired |
| `20` | Restricted account |
| `29` | Invalid response |
| other | Unknown terminal failure |

A nested response status `-404` has a separate recovery/presentation branch whose
protocol meaning remains unknown. Passcode semantic states are known, but their
numeric server-status table is not.

## Final carriage request

The final command is `LOGINLIST`, using the existing LOCO framed BSON transport.

| BSON key | BSON type | Current-client source/behavior |
| --- | --- | --- |
| `appVer` | string | Client version from LOCO configuration |
| `os` | string | Platform identifier |
| `lang` | string | Configured language |
| `duuid` | string | Registered device UUID |
| `sKey` | string | Unset in this path |
| `oauthToken` | string | Current access-token string, unchanged here |
| `ntype` | int32 | Network/config value |
| `MCCMNC` | string | Mobile-country/network configuration |
| `revision` | int32 | Protocol/config revision |
| `dtype` | int32 | Device type |
| `pcst` | int32 | PC status |
| `rp` | binary | Resume/presence blob |
| `bg` | boolean | Background flag |
| `chatIds` | array<int64> | Chat-list resume IDs |
| `maxIds` | array<int64> | Corresponding per-chat maximum IDs |
| `lastTokenId` | int64 | Chat-list cursor |
| `lbk` | int32 | Last blind-token cursor |

All scalar fields are set. `chatIds` and `maxIds` are positional pairs and must
have equal length. Empty arrays are valid. Static evidence proves that `sKey` is
unset, but generic serializer behavior for unset object values—omitted versus BSON
null—still requires a synthetic comparison. Initially omit unset objects behind a
versioned compatibility profile.

The access token is not decoded, hashed, or otherwise transformed at this builder.
An import path may already have decrypted and formatted stored credential material;
`LOGINLIST` consumes the prepared string directly as `oauthToken`.

## Login response

The response extends chat-list state with:

- `userId` (int64), `revision` (int32), `revisionInfo` (string), and `minLogId`
  (int64);
- carriage host (string) and port (int32);
- voice-talk IPv4/IPv6/VSS hosts and int32 ports;
- `lastLinkToken` (int32);
- inherited chat data, deleted chat IDs, EOF, MCM revision, `lastTokenId` (int64),
  blind token (int32), and last chat ID (int64);
- generic `status` (int32) and optional error message/URL/label strings.

Observed predicates:

- `0`: generic success;
- `-305`: also accepted as login success;
- `-310`: generic partial success;
- `-445`: explicitly classified as login blocked.

The semantic labels of the three negative statuses remain unresolved. Unknown
statuses preserve their numeric value and fail closed.

An accepted login installs minimum-log, carriage, voice, chat-list, revision, and
cursor state before the higher-level session becomes logged in. Updates must be
atomic from the caller's perspective; a partially decoded response must not advance
resume cursors.

## Routing cache and retry

When no live carriage route exists, a cached host/port may be tried. The host must
be nonempty and the port positive. Failure of that exact cached endpoint clears it
before fallback. Cache lifetime uses elapsed system uptime and the configured
expiry interval; expiry returns through booking/check-in.

Booking suppresses concurrent attempts and makes at most three observed attempts,
with jitter capped at eight seconds. Ticket retries advance through the address
pool with jitter capped at 32 seconds. Address-pool exhaustion terminates the
attempt instead of looping indefinitely.

## Reconnect and resume

Ordinary recovery runs the normal login sequence again and preserves:

- `chatIds` / `maxIds`;
- `lastTokenId`;
- `lbk`.

Recovery is suppressed when authentication is no longer usable, the network is
unreachable, recovery is disabled, another recovery is active, or the requested
recovery generation is stale. Completing an admitted attempt advances the recovery
generation so delayed callbacks cannot affect later work; failure then schedules
another bounded attempt.

Public implementations corroborate a fresh bootstrap on reconnect. Public claims
of a separate cursor-resume feature are not treated as evidence where their code
does not actually feed saved cursors into a wire request.

Special events are distinct:

- `CHANGESVR` clears carriage routing and forces fresh route discovery;
- `KICKOUT` carries a numeric reason and optional safe error metadata, then logs
  out instead of entering an unlimited reconnect loop;
- kickout reasons `1` and `10` request local database reset in this client; exact
  human meanings remain unproven;
- an upper-layer disconnect that survives/exhausts manager recovery logs out
  without database reset.

Expired authentication is terminal: rejected credentials must not be retried in a
tight loop. The current numeric association for the expired-token presentation is
not yet proven.

## Current blockers

- QR check-key validation algorithm and full QR URL grammar;
- passcode numeric status table and complete registration success schemas;
- effective platform-default cookie/header values, if server-significant;
- omit-versus-null encoding for unset LOGINLIST object fields;
- abbreviated response wire-key mappings not visible from property metadata;
- semantic labels and complete mappings for LOCO statuses and kickout reasons;
- exact ordinary recovery delay constants and internal state names;
- cursor inclusivity, gap recovery, and post-reconnect delivery guarantees.

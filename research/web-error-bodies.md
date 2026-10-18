# Web API error bodies

Status: static trace of KakaoTalk for Mac 26.8.0 plus credential-free live
observations, 2026-10-17. Scope is the katalk and talk-pilsner HTTP endpoints
mooo calls through `internal/protocol/macweb`: reactions and reaction metadata
(talk-pilsner), add friend by phone (katalk), token renewal (katalk auth) and
the direct-photo prepare step (talk-pilsner). The Kage upload host is covered
where it shares a body shape.

Fixtures:

- [`fixtures/web-errors/observed-unauthenticated.json`](fixtures/web-errors/observed-unauthenticated.json):
  real status codes, headers, keys and values from WEB-LIVE-001.
- [`fixtures/web-errors/static-mac-error-model.json`](fixtures/web-errors/static-mac-error-model.json):
  what the Mac client reads from a body and what it does with it (WEB-BIN-001).
- [`fixtures/web-errors/synthetic-diagnostic-extraction.json`](fixtures/web-errors/synthetic-diagnostic-extraction.json):
  seven error bodies, each mirroring one observed in WEB-LIVE-001, reported in
  WEB-REP-001 or traced from the Mac client in WEB-BIN-001, with the diagnostic
  a parser should pull out of each under the rules in
  [Extracting a safe diagnostic](#extracting-a-safe-diagnostic). Message and
  token values are synthetic; no body is invented to stress a parser.

No mooo code consumes these yet. Following
[parity-fixtures.md](parity-fixtures.md), they wait here until the parser that
needs them lands.

## Summary

There is no single Kakao error envelope. Each host family has its own:

| Family | Where the error lives | Fields | Types |
| --- | --- | --- | --- |
| katalk (`/mac/...`) | Usually HTTP **200**, sometimes 401 | `status`, `message`; on 401 also `reason.detailCode` | `status` integer, `message` string, `reason` object |
| katalk auth (`renew_token.json`) | HTTP 200 | `status` | integer |
| talk-pilsner gateway | HTTP 401 with `talk-served-by: gateway` | `reason`, `message` | both strings |
| talk-pilsner service | 2xx with `status`, or non-2xx with an untraced body | `status`; reported `code`, `reason` | integer; strings |
| Kage upload | 2xx and 404 | `msg` | string |

- **`status`** is the katalk result code: a JSON integer, 0 for success,
  negative for most failures. katalk returns most rejections on HTTP 200, so
  they never reach `macweb.StatusError`; the endpoint decoder sees them.
- **`reason`** is a string on the talk-pilsner gateway (`UNAUTHENTICATED`) and
  an object on katalk (`{"detailCode": 9503}`). A parser must not assume either.
- **`code`** was not seen in any response here and is not read by any traced Mac
  code. Its only source is the body reported in frrad/mooo#424:
  `{"code":"90008","reason":"INVALID_REQUEST"}` on an HTTP 400 from
  talk-pilsner reaction metadata. It is a string of digits there.
- **`message`** is localized, user-facing text. The Mac client shows it to the
  user. It must never be logged.
- Non-JSON bodies happen: katalk answers unknown paths with an empty 404, and
  talk-pilsner with `text/plain` "404 Not Found".

Safe to log: `status`, the gateway `reason` string, `reason.detailCode` and a
short digit `code`, each under the shape limits below. Never log: `message`,
`msg`, `title`, `errUrl`, `errUrlLabel`, `linkUrl`, `displayString`,
`loginFailedAccountToken`, `friend`, any other key, and the raw body.

## Evidence

| ID | Date | Client / platform | Method | Observation | Confidence |
| --- | --- | --- | --- | --- | --- |
| WEB-BIN-001 | 2026-10-17 | KakaoTalk for Mac 26.8.0 arm64 | Read-only headless Ghidra: annotated disassembly and selector resolution of the katalk error constructors, the token manager's success check and 401 predicate, the error-domain mapper, the add-by-phone callbacks, and the talk-pilsner client's accepted statuses and its bridge into the katalk error type | The field model, consumers and failure behaviour in the sections below | High for the katalk constructors, status check, 401 predicate, status mapper and pilsner bridge; medium for the add-by-phone completion; the Swift talk-pilsner body decoding is untraced |
| WEB-LIVE-001 | 2026-10-17 | curl, Mac 26.8.0 `A` and `User-Agent` headers | Fifteen single requests to katalk and talk-pilsner with no `Authorization` header or the literal non-credential value `invalid-invalid`. No account, token, refresh token or device identifier was sent. Raw responses stay in the private lab directory | Bodies and headers in `observed-unauthenticated.json` | High for these unauthenticated paths; says nothing about errors returned to an authenticated request |
| WEB-REP-001 | 2026-10 | Reported in frrad/mooo#424 | External report, not reproduced | HTTP 400 `{"code":"90008","reason":"INVALID_REQUEST"}` from talk-pilsner reaction metadata | Unverified lead |
| GR-LIVE (2026-10-10) | 2026-10-10 | mooo, owned account | Recorded in [GROUP-REACTIONS.md](bridge/GROUP-REACTIONS.md) and `fixtures/reactions/observed-sync-meta-page.json` | An authenticated sync-meta read with `cur` above `max` and `max=0` returned HTTP 400; the body was not recorded | High for the status, none for the body |
| SL-BIN-042 | 2026-10-15 | KakaoTalk for Mac 26.8.0 | [session-login/EVIDENCE.md](session-login/EVIDENCE.md) | 401 expiry markers on both hosts | High |

## Observed bodies (WEB-LIVE-001)

All JSON bodies were single compact objects. `message` values below are
replaced with synthetic text of the same language and shape; everything else is
as observed.

**talk-pilsner gateway.** Every unauthenticated or invalid-token request,
including a POST to the photo prepare route and a DELETE the route does not
support, returned:

```
HTTP 401
content-type: application/json
talk-served-by: gateway

{"reason":"UNAUTHENTICATED","message":"<English sentence>"}
```

The gateway checks authentication before routing or method. Its `message`
stayed English for a Korean request. Responses also carried an `x-b3-traceid`
header (a per-request random trace ID). An unknown path returned
`404 text/plain` `404 Not Found` without `talk-served-by`.

**katalk.** Errors arrived on HTTP 200:

- add-by-phone without `Authorization`: `{"status":-500,"message":"<text>"}`.
  The message is three paragraphs separated by `\n\n`, and katalk localizes it
  by the language in the `A` header, not `Accept-Language`: `A: mac/26.8.0/ko`
  with `Accept-Language: en` returned Korean.
- add-by-phone with `Authorization: invalid-invalid`: HTTP **401**,
  `{"status":-950}`. This is the katalk expiry marker the Mac client renews on.
- renew_token without `Authorization`: HTTP 200, `{"status":-500}`, no message.
- renew_token with `Authorization: invalid-invalid` and no refresh token:
  HTTP 200, `{"status":-400}`.
- Unknown paths, including a talk-pilsner path asked of katalk: HTTP 404 with an
  empty body and no content type.

## Mac client model (WEB-BIN-001)

The client has three HTTP stacks (see SL-BIN-042). Two matter for bodies.

### katalk

The katalk JSON client accepts HTTP 2xx and 401 and decodes those bodies with
Foundation's JSON reader. Any other status fails in the HTTP library before the
body is decoded.

**Status check.** For katalk clients that check status in the shared token
manager, a 2xx body succeeds only when `status` is present, is an `NSNumber`,
and its `integerValue` is 0. Otherwise the request fails with a katalk error
whose code is the status, or 0 when `status` is missing or is not a number
(a string `"-500"` gives 0). Foundation decodes JSON `true` as an `NSNumber`,
so `"status": true` is read as status 1. The katalk client that add-by-phone uses
skips this check and hands every decoded
2xx or 401 body to the caller, which checks `status` itself.

**The error object.** The katalk error constructor takes the status and the
response dictionary and reads:

| Key | Use |
| --- | --- |
| `message` | The error's localized failure reason: the text the client shows. If it is absent the client uses a localized "session expired" text for status -910 and a localized "unknown error (code)" text otherwise. |
| `errUrl`, `errUrlLabel`, `title`, `linkUrl` | Copied into the error for alert buttons and links. |
| (whole body) | Kept in the error as `response`. |

The domain mapper then turns the katalk status into an internal error category
(next section). It copies `errUrl` and `errUrlLabel` again and keeps `message`
as the failure reason, except for -4247, where it substitutes a localized
"temporary error, try again" text. For status 27 it reads
`loginFailedAccountToken` from the response and passes it to an account web
view as a token.

**401.** A 401 is an expiry only when the body is a dictionary and either
`reason` is a dictionary whose `detailCode` is 9503, or `status` (read as a
32-bit integer) is -950. A 401 without either marker is delivered to the caller
as a success.

**Non-2xx, non-401.** The shared error conversion copies the HTTP library's
error domain, code and user info unchanged and reads nothing from the body. The
add-by-phone failure callback logs the error, then reads `status` from the
error's `response` entry, which exists only for katalk errors built from a
decoded body; for an HTTP-level failure it gets nothing and reports status 0.

**Token renewal failure.** -998 maps to the kickout notification and -999 to the
authentication notice while logged in, with the error's failure reason as the
notice text (SL-BIN-042).

### talk-pilsner

The talk-pilsner command client accepts and decodes 2xx, 400, 401, 403, 404,
405, 406, 409, 415, 429, 500, 503 and 507, and hands callers the HTTP status, the
decoded dictionary and a client error. A 401 with `talk-served-by: gateway` is
an expiry without reading the body (SL-BIN-042).

Objective-C callers turn that triple into a katalk error through a bridge:

- HTTP 200 and no client error: read `status` as a number; non-zero becomes a
  katalk error with that code and the body; missing or zero is success.
- Anything else: a katalk error with code -500 and **no** body.

So in bridged callers a talk-pilsner 4xx body never reaches the error, the UI or
the log. How the Swift reaction callers read a non-2xx body was not traced.

### Status values the Mac client distinguishes

The domain mapper gives these katalk statuses their own category. Meanings are
given only where the client's handling shows them; the rest are distinct but
unlabelled.

| Status | Meaning | Source |
| ---: | --- | --- |
| 0 | Success | WEB-BIN-001 |
| -400 | Renewal rejected for an invalid `Authorization` with no refresh token (HTTP 200) | WEB-LIVE-001; not in the mapper |
| -500 | Generic failure. Unauthenticated katalk requests return it on HTTP 200, and the pilsner bridge uses it for every non-200 response | WEB-LIVE-001, WEB-BIN-001 |
| -910 | Session expired; a localized notice replaces a missing message | WEB-BIN-001 |
| -911 | Distinct category | WEB-BIN-001 |
| -950 | Access token expired; renew and replay. Observed on HTTP 401 | WEB-LIVE-001, SL-BIN-042 |
| -997 | Distinct category | WEB-BIN-001 |
| -998 | Kicked out (expired authentication) | SL-BIN-042, WEB-BIN-001 |
| -999 | Authentication notice | SL-BIN-042, WEB-BIN-001 |
| -4247 | Temporary error; the client shows its own "try again" text | WEB-BIN-001 |
| 27 | Account check; the body carries `loginFailedAccountToken` | WEB-BIN-001 |
| -100, -101, -102, -110, -111, -120, -122, -125, -126, -140, -141, -142, -323, -326, -327, -332, -333, -706, -707, -1000, -1002, -1400, -1408, -140000, 12, 13, 30, 31, 32 | Distinct categories, meaning not recovered | WEB-BIN-001 |

`reason.detailCode` 9503 means an expired access token on a katalk 401. The
gateway `reason` string `UNAUTHENTICATED` means a missing or invalid access
token. `code` 90008 with `reason` `INVALID_REQUEST` is only reported
(WEB-REP-001). Registration statuses such as -150 and -404 are documented in
[session-login/PROTOCOL.md](session-login/PROTOCOL.md); registration does not
use `macweb`.

### Which fields can carry user or account data

| Field | Content | Log? |
| --- | --- | --- |
| `status`, `reason.detailCode` | Small integers | Yes, under the bounds below |
| gateway `reason` | Upper-case enum name | Yes, under the pattern below |
| `code` (reported) | Short digit string | Yes, under the pattern below |
| `message`, `msg`, `displayString` | Localized human text shown to the user; free text can quote what the user sent | Never |
| `title`, `errUrlLabel` | Localized UI text | Never |
| `errUrl`, `linkUrl` | URLs that may carry query tokens | Never |
| `loginFailedAccountToken` | A token for an account web view | Never |
| `friend` and other success data | User IDs, names, phone numbers | Never |

## Failure behaviour for non-JSON bodies

- **Observed:** katalk returns empty 404 bodies without a content type;
  talk-pilsner returns `text/plain` "404 Not Found". No HTML page was seen, but
  a CDN or load balancer in front of either host could return one (for example
  a 502 page).
- **Mac katalk:** a status outside 2xx/401 never reaches the JSON reader. A 2xx
  body that is not JSON fails in the HTTP library's JSON serializer
  (library behaviour, not traced in this binary).
- **Mac talk-pilsner:** untraced for non-JSON bodies on an accepted status.
- **mooo today:** `macweb.Do` returns `*StatusError{Code}` for any non-2xx and
  does not read the body. For 2xx, each endpoint decoder rejects non-JSON as an
  invalid response.

## Extracting a safe diagnostic

These rules are an implementation decision for mooo, not official-client
behaviour; the official client logs none of these fields. They are what
`synthetic-diagnostic-extraction.json` encodes.

1. Read only a body that is exactly one UTF-8 JSON object with no duplicate keys
   at any depth and no trailing data (as `strictjson` already enforces for
   top-level keys). Decoders disagree about which duplicate wins, so a body
   with duplicates gives no diagnostic.
2. `status`: a JSON integer literal, no fraction or exponent, from -9,999,999 to
   9,999,999. The largest known magnitude is 140000; user IDs and phone numbers
   are longer than seven digits.
3. `reason`: a string matching `^[A-Z][A-Z_]{0,63}$` after JSON unescaping. Every
   observed or reported value is an upper-case enum name. If `reason` is an
   object, read only its `detailCode`, under the `status` rule.
4. `code`: a string matching `^[0-9]{1,7}$`, or an integer under the `status`
   rule.
5. Read nothing else, never search the raw text, and never fall back to the
   raw body.
6. Treat `null`, booleans, strings in numeric fields and any rejected value as
   absent, and render absent fields as absent: no empty value, no `null` text,
   no dangling separator.

Status 200 matters too: a parser that only looks at non-2xx bodies misses
katalk's errors, which arrive on HTTP 200 and are already handled by each
endpoint decoder's `status` check.

## Gaps

- **Authenticated errors.** WEB-LIVE-001 only reached the authentication layer.
  The body of an authenticated talk-pilsner 4xx (for example the sync-meta 400
  in GR-LIVE) and of an authenticated katalk rejection on an owned account was
  not captured. The reported `code`/`reason` body (WEB-REP-001) is unverified.
- **Swift talk-pilsner decoding.** The Swift reaction and reaction-metadata
  callers' handling of a decoded non-2xx body, and the client's handling of a
  non-JSON body on an accepted status, were not traced.
- **katalk failing-response data.** Whether any katalk caller reads the HTTP
  library's raw failing-response data for a non-2xx status was not checked
  across all callers.
- **Add-by-phone completion.** The consumer of the success callback, and so how
  the client branches on each add-by-phone status, was not traced.
- **Status meanings.** Most mapped statuses have no recovered meaning, and the
  table may be incomplete for endpoints that check status inline.
- **Duplicate keys.** Foundation's behaviour with duplicate JSON keys was not
  tested in this binary.

## Follow-up experiment

On an owned account with the lab lease, repeat the GR-LIVE sync-meta read that
returned 400 (`cur` above `max`, `max=0`) and one authenticated read-only katalk
request the server rejects, recording only key names, types, status
values and enum strings. That would confirm or replace WEB-REP-001 and supply
`observed` fixtures for authenticated errors.

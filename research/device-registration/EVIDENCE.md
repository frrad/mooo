# Secondary-device registration evidence ledger

Append new evidence and supersede earlier hypotheses rather than silently turning
them into facts.

| ID | Date | Version | Evidence class | Statement | Confidence | Status |
| --- | --- | --- | --- | --- | --- | --- |
| DR-BIN-001 | 2026-09-20 | macOS 26.8.0 | Observed/static | Passcode and QR presentations maintain challenge validity, approval polling, and subsequent device-authorization expiry as separately cleaned-up concerns. | High | Confirmed control-flow shape; exact schemas pending |
| DR-BIN-002 | 2026-09-20 | macOS 26.8.0 | Observed/static | The passcode path generates a four-character challenge, polls registration, enters normal login on approval, and cancels active work on close or expiry. | High | Exact fields, status values, and cadence pending |
| DR-BIN-003 | 2026-09-20 | macOS 26.8.0 | Observed/static | QR polling distinguishes pending approval, unregistered-device authorization, rejection, expiry, unsupported/restricted/suspended outcomes, malformed responses, and permanent or temporary success. | High | Exact response mapping pending |
| DR-BIN-004 | 2026-09-20 | macOS 26.8.0 | Observed/static | Registration uses an HTTPS-oriented account/login layer distinct from the persistent LOCO transport. | High | Route inventory and request integrity pending |
| DR-BIN-005 | 2026-09-20 | macOS 26.8.0 | Observed/static | The registration surface contains seven distinct passcode/QR generate, poll/register, cancel, login, and password-check routes documented in `PROTOCOL.md`. | High | Route inventory complete; schemas incomplete |
| DR-BIN-006 | 2026-09-20 | macOS 26.8.0 | Observed/static | Request builders contain candidate login-identifier, password, permanence, device, UUID-like identity, and previous-identifier fields. | Medium | Superseded by direct route mapping in DR-BIN-009 |
| DR-BIN-007 | 2026-09-20 | macOS 26.8.0 | Observed/static | Success distinguishes permanent and temporary login; permanent login may branch to restore or start-without-restore before normal session login. | High | Persistence and LOCO handoff values unresolved |
| DR-BIN-008 | 2026-09-20 | macOS 26.8.0 | Observed/static | A QR check-key validation boundary exists, but its payload grammar and placement were not recovered. | Medium | Requires targeted static or invented-value instrumentation |
| DR-BIN-009 | 2026-09-20 | macOS 26.8.0 | Observed/static | All seven route builders are directly mapped to the request fields and nested device shapes in `PROTOCOL.md`. | High | HTTP verb, encoding, common headers, and signing unresolved |
| DR-BIN-010 | 2026-09-20 | macOS 26.8.0 | Observed/static | QR poll codes 1, 5, 13, 14, 15, 16, 20, and 29 map to the semantic outcomes documented in `PROTOCOL.md`; unknown values are terminal. | High | Nested `-404` special case remains unnamed |
| DR-BIN-011 | 2026-09-20 | macOS 26.8.0 | Observed/static | Countdown timers tick each second; passcode polls clamp to three seconds; QR starts at three seconds and then follows the documented server-directed fallback rules. | High | No retry ceiling or jitter observed |
| DR-BIN-012 | 2026-09-20 | macOS 26.8.0 | Observed/static | Permanent QR success checks restore eligibility while temporary success skips restore; both converge on the common LOCO-login coordinator after transient challenge cleanup. | High | Final carriage `LOGIN` schema unresolved |
| DR-BIN-013 | 2026-09-20 | macOS 26.8.0 | Observed/static | Returned QR auto-login material crosses the existing device-bound encrypted-preference boundary; temporary-session persistence conditions remain unresolved. | High | Persistence flag truth table pending |
| DR-BIN-014 | 2026-09-20 | macOS 26.8.0 | Observed/static | `/mac/account/destroy.json` unregisters the current Mac device during logout; it is not account deletion. | High | Body, response, and failure cleanup unresolved |
| DR-BIN-015 | 2026-09-23 | macOS 26.8.0 | Observed/static | The seven registration builders issue `POST` requests whose parameter dictionaries, including nested `device` dictionaries, are passed through Alamofire `URLEncoding.httpBody`. | High | Common headers, cookies, signing, and empty-value omission remain unresolved |
| DR-REF-001 | 2026-09-23 | Alamofire `URLEncoding` | Published/upstream source | `URLEncoding.httpBody` uses form content type `application/x-www-form-urlencoded; charset=utf-8`, bracketed nested keys, numeric Booleans, percent-escaped spaces, and sorted top-level keys. | High | Library contract; nested dictionary pair order is not guaranteed and is not treated as semantic |
| DR-DSP-001 | 2026-09-20 | Android 26.8.2 / macOS 26.8.0 | Observed/disposable | An authorized QR approval reached Android success, after which the Mac failed its post-registration server connection and the device did not remain listed. | High | Registration-to-session handoff failure unresolved |
| DR-DSP-002 | 2026-09-20 | Android 26.8.2 / macOS 26.8.0 | Observed/disposable | A later controlled QR approval was rejected with protocol status `-997`, selecting the secondary-device protection message and terminating registration. | High | No further attempts until protection plausibly ages out |
| DR-AND-001 | 2026-09-23 | Android 26.8.2 | Observed/static comparison | The Android QR-generate model is `{status, url, remainingSeconds}` and its request contains `{device:{name,uuid,model,osVersion}, previousId?}`. | High | Android-side schema; not Mac wire proof |
| DR-AND-002 | 2026-09-23 | Android 26.8.2 | Observed/static comparison | The Android QR-login response model contains polling fields `nextRequestIntervalInSeconds`, `passcode`, `remainingSeconds`, nested `user{userId,countryIso,accountId?,displayAccountId?}`, and success token fields `accessToken`, `refreshToken`, `tokenType`, and `nonce`, in addition to `status`. | High | Android-side schema; Mac success mapping remains unresolved |
| DR-AND-003 | 2026-09-23 | Android 26.8.2 | Observed/static comparison | The Android scanner recognizes `/talk/account/qrCodeLogin/info.json?id=...`, extracts the raw identifier suffix, and queries account-side QR info; scheme and host validation were not observed in this path. | High | Android-side URL grammar; Mac host and local validator remain unresolved |
| DR-AND-004 | 2026-09-23 | Android 26.8.2 | Observed/static comparison | The authenticated Android primary computes an HMAC-SHA256 response over the QR challenge and posts `{id, macResponse, forceLogin}` to account-side QR authorization. | High | Authorization operation shape confirmed; account-secret identity and Mac validator remain unresolved |
| DR-AND-005 | 2026-09-23 | Android 26.8.2 | Observed/static comparison | Android primary-side unregistered-PC confirmation posts `{id, passcode, forced?, permanent?}` to `qrCodeLogin/confirm` and receives a status-only result. | High | Complementary Android flow; Mac password-check ownership remains separate |
| DR-MAC-016 | 2026-09-23 | macOS 26.8.0 | Observed/static | The shared Mac account-login response handler directly reads `userId`, `countryIso`, `accountId`, `access_token`, `refresh_token`, `token_type`, `server_time`, `autoLoginAccountId`, and `displayAccountId`. | High for key names; medium for QR association | General decoder inventory; direct QR `/login` carriage, optionality, wire types, and persistence mapping remain unresolved |

## Transfer review

Only behavioral state, typed field semantics, timing rules, and synthetic examples
may move into the public specification and Go model. Binary addresses, proprietary
names or expression, raw QR payloads, credentials, account/device identifiers, and
decompiler output remain private under `../../.lab/` or the external lab root.

DR-EXP-001 passed transfer review on 2026-09-20. The route family, state and timer
semantics, result categories, cleanup rules, and success-mode branches may inform
the Go state model. Candidate request fields remain explicitly non-authoritative,
and no network implementation may be derived from them alone.

DR-EXP-002 and DR-EXP-003 passed transfer review on 2026-09-20. Direct request
shapes, QR numeric results, polling policy, success handoff, storage boundary, and
current-device unregister attribution may inform implementation. Unresolved HTTP
transport/signing, success schemas, persistence flags, final LOCO login, and
revocation details remain non-implementable gaps.

DR-AND-001 through DR-AND-005 passed transfer review on 2026-09-23 as a
version-labeled Android comparison addendum. They may constrain the public model
and guide future differential testing, but they must not be substituted for Mac
26.8.0 wire observations. No Android account values, QR payloads, tokens,
binary addresses, or decompiler output are included here.

DR-BIN-015 and DR-REF-001 passed transfer review on 2026-09-23. Together they
support an offline, deterministic request-body codec. They do not authorize a
live transport or resolve headers, cookies, signing, response schemas, QR URL
validation, or authentication-result persistence.

DR-MAC-016 passed transfer review on 2026-09-23 as a version-labeled Mac shared
decoder inventory. It may inform the response model's candidate field set, but
it must not be treated as direct QR wire proof or as evidence of required fields,
wire types, QR URL validation, or final persistence/LOCO `LOGIN` mapping.

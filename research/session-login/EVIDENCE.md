# Session-login evidence ledger

Append new entries and explicitly supersede hypotheses. Never publish live tokens,
device/account identifiers, private endpoints, or raw binary/capture material.

| ID | Date | Version | Evidence class | Statement | Confidence | Status |
| --- | --- | --- | --- | --- | --- | --- |
| SL-BIN-001 | 2026-09-20 | macOS 26.8.0 | Observed/static | Booking, ticket/check-in, and carriage are separate agents with distinct address pools and retry behavior. | High | Exact transition policy incomplete |
| SL-SYN-001 | 2026-09-20 | macOS 26.8.0 | Observed/synthetic | LOCO uses a 22-byte little-endian header, BSON bootstrap bodies, and incremental parsing across fragmented/coalesced reads. | High | Malformed-input limits incomplete |
| SL-SYN-002 | 2026-09-20 | macOS 26.8.0 | Observed/synthetic | Secure-layer type 3 uses a 16-byte key, RSA-OAEP handshake, and AES-128-GCM envelopes with fresh 12-byte IVs. | High | Negotiation/fallback incomplete |
| SL-BIN-002 | 2026-09-20 | macOS 26.8.0 | Observed/static | Registration clears transient QR/device-auth values before common login; downstream login consumes at least user identity, access token, and background state. | Medium/high | Final carriage schema unresolved |
| SL-BIN-003 | 2026-09-20, corrected 2026-09-28 | macOS 26.8.0 | Observed/static + controlled live validation | All seven registration operations use `https://katalk.kakao.com`, POST, JSON bodies with nested device dictionaries, JSON responses, and the reviewed Mac compatibility headers. No registration-specific signing transform was observed. | High | The earlier URL-form attribution was disproved by serializer tracing and live generate/cancel |
| SL-BIN-004 | 2026-09-20 | macOS 26.8.0 | Observed/static | QR password check succeeds only for HTTP 200 plus a dictionary integer `status` equal to zero. | High | Complete registration success schemas remain open |
| SL-BIN-005 | 2026-09-20 | macOS 26.8.0 | Observed/static | Final carriage authentication uses `LOGINLIST` with the 17 typed fields in `PROTOCOL.md`; `sKey` is unset and the prepared access token is passed unchanged as `oauthToken`. | High | Unset object omit/null behavior awaits synthetic proof |
| SL-BIN-006 | 2026-09-20 | macOS 26.8.0 | Observed/static | LOGINLIST treats status 0 and -305 as login success, -310 as generic partial success, and -445 as login blocked. | High | Semantic labels for negative statuses unresolved |
| SL-BIN-007 | 2026-09-20 | macOS 26.8.0 | Observed/static | Cached carriage routing is expiry-checked and cleared after matching-endpoint failure; ordinary recovery reruns login with chat/token cursors and rejects stale, concurrent, network-unreachable, or unauthenticated attempts. | High | Exact recovery delays incomplete |
| SL-BIN-008 | 2026-09-20 | macOS 26.8.0 | Observed/static | CHANGESVR clears routing for fresh discovery; KICKOUT terminates the session, and reasons 1 and 10 request local database reset. | High | Current reason semantics unresolved |
| SL-PUB-001 | 2026-09-20 | pinned OpenKakao/node-kakao revisions | Public prior art | Public sources corroborate GETCONF -> CHECKIN -> LOGINLIST and `oauthToken`, but conflict on `rp`, revisions, fixed profile values, resume, and historical registration routes. | Medium | Leads and negative compatibility tests only |
| SL-LIVE-002 | 2026-09-28 | clean-room Go probe / disposable account | Observed/controlled | A fresh process loaded only the client-owned persisted state, completed TLS `GETCONF`, TLS `CHECKIN`, secure-v3 carriage setup, and `LOGINLIST` with status 0. Repeating the sequence required no QR approval and established a new authenticated session each time. | High | Live endpoint values and account/session material retained only outside the repository |
| SL-LIVE-003 | 2026-09-28 | clean-room Go probe / Android 26.8.2 | Observed/controlled | While the authenticated carriage remained connected, a synthetic 38-byte sentence sent to the owned account's self-chat arrived as an unsolicited `MSG`. Its BSON contained `chatId`, `chatLog`, `logId`, `noSeen`, `pushAlert`, and `status`; recursive typed decoding matched the exact sent string without publishing it. | High | Establishes live inbound text delivery; full `chatLog` field schema, acknowledgements, and durable cursor handling remain open |

## Transfer review

Only implementation-neutral fields, types, encodings, state transitions, errors,
timing policy, and synthetic examples may move into `PROTOCOL.md` and Go. Private
names, addresses, decompiler expression, and live values remain outside Git.

SL-EXP-001 through SL-EXP-003 passed transfer review on 2026-09-20. Registration
HTTP transport, LOGINLIST fields/types, exact current token placement, status
predicates, endpoint-cache behavior, and recovery gates may inform clean-room Go
models. Unresolved serializer, QR-validation, status-label, cookie, and catch-up
details remain non-normative.

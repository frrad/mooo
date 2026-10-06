# Live-validation and reversing debt

This is the single ledger of work that needs the maintainer's lab: controlled
owned-account experiments against the live Kakao service, static or dynamic
analysis of the official clients, and lab harnesses that execute official-client
code. Implementation work proceeds offline (synthetic fixtures and the scripted
backend) and records what it could not prove here.

## Rules

- Agents do not run live experiments while implementing. When a change needs a
  live check or an official-client trace it could not perform, the same pull
  request adds a row here. Docs elsewhere refer to rows by ID, for example
  `(LV-3)`.
- Rows are closed only with sanitized evidence: link the experiment note or
  dossier section, the date, and the client versions in the Status column. Never
  record credentials, identifiers, message content, or raw captures here.
- IDs are stable. Do not renumber; mark obsolete rows `withdrawn` with a reason.
- Alpha = blocks an acceptance criterion in
  [`bridge/PLAN.md`](bridge/PLAN.md#goal-and-acceptance-evidence).

## LV: live owned-account validation

Suggested execution order for a lab session is the table order.

| ID | Item | Done when | Sources | Alpha | Added by | Status |
|---|---|---|---|---|---|---|
| LV-1 | Fresh bridge-native QR enrollment is rejected by Android ("You cannot use this QR code"); failing stage unknown. | Root cause identified (instrument the Android QR-info request/response), regression test lands, one controlled retry enrolls, restart resumes without re-enrollment. Includes device-authorization code, expiry, and cancellation. | `bridge/PLAN.md` (B1), `bridge/QR-ENROLLMENT.md`, `bridge/DEPLOYMENT-VALIDATION.md` | Y | ledger seed | open |
| LV-2 | QR renderer differential: official Android renders ZXing EC=H, zero quiet zone; bridge used go-qrcode EC=Low at 512 px. | Live retry with the parity renderer recorded as accepted/rejected; feeds LV-1. | `bridge/QR-ENROLLMENT.md` | Y | ledger seed | open |
| LV-3 | Delivery from a real other participant (not the logged-in device's own messages): text, replies, photos, in direct and group portals. | Each kind observed both directions in a direct and a group portal with a second owned account. | `bridge/PLAN.md` (baseline), `bridge/DIRECT-MESSAGING-VALIDATION.md` | Y | ledger seed | open |
| LV-4 | Initial portal metadata (names, avatars, ghost profiles, roster) and membership updates (`NEWMEM`, `DELMEM`, `LEFT`, `CHGCHATST`) on existing portals. | Observed in a direct and a group portal, including one join and one leave. | `bridge/PLAN.md` (B2), `chat-metadata.md` | Y | ledger seed | open |
| LV-5 | `CHATINFO` live encoding: room type key (`t` vs `type`), `displayMembers` vs flat `i`/`k` arrays, integer widths, `bmids` presence. | One sanitized decoded-shape note per chat type (direct, group). | `chat-metadata.md` (open gaps) | Y | ledger seed | open |
| LV-6 | Outbound replies, including a reply to a message bridged before a restart. The earlier Docker probe never reached Kakao (harness event-ID bug). | Reply renders as a reply on the official client, before and after restart. | `bridge/PLAN.md` (B1), `bridge/DIRECT-MESSAGING-VALIDATION.md` | Y | ledger seed | open |
| LV-7 | Photos: direct/group transfers, Matrix room E2EE media, inbound author/timestamp encoding, inbound encrypted Kakao-to-Matrix text. | Each observed once; author/timestamp field encoding recorded. | `bridge/PLAN.md` (B1), `bridge/DEPLOYMENT-VALIDATION.md`, `media-transfer.md` | Y | ledger seed | open |
| LV-8 | Reactions both directions in direct and group portals. The Docker heart probe failed generically and was not retried; also revision conflicts, custom/mini mutation, open-chat `linkId`. | Add/remove observed both ways; the earlier failure classified. | `bridge/PLAN.md` (B1), `bridge/reaction-failure-policy.md`, `replies-and-reactions.md` | Y | ledger seed | open |
| LV-9 | Reconnect lifecycle: automatic reconnect, terminal `CHANGESVR`/`KICKOUT`, Matrix delivery failure, cleanup timeout; idle survival, network drop, concurrent secondary login, revocation (last). | Each scenario run once with catch-up-before-live confirmed; revocation run last. | `bridge/PLAN.md` (B3), `reconnect.md`, `bridge/continuity-failure-policy.md` | Y | ledger seed | open |
| LV-10 | Does `SYNCMSG` with `cnt=0` mark recovered messages read on the server? Gates the catch-up read-side-effect policy and enabling backfill by default. | A/B procedure executed and the policy updated. | `bridge/syncmsg-read-side-effect-procedure.md`, `bridge/PLAN.md` (B0) | Y | ledger seed | open |
| LV-11 | Continuity server facts: `cur` inclusivity in every branch, retention/permission/status failure classes, live cursor boundaries; `INFOLINK` optional/empty responses (not alpha). | Each fact recorded with a sanitized observation. | `message-continuity.md`, `protocol-parity.md`, `typed-events.md` | Partly | ledger seed | open |
| LV-12 | Operator recovery from an unidentifiable malformed `MSG` (cannot be provoked on demand; validate if it occurs naturally or via an owned-account edge case). | Recovery path exercised once, or documented as not reproducible. | `bridge/continuity-failure-policy.md`, `bridge/parser-gap-policy.md` | Y | ledger seed | open |
| LV-13 | Standard Matrix appservice deployment: full messaging acceptance on the Docker image with persistent storage, upgrade, and restart. | Acceptance run recorded in deployment validation. | `bridge/DEPLOYMENT-VALIDATION.md` | Y | ledger seed | open |
| LV-14 | Beeper self-hosting (bbctl): config, websocket, E2EE, media, restart, offline. Needs an operator-selected Beeper account. | Separate compatibility evidence with supported configuration and limitations. | `bridge/DEPLOYMENT-VALIDATION.md` | Y | ledger seed | open |
| LV-15 | Read receipts live: `MarkRead`, `DECUNREAD` ghost/self receipts; whether a live `MSG` without `NOTIREAD` clears the sender's unread marker or leaves primary-device notifications stale. | Observed both directions; NOTIREAD question answered. | `bridge/read-receipt-policy.md`, `bridge/PLAN.md` (open questions) | N | ledger seed | open |
| LV-16 | Repeatable experiments for login, device registration, reconnect, logout, revocation (Phase 1). | A written, rerunnable procedure for each. | `../PLAN.md` (Phase 1) | N | ledger seed | open |
| LV-17 | Credential storage CS-8 against a disposable populated official Mac profile. | CS-8 exit criteria met. | `credential-storage/PLAN.md` | N | ledger seed | open |

## RV: reversing and static traces

| ID | Item | Done when | Sources | Alpha | Added by | Status |
|---|---|---|---|---|---|---|
| RV-1 | Reconnect static questions RC-Q1/Q2/Q3/Q6/Q7/Q8/Q9: recovery trigger, backoff, retry budget, `LOGINLIST` status routing, `CHANGESVR` follow-up, `KICKOUT` reasons, endpoint cache. | Evidence rows per the answer contract. | `reconnect/STATIC-QUESTIONS.md`, `reconnect.md` | Partly | ledger seed | open |
| RV-2 | Mac `qrLoginCheckKey`: what populates it and who calls it. May explain LV-1. | Full chain traced. | `bridge/QR-ENROLLMENT.md`, `session-login/PLAN.md` | Partly | ledger seed | open |
| RV-3 | Membership/chat changes: `DELMEM`/`NEWMEM` optional/null bodies, transaction failures, chat-type guards; `CHGCHATST`/`LEFT` gaps; `CHGMETA` merge keys and subtype labels; `CHGMCMETA` consumer. | Gaps closed in the inventory. | `membership-chat-change-inventory.md`, `protocol-parity.md`, `chgmcmmeta-transition.md` | Partly | ledger seed | open |
| RV-4 | Friend/contact sync: request, response, persistence, and consumers (identified, not traced). Prerequisite for a protocol-level contact list. | Dossier with full chain and synthetic fixture. | `chat-metadata.md`, `bridge/PLAN.md` (B2) | N | ledger seed | open |
| RV-5 | Chat metadata internals: room-upsert, user-upsert creation path and type-9 table, `NEWMEM` predicates, `MEMLIST` token semantics. | Gaps closed in the dossier. | `chat-metadata.md` | N | ledger seed | open |
| RV-6 | Ping/receive-timeout: serialized config key and override, initial admission source, official queue timing, idle/no-pending timeout. Gates installing the receive-header timeout owner. | Public contract for each input. | `../PLAN.md` (status/config binding), `reconnect/PING-SCHEDULER.md`, `reconnect/RECEIVE-HEADER-TIMEOUT.md` | N | ledger seed | open |
| RV-7 | Push receipts: typed incoming `HINT`/`BLOCKSYNC` eligibility through parser, notice decoder, handler; encryption caller failure handling; lower socket-tag mapping and active transport; ack method/ID contract. Also needs a product decision. | Remaining boundaries in `../PLAN.md` traced. | `../PLAN.md` (push-receipt sections) | N | ledger seed | open |
| RV-8 | Registration control plane: device listing and revocation, server time, passcode statuses and success schemas, password-check owner, persistence flags, unregister schemas, default headers/cookies. | Gaps closed in the plan. | `device-registration/PLAN.md`, `session-login/PLAN.md` | N | ledger seed | open |
| RV-9 | `LOGINLIST` and booking: unknown response key mappings, retry formulas, negotiation variants. | Gaps closed. | `session-login/PLAN.md`, `protocol-bootstrap.md` | N | ledger seed | open |
| RV-10 | Read state: `NOTIREAD` response boolean meaning; `DECUNREAD` database completion/error reporting and archive-folder mutation. | Gaps closed. | `protocol-parity.md`, `read-state-notiread.md`, `read-state/DECUNREAD.md` | N | ledger seed | open |
| RV-11 | Text/photo send: official message-ID persistence, cancellation, restart behavior. | Traced. | `protocol-parity.md` | N | ledger seed | open |
| RV-12 | Replies/reactions: all legacy selection types, reply-to-media variants, picker/search sync. | Traced. | `replies-and-reactions.md` | N | ledger seed | open |
| RV-13 | Secure framing: short-input exceptions, invalid UTF-8, embedded NUL. | Traced. | `reconnect/SECURE-FRAMING.md` | N | ledger seed | open |
| RV-14 | Credential storage CS-6/CS-7: public spec and transfer review, then guarded implementation. | CS-6/CS-7 exit criteria met. | `credential-storage/PLAN.md` | N | ledger seed | open |
| RV-15 | Phase-2 questions: concurrent-device rules, Windows/tablet parity, revocation. | Answered with evidence. | `../PLAN.md` (questions) | N | ledger seed | open |

## LH: lab harnesses and fixture upgrades

| ID | Item | Done when | Sources | Alpha | Added by | Status |
|---|---|---|---|---|---|---|
| LH-1 | First lab harness: run the official BSON dictionary decoder on fixture inputs. No harness exists yet. | Harness documented and rerunnable outside the repo. | `../PLAN.md` (parity migration), `parity-fixtures.md` | N | ledger seed | open |
| LH-2 | The invalid-UTF-8 BSON fixture `../PLAN.md` expects LH-1 to upgrade does not exist yet. | Fixture written (static), then upgraded to `executed` by LH-1. | `../PLAN.md`, `parity-fixtures.md` | N | ledger seed | open |
| LH-3 | All fixtures under `fixtures/` are `static`; none is `executed` or `observed`. Stateful ones (timeouts, ping, kickout/changesvr, socket disconnect, push receipt) need `observed` runs. | Each fixture upgraded or explicitly left as a lead. | `fixtures/`, `parity-fixtures.md` | N | ledger seed | open |
| LH-4 | Reconnect policy vectors not executed: ping intervals 0/negative/1/179/180/181, `PING` completions, receive-header 0/positive, signed tags, arm/cancel/fire/disconnect ordering. | Vectors executed against the official code. | `reconnect.md`, `reconnect/EVIDENCE.md` | N | ledger seed | open |
| LH-5 | Credential storage CS-4 isolated client-helper comparison. | Comparison recorded. | `credential-storage/PLAN.md` | N | ledger seed | open |

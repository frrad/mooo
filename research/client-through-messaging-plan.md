# Reversed client plan through text messaging

Status: proposed execution plan, revised 2026-09-23.

## Objective

Build a clean-room Go client that authenticates as a new KakaoTalk secondary
device without importing an official Mac installation's device identity,
credentials, or session state. The client will generate its own device identity,
complete QR-based authorization through the operator's primary Android client,
install the server-issued authentication state, establish a LOCO session, receive
text messages, and send text messages to an explicitly selected owned test chat.

The official Mac login is evidence and an emergency control client only. It is not
an authentication input to the reversed client.

## Progress

- **A0: partial.** The controlled official-client QR approval reached the Mac
  Friends screen briefly, but the subsequent server connection failed, the Mac
  returned to its login screen, and Android showed no registered-device row.
  This is a useful failed-handoff control, not a durable authenticated baseline.
- **A1: implemented offline.** `internal/authstate` creates a fresh client-owned
  UUID, stores versioned Mac metadata, atomically installs only complete
  server-issued credentials, enforces private permissions, rejects corruption and
  symlinks, and redacts identity and credential formatting. `mooo-lab auth init`
  and `auth inspect` expose this without network behavior or official-profile
  discovery.
- **A2: partial.** A mockable semantic registration transport/coordinator now
  carries generate, poll, and cancel effects without HTTP assumptions. Opaque QR
  presentation data is one-shot and excluded from reducer state. All seven
  directly evidenced request bodies have typed, bounded, redacted offline
  builders using the reviewed Alamofire form encoding. The complete server QR
  value is preserved while a fail-closed parser derives its single transient
  `id`; scheme, host, and check-key validation remain deliberately unresolved.
  Response decoding, common headers/cookies/signing, and success handoff remain
  blocked on evidence.

## Safety and evidence rules

- Use only the disposable account and chats the maintainer owns or is authorized
  to test.
- Generate a fresh, client-owned device UUID and state directory. Never read or
  clone the official Mac client's device UUID, token, preferences, or database.
- Keep credentials, device identifiers, chat identifiers, private messages, raw
  captures, and decrypted databases outside Git. Redact them from logs and errors.
- Default live commands to dry-run or read-only behavior. Sending requires an
  explicit destination and message supplied by the operator.
- Run one preplanned live authentication experiment at a time. After a failed live
  authentication attempt, record the time and obey the maintainer's exponential-
  backoff policy before another attempt.
- Treat public implementations as leads. Protocol behavior enters production code
  only after current-client static, synthetic, or disposable-account evidence.
- Unknown status values and incomplete success payloads fail closed. The client
  never guesses, brute-forces, or immediately retries authentication.

## Milestone A0 — Preserve a control baseline

1. Confirm the official Mac registration remains present in Android **Manage
   Devices** and that both official clients are usable.
2. Record sanitized versions, platforms, and experiment timestamps.
3. Define a private directory for the reversed client's future device identity,
   credentials, and redacted experiment events.
4. Do not log out, revoke, or copy state from the official Mac client.

Exit gate: there is a known-good official control session and an isolated empty
state directory for the new client.

## Milestone A1 — Client-owned identity and credential store

Implement a local state package that:

- creates a new random device UUID once and reuses it for that logical secondary
  device;
- stores a user-chosen device name plus versioned Mac-compatible OS/model metadata;
- supports atomic installation of server-issued user identity, access token, and
  auto-login material after successful authorization;
- uses owner-only filesystem permissions and an interface that can later move
  secrets into an OS keychain or external secret store;
- never logs values, accepts arbitrary profile discovery, or reads official Kakao
  application containers;
- distinguishes transient QR state from durable registered-device state.

Exit gate: synthetic create/load/replace/corruption tests pass and no credential
exists before a successful server response.

## Milestone A2 — Registration HTTP and QR generation

Complete the seven-operation registration codec using the current Mac profile:

- HTTPS `POST` to the reviewed registration service;
- URL-form request bodies with the exact nested device encoding;
- QR generate, poll/login, cancel, and password-check operations;
- passcode operations as typed codecs, even though the first live path uses QR;
- bounded deadlines, JSON response decoding, and redacted errors;
- ordinary platform headers/cookies only when current-client evidence shows they
  are server-significant.

Resolve before live use:

- the complete QR-generate success schema;
- the QR URL grammar and check-key validation algorithm;
- password-check ownership and when it participates in unregistered-device
  authorization;
- all success fields required to install authentication state.

The client renders the opaque server-returned QR value; it does not invent a QR
payload. QR IDs and payloads remain transient and are never logged.

Exit gate: invented requests/responses round-trip through the independent codecs,
invalid QR payloads fail validation, and synthetic secrets never enter logs.

## Milestone A3 — QR approval and new-device authorization

Implement the QR state machine end to end:

```text
generate QR
  -> poll after three seconds
  -> wait for primary-device approval
  -> if unregistered, present the server's four-character device-auth code
  -> continue server-directed polling until approved, expired, or terminal
  -> atomically install the complete server-issued authentication state
```

Preserve independent QR-display, polling, and device-authorization expiry. Support
explicit cancellation and refresh. Map the known result codes exactly, preserve
unknown codes, and keep the nested `-404` case terminal until its meaning is proven.

Initially request permanent device authorization so the generated identity can
reconnect. Skip history restoration; it is not required for authentication.

Exit gate: the pure reducer plus HTTP effects cover pending, approval,
unregistered-device authorization, rejection, expiry, restriction, malformed
responses, cancellation, refresh, permanent success, and stale callbacks.

## Milestone T1 — LOCO codec and secure transport

Implement and test independently of the network:

- the fixed 22-byte little-endian LOCO frame;
- bounded incremental parsing for fragmented and coalesced reads;
- BSON request/response encoding with exact integer widths, arrays, binary values,
  booleans, and absent values;
- request-ID correlation and unsolicited server-command dispatch;
- secure-layer type 3 handshake using the versioned embedded RSA public key;
- RSA-OAEP key wrapping and AES-128-GCM envelopes with fresh 12-byte IVs;
- deadlines, bounded lengths, clean shutdown, and redacted protocol errors.

Synthetic tests include pinned vectors, malformed lengths, truncated frames, bad
authentication tags, unknown commands, duplicate IDs, and partial reads. Resolve
the `LOGINLIST.sKey` omit-versus-null question before any live request.

Exit gate: invented packets round-trip byte-for-byte and hostile inputs fail closed
without unbounded allocation or payload disclosure.

## Milestone T2 — Bootstrap routing and LOGINLIST

Specify and implement:

```text
GETCONF -> CHECKIN -> secure carriage connection -> LOGINLIST
```

Recover the exact current request and minimum response fields for endpoint
selection. Implement endpoint expiry, address ordering, bounded retries,
cancellation, and matching-endpoint invalidation. Implement the reviewed 17-field
`LOGINLIST` request using only state issued through A3.

Accepted login state is installed atomically. Unknown statuses fail closed;
CHANGESVR, KICKOUT, disconnect, and expired authentication remain distinct.

Exit gate: synthetic bootstrap and login fixtures reach authenticated state without
any unresolved required field being filled from historical assumptions.

## Milestone L1 — One bounded end-to-end self-authentication

1. Start with the reversed client's fresh device identity and empty credentials.
2. Generate and display one QR challenge.
3. Have the operator scan and approve it in the owned Android primary client,
   including the new-device authorization step.
4. Install only the returned server-issued state.
5. Run `GETCONF -> CHECKIN -> LOGINLIST` once.
6. On success, remain idle briefly, record only redacted command names, timings,
   sizes, endpoint classes, and status codes, then disconnect cleanly.
7. Confirm Android **Manage Devices** lists the new client identity and that the
   official primary session remains usable.

A failed live authentication attempt ends the experiment and schedules the next
attempt under the backoff policy. It does not fall back to importing the official
Mac session or guessing credentials.

Exit gate: a brand-new client-owned identity completes authorization and LOCO login
without reading any official desktop-client state.

## Milestone L2 — Reconnect with self-issued state

Restart the Go client using only the durable state created by A3. Confirm it can
repeat bootstrap and `LOGINLIST` without another QR approval, then test one clean
disconnect/reconnect cycle. Do not fetch messages yet.

Exit gate: the reversed client owns a persistent secondary-device registration and
can reconnect without the official Mac installation.

## Milestone M1 — Specify inbound text delivery

Trace the current client from authenticated carriage dispatch through:

- initial chat-list and synchronization commands;
- unsolicited new-message command names and payload schemas;
- chat, sender, server message/log ID, client ID, timestamp, and text fields;
- acknowledgements and whether they are transport-, delivery-, or read-level;
- ordering, duplicate suppression, cursor advancement, and reconnect catch-up;
- unknown message types and malformed payload behavior.

Implement a read-only event model that preserves unknown types without presenting
them as text. Separate receipt from read receipts; the first client must not mark a
conversation read unless the protocol requires and documents it.

Exit gate: synthetic fixtures cover ordered delivery, duplicates, gaps, reconnect,
unknown message types, and malformed payloads.

## Milestone M2 — Receive one live text message

Have a maintainer-controlled peer send one unique, non-sensitive text message to a
dedicated test chat. Verify that the client emits it once, advances only proven
delivery state, does not mark it read, and does not duplicate it after reconnect.

Do not commit content or live identifiers.

Exit gate: one inbound text is decoded exactly once and reconnect behavior is
understood.

## Milestone M3 — Specify outbound text sending

Recover and implement:

- the text-send command and request/response schemas;
- chat ID, text, client-generated ID, timestamps, flags, and reference fields;
- client-ID generation and idempotency rules;
- reconciliation to the server-assigned message/log ID;
- timeout, ambiguous delivery, duplicate, rejection, permission, and rate-limit
  outcomes.

Never automatically retry an ambiguous send. The CLI requires an explicit chat and
message and shows a redacted dry-run summary before transmission.

Exit gate: synthetic fixtures prove one-send/one-result behavior and safe handling
of timeouts and unknown statuses.

## Milestone M4 — Send one live text message

Send one unique, non-sensitive text message to a maintainer-controlled test chat.
Verify exactly one copy through an official receiving client, reconcile the client
ID to the server ID, reconnect, and confirm no duplicate appears.

Exit gate: the reversed client can independently authorize, reconnect, receive text
messages, and send exactly one text message with understood acknowledgement and
deduplication behavior.

## Deferred until after M4

- bulk history/backfill and full contact synchronization;
- read receipts, typing, reactions, replies, edits, deletion, media, and calls;
- passcode/password login as a user-facing alternative to QR;
- durable local message storage beyond minimal cursors and deduplication state;
- multiple accounts or arbitrary profile discovery;
- Matrix/Beeper integration and production deployment.

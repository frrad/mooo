# Read-state parity: inbound NOTIREAD and explicit read orchestration

Status: first-party static trace, macOS KakaoTalk 26.8.0, reviewed 2026-09-30.

This dossier records behavior recovered from the authorized official Mac client
with Ghidra. It is deliberately implementation-neutral: no decompiler output,
addresses, account values, message contents, or proprietary assets are
included.

## Automatic inbound acknowledgement

The inbound `MSG` path has a distinct notification-read acknowledgement. The
complete observed chain is:

1. The LOCO push handler accepts the `MSG` packet and forwards it to the
   message-push delegate.
2. The delegate reads the message's chat ID and link ID, then looks up the
   existing room using both identifiers. A missing room ends this branch; it
   does not create a room solely to send the acknowledgement.
3. For an existing room, it reads the room's notification-read boolean, the
   accepted message's log ID, and that message's service ID.
4. It invokes the LOCO client operation named `NOTIREAD` with those values.

The request model recovered from the official request class contains five
fields. The request uses the short wire property `li` for the link ID (the
model's setter is `setLi:`):

| field | wire type | meaning |
| --- | --- | --- |
| `chatId` | int64 | room identifier |
| `li` | int64 | link identifier, including zero for direct chats |
| `watermark` | int64 | accepted message log ID |
| `notiRead` | boolean | room notification-read value |
| `serviceId` | int32 | accepted message service identifier |

The initializer writes all five values before the request is handed to the
carriage manager. The request is therefore not a `DECUNREAD` packet and is not
an application commit acknowledgement. It is an automatic notification-read
mutation associated with one accepted inbound message.

## Response and failure boundary

The request is sent through the shared carriage request path with a completion
callback. The static trace confirms request construction and dispatch, but does
not establish a retry loop, a local watermark write, or a caller-visible
interpretation of a failed `NOTIREAD` completion. Until a response callback is
traced at the model and caller boundary, implementations must treat a
transport error, malformed response, disconnect, timeout, or non-zero LOCO
status as an unacknowledged automatic mutation and must not retry it
implicitly. The inbound message itself remains delivered independently of this
follow-up.

The response model has a `notiRead` boolean accessor and accepts the shared
packet-header response. Its exact server-side acknowledgement status semantics
remain open; that is intentionally not guessed by the conformance tests in
this change.

## Explicit user read operations

Separate static traces of `markAsReadOfChatRoom:`, summary-and-read, and
read-all show no direct `NOTIREAD` call and no separate mark-read LOCO method.
These operations route through the existing local `SYNCMSG` coordinator with a
room's current last-log or seen-log ceiling. `CHATONROOM` is room setup and
`CHATOFF` is local teardown/sync cancellation in the reviewed paths; neither
is an explicit read acknowledgement wire command.

The shared request boundary is significant: a successful status-zero `SYNCMSG`
may advance server-visible read state even when the returned `chatLogs`
collection is empty, while a non-zero status or transport failure does not
constitute a successful read acknowledgement. Local application message
commit and read-watermark persistence must remain separate transactions.

## Test handoff

`internal/protocol/notiread/notiread_test.go` encodes the five-field request
shape, BSON widths, and the no-implicit-retry/failure contract as synthetic
tests. The package has no production implementation on this branch by design.
The implementation stage should add the smallest transport-independent request
codec and wire it into the existing one-shot carriage path, then make these
tests pass without coupling automatic notification-read state to application
message commits. Response status semantics remain deliberately untested until
the response callback and caller boundary are traced.

## Provenance

- Source: authorized official KakaoTalk Mac binary, version 26.8.0, analyzed
  read-only in the maintained Ghidra project.
- Method: exact Objective-C selector lookup, caller/callee tracing, request
  model initializer tracing, and decompilation of the inbound message handler,
  request sender, request initializer, response initializer/accessor, and
  explicit mark-read coordinators.
- Confidence: high for the inbound call chain and request fields/types;
  medium for the short `li` wire spelling until a clean-room live capture or
  serializer-level trace confirms it; low/open for response status and
  completion-side effects.

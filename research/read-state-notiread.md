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

The request-specific completion block is now traced through the response model.
It wraps a non-null packet in the official `NOTIREAD` response object and
passes a nil packet through as a nil response; it does not inspect a status
field. The shared pending-response path removes the matching callback and
invokes it with the packet and nil error, also without a response-status
branch. No dedicated `NOTIREAD` persistence or downstream consumer was found.

The response model's `notiRead` accessor reads an optional boolean-like value
from extra information and defaults to true when absent. The exact key and
server acknowledgement meaning remain open. Carriage-unavailable requests
receive an error without a packet, while a receive timeout disconnects the
agent. Pending-callback fan-out after disconnect was not proven, so a timeout
or disconnect remains an ambiguous, unacknowledged mutation and must not be
treated as local read success or retried implicitly. The inbound message
remains delivered independently of this follow-up. See
[`research/read-state-notiread-response.md`](read-state-notiread-response.md)
for the layer-by-layer trace and conformance boundary.

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
shape, BSON widths, the no-implicit-retry/failure contract, and raw response
pass-through as synthetic tests. The response test is a passing
characterization test: it protects the observed absence of status
interpretation without inventing a success predicate. The implementation
stage must keep automatic notification-read state separate from application
message commits; disconnect callback fan-out and response status semantics
remain explicit follow-up gaps.

## Provenance

- Source: authorized official KakaoTalk Mac binary, version 26.8.0, analyzed
  read-only in the maintained Ghidra project.
- Method: exact Objective-C selector lookup, caller/callee tracing, request
  model initializer tracing, and decompilation of the inbound message handler,
  request sender, request initializer, response initializer/accessor, and
  explicit mark-read coordinators.
- Confidence: high for the inbound call chain, request fields/types, and the
  traced packet-forwarding boundary; medium for the short `li` wire spelling
  until a clean-room live capture or serializer-level trace confirms it and
  for the optional response boolean's exact meaning; open for pending-callback
  fan-out after timeout/disconnect.

# Typed receipt integration boundary

Status: design contract only; no runtime binding or default activation is
proposed by this document. This slice depends on the reviewed incoming scalar
projection contract (merged PR209) and the merged receipt body, packet,
encryption, and Session lifecycle contracts.

The existing Session seam is already generic and opt-in. Its current
`EligiblePushReceiptPacket` helper only checks the reviewed method names and
nonzero body-length consistency; it deliberately admits arbitrary consistent
raw bytes for seam tests and is not the final typed upstream eligibility
contract. `BindPushReceipt`
accepts a `PushReceiptSender` (`Send(packet any) error`) and an injected
`func(loco.Packet) bool` eligibility predicate before reader startup. When
`dispatchPacket` cannot correlate an incoming packet, `readLoopBody` forwards
the original `loco.Packet` to `dispatchPushReceipt`; the normal unsolicited push
stream continues independently. `Session.Shutdown` joins the binding worker
and any optional sender owner. The seam does not allocate IDs, build packets,
choose wire tags, encrypt, retry, or insert pending-map entries. `readLoopBody`
queues the receipt callback before enqueueing the ordinary push stream, but the
worker is asynchronous; that queue order does not establish typed-consumer
completion before sending.

A future typed adapter should therefore have one source-qualified eligibility
boundary and one explicit composition boundary:

1. The approved parser/notice/handler contract converts an unmatched
   `loco.Packet` into a typed receipt input or rejects it. Header method,
   body-length consistency, BSON decode outcome, and source-specific nil or
   empty behavior belong to this boundary. It must distinguish absent values,
   BSON `null`, signed int32 zero, and malformed or truncated fields rather than
   treating all of them as Go zero values.
2. The adapter receives a caller-owned `uint32` packet ID and a typed
   `sessionlogin.ReceiptBody`. `BuildReceiptBody` produces the reviewed BSON
   body; `BuildReceiptPacket` copies the caller-supplied explicit ID and method
   into the plaintext LOCO header. The source-qualified adapter should select a
   matching method/body-kind pair before calling it; the constructor itself does
   not prove or add a generic method/kind validation rule. Neither function
   allocates an ID or touches pending request state.
3. The existing reviewed encryption and write owners compose around the
   plaintext packet. The signed receipt admission tag stays separate from the
   uint32 header ID and from any lower socket-write tag. The adapter reports
   source-defined admission or write outcomes through its injected sender
   contract; it does not add retries or acknowledgements that the source has
   not established.

The source contract has four distinct value cases that must remain separate
before runtime binding:

* An empty input dictionary in the HINT nested `ChatLog` path is a normal
  successful value; the outgoing HINT receipt body is the reviewed canonical
  empty BSON document. It is not a nil-body error.
* An observed `NSNull` source key is removed without assigning its destination;
  the SGJson projection skips remaining `NSNull` values. It is not a Go zero
  value and does not, by itself, reject the notice.
* A nil body that reaches the observed SGJson operation raises. A future typed
  adapter must expose that as a Go error while retaining the original input
  packet for ordinary push delivery.
* A nil notice returned by the notice initializer is a separate boundary. The
  original packet/header remains available: the default handler can pass the
  nil notice and original header to the optional delegate, then continue to
  receipt construction. HINT receipt construction uses the header; BLOCKSYNC
  nil revision getters yield zero before receipt construction. This source
  path is conditional on the delegate returning normally; it does not forbid
  the resulting zero-valued receipt.

The nested `LocoChatLog` lineage is bounded at the model boundary. In the
26.8.0 arm64 metadata, `LocoChatLog` is a `LocoModel` subclass and declares
typed properties including signed `type`, `scope`, `referer`, `revision`, and
`sentAt`, 64-bit IDs, a `BOOL` silence flag, a `double` expiry, and string
message/attachment/supplement/extra fields. It does not declare its own
`initWithJSONObject:` override: construction uses `LocoModel` IMP
`0x10167beb0`, which copies dictionary input, applies the model mapping block,
and forwards to `SGJsonObject`. The source evidence does not establish an
additional mandatory-field validator on this path. Property conversion and
unsupported KVC input failures therefore remain the SGJson/KVC boundary; the
adapter must receive a notice that already passed the appropriate upstream
constructor path rather than treating arbitrary decoded maps as eligible.

Strict BSON rejection is not part of this source contract. The source BSON
decoder returns the documented partial dictionary on an unknown element type;
that observed behavior remains the compatibility boundary and is separate from
the SGJson mapping phase. Any future stricter clean-room policy would require a
separate review and must preserve the original packet for the ordinary push
path. The
current builders intentionally encode only their typed inputs; this plan does
not broaden them into a generic JSON converter.

The proposed boundary can be expressed without changing the current runtime
API:

```go
type ReceiptNoticeDecoder func(loco.Packet) (sessionlogin.ReceiptBody, error)
type ReceiptPacketComposer func(uint32, string, sessionlogin.ReceiptBody) ([]byte, error)
```

The decoder is source-qualified and returns a typed error for the third case
above. A future adapter would call `BuildReceiptPacket` once through the
composer boundary; it must not call `BuildReceiptBody` separately and then
call `BuildReceiptPacket`, because the latter already builds the body. The
composer receives the caller-owned `uint32` ID and method, and neither boundary
allocates IDs, mutates pending state, or enables the current opt-in hook.

The adapter must remain opt-in. No default Session constructor path should
call `BindPushReceipt`, create a new allocator, reuse the signed tag as a
request ID, or treat an inbound receipt as an acknowledgement until the
eligibility and completion contracts are independently approved.
Once those eligibility and completion contracts are approved, a later client
phase may enable the typed adapter as the normal receipt path; this document
does not make that readiness gate permanent.

Concrete source/code anchors are `internal/client/session.go`
(`BindPushReceipt`, `dispatchPushReceipt`, and `readLoopBody`),
`internal/protocol/sessionlogin/receipt_body.go` (`BuildReceiptBody`), and
`internal/protocol/sessionlogin/receipt_packet.go` (`BuildReceiptPacket`).
Synthetic tests should cover the four distinct nil/empty/`NSNull`/initializer
outcomes above, signed-int32 distinctions,
caller-owned ID preservation at zero and `math.MaxUint32`, rejection without
pending-map mutation, and ordinary push ordering when the typed boundary
rejects an input.

## Delegate-consumer source boundary

The reviewed handler chain is narrower than a typed receipt consumer. In the
macOS 26.8.0 source inventory, `handleHintPushNotice:packetHeader:` (IMP
`0x101515264`) and `handleBlockSyncPushNotice:packetHeader:` (IMP
`0x101517988`) retain the notice/header and first attempt the optional
delegate callbacks
`locoManager:didReceiveHintPushNotice:` (callsite `0x10151532c`) and
`locoManager:didReceiveBlockSyncPushNotice:` (callsite `0x101517a50`) under
their respective responds-to-selector checks. They then construct their notice
packet and call `sendCarriagePushReceipt:`. Absence of a delegate does not
suppress that receipt attempt. The callback recipient is the captured
delegate; this paragraph does not identify it as the manager object itself.

This is ordering evidence, not proof of the manager's downstream consumer.
The callback's persistence, queue handoff, and failure handling after the
captured delegate receives the notice remain an explicit source gap. In particular, the
current Session hook's callback queue ordering cannot stand in for completion
of either manager callback, and the two callbacks must not be treated as
request constructors. The bounded synthetic contract should therefore assert
delegate-present and delegate-absent handler effects separately, while leaving
typed consumer completion and persistence unimplemented until the downstream
manager chain is traced.

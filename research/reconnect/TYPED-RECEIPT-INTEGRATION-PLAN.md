# Typed receipt integration boundary

Status: design contract only; no runtime binding or default activation is
proposed by this document. This slice depends on the reviewed incoming
eligibility contract (held in PR203) and the merged receipt body, packet,
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

* HINT's observed empty projected dictionary is a normal successful value. It
  is not a nil-body error and must encode as the reviewed empty BSON document.
* An observed `NSNull` field is skipped by the reviewed projection/mapping
  phase. It is not a Go zero value and does not, by itself, reject the notice.
* A nil body/notice projection that reaches a source operation which raises or
  returns no object needs a typed Go error outcome, while the original input
  packet remains available to ordinary push delivery.
* A nil result from the packet initializer is a separate source boundary: the
  callback path has no packet object to consume, but the handler's receipt
  attempt is not suppressed merely because the optional delegate callback
  forwarded a nil notice. This is a lower-initializer failure, not permission
  to convert the notice into an empty HINT or zero-valued BLOCKSYNC.

Strict BSON rejection is an explicit clean-room implementation-policy choice,
not an observed official-client rule. The approved source mapping permits the
documented partial-dictionary behavior for unsupported or skipped fields; an
adapter may choose strict typed decoding, but it must label that choice as a
policy and preserve the original packet for the ordinary push path. The
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
`0x101517988`) retain the notice/header, construct their notice packet, and
attempt the optional manager callbacks
`locoManager:didReceiveHintPushNotice:` (callsite `0x10151532c`) and
`locoManager:didReceiveBlockSyncPushNotice:` (callsite `0x101517a50`) under
their respective responds-to-selector checks. The `sendCarriagePushReceipt:`
call follows packet construction and the delegate attempt in both handlers;
absence of a delegate does not suppress that receipt attempt.

This is ordering evidence, not proof of the manager's downstream consumer.
The callback's persistence, queue handoff, and failure handling after the
manager receives the notice remain an explicit source gap. In particular, the
current Session hook's callback queue ordering cannot stand in for completion
of either manager callback, and the two callbacks must not be treated as
request constructors. The bounded synthetic contract should therefore assert
delegate-present and delegate-absent handler effects separately, while leaving
typed consumer completion and persistence unimplemented until the downstream
manager chain is traced.

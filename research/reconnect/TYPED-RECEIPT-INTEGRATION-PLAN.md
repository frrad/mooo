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

The source contract leaves two implementation choices to resolve explicitly
before runtime binding. First, a source nil/empty/`NSNull` projection that
would raise or return no object needs a typed Go error outcome, with the
original input packet preserved for ordinary push delivery. The adapter must
not silently turn that outcome into an empty HINT or zero-valued BLOCKSYNC.
Second, BSON decoding should remain strict for the source-qualified fields and
reject unsupported types unless the reviewed source mapping proves a partial
projection. The current builders intentionally encode only their typed inputs;
this plan does not broaden them into a generic JSON converter.

The adapter must remain opt-in. No default Session constructor path should
call `BindPushReceipt`, create a new allocator, reuse the signed tag as a
request ID, or treat an inbound receipt as an acknowledgement until the
eligibility and completion contracts are independently approved.

Concrete source/code anchors are `internal/client/session.go`
(`BindPushReceipt`, `dispatchPushReceipt`, and `readLoopBody`),
`internal/protocol/sessionlogin/receipt_body.go` (`BuildReceiptBody`), and
`internal/protocol/sessionlogin/receipt_packet.go` (`BuildReceiptPacket`).
Synthetic tests should cover nil/empty/`NSNull`/signed-int32 distinctions,
caller-owned ID preservation at zero and `math.MaxUint32`, rejection without
pending-map mutation, and ordinary push ordering when the typed boundary
rejects an input.

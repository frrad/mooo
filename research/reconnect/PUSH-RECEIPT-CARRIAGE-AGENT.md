# Push-receipt carriage-agent selection

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-05. Client build: macOS KakaoTalk 26.8.0.

`sendCarriagePushReceipt:` obtains the current `carriageAgent` at callsite
`0x101515000` and invokes inherited receipt behavior through that object. The
manager's `connectToCarriageServer:completion:` allocates an agent at
`0x101518340`, calls
`initWithHost:port:serverType:secureLayerType:connectTimeout:receiveHeaderTimeout:inSegmentTimeout:outSegmentTimeout:`
at `0x1015183f8`, then disables fallback, stores the new object with
`setCarriageAgent:` (`0x10151843c`), installs the status handler, sets manager
status `0x15`, and calls `connect`. The callback's agent identity is therefore
the newly constructed object on this traced path. Later reads of the property
from connect, logout, request, and login-list paths remain dynamic reads.

The class hierarchy metadata distinguishes `LocoAgent` (base `NSObject`),
`LocoTrailerAgent` (subclass of `LocoAgent`), and `LocoNWAgent` (subclass of
`LocoAgent`). `sendPushReceipt:` is inherited, while `sendPacket:tag:` has the
base LocoAgent implementation and a distinct NW transport wrapper. The exact
class selected by allocation/factory for every runtime route is not established
by this slice. The fixture represents that as an explicit factory-selection
input with no invented identity or nil-property behavior. The traced manager
constructor path is recorded independently of that unresolved dispatch question.

The request constructors have established header method/packet ID copying for
HINT and signed-32 revision/plus-revision storage for BLOCKSYNC. BSON/body
serialization, default field values, and constructor failure outputs remain
explicit gaps in this slice; no packet builder is inferred from selector names.

## Synthetic contract

The fixture records receipt reads, the connect-time construction/setter
sequence, and factory selection as separate operations. It checks current, new,
and callback identities from inputs and preserves unresolved factory selection
as an explicit gap; it does not select behavior from case names or property
absence.

## Provenance

- `sendCarriagePushReceipt:` carriage-agent read: `0x101515000`.
- Manager allocation and constructor: `0x101518340` and `0x1015183f8`.
- Manager setup after construction: disable fallback `0x101518430`, setter
  `0x10151843c`, status handler `0x101518508`, manager status `0x10151851c`,
  and connect `0x101518534`. The private connect-lifecycle report records the
  sequence.
- Later manager reads: `0x101518458`, `0x1015184f4`, and `0x101518524`.
- Class hierarchy: private Objective-C table extraction for `LocoAgent`,
  `LocoTrailerAgent`, and `LocoNWAgent`.
- Request constructor receipts: private Ghidra
  `parity/receipt-request-model/`; no proprietary artifacts are tracked.

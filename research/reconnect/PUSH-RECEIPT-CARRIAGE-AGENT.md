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
base LocoAgent implementation and a distinct NW transport wrapper. The manager
allocation at `0x101518340` loads class reference `0x102114260`, which resolves
through `0x102182700` to `LocoNWAgent`. Thus the traced manager path selects
`LocoNWAgent`; other producer routes remain separate runtime questions.

The request hierarchy is `LocoHintPushReceipt` and `LocoBlockSyncPushReceipt`
over `LocoPushReceipt`, which is a `LocoModel`/`SGJsonObject`. The base receipt
declares `method` (`NSString`) and `packetId` (`uint32`); HINT adds no object
fields. BLOCKSYNC declares signed 32-bit `revision` and `plusRevision`. The
constructor receipts show method/packet-ID copying and revision setter calls.
The synthetic fixture checks only this object-field inventory. Value propagation
for method, packet ID, and signed revisions is covered by the constructor
contract in `PUSH-RECEIPT-REQUEST-MODEL.md`. `sendPacket:tag:` obtains
`packetData` and passes it through `encryptPacketData:` before socket write;
the exact serialized key encoding and encryption output remain outside this
source slice. This packet path is the base `LocoAgent` comparison path; the
selected manager route uses the distinct `LocoNWAgent` wrapper.
Constructor nil-failure boundaries are documented in the request-model slice.

## Synthetic contract

The fixture records receipt reads, the connect-time construction/setter
sequence, and factory selection as separate operations. It checks current, new,
callback, and concrete-class identities from inputs; it does not select behavior
from case names or property absence.

## Provenance

- `sendCarriagePushReceipt:` carriage-agent read: `0x101515000`.
- Manager allocation and constructor: `0x101518340` and `0x1015183f8`.
- The allocation class reference is `0x102114260` → class `0x102182700` →
  read-only metadata `0x1021826a0` (`LocoNWAgent`).
- Manager setup after construction: disable fallback `0x101518430`, setter
  `0x10151843c`, status handler `0x101518508`, manager status `0x10151851c`,
  and connect `0x101518534`. The private connect-lifecycle report records the
  sequence.
- Later manager reads: `0x101518458`, `0x1015184f4`, and `0x101518524`.
- Class hierarchy: private Objective-C table extraction for `LocoAgent`,
  `LocoTrailerAgent`, and `LocoNWAgent`.
- Receipt model hierarchy and fields: private Objective-C metadata for
  `LocoModel` → `LocoPushReceipt` → `LocoHintPushReceipt` /
  `LocoBlockSyncPushReceipt`; base fields are `method` (`NSString`) and
  `packetId` (`uint32`), with signed `int32` BLOCKSYNC revisions.
- Packet path: `sendPacket:tag:` at `0x101773670` calls `packetData` at
  `0x1017737fc`, `encryptPacketData:` at `0x101773814`, and socket
  `writeData:withTimeout:tag:` at `0x101773868`.
- Request constructor receipts: private Ghidra
  `parity/receipt-request-model/`; no proprietary artifacts are tracked.

# Push-receipt carriage-agent selection

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-05. Client build: macOS KakaoTalk 26.8.0.

`sendCarriagePushReceipt:` obtains the current `carriageAgent` at callsite
`0x101515000` and invokes inherited receipt behavior through that object. The
same manager stores a carriage agent through `setCarriageAgent:` in
`connectToCarriageServer:completion:` (`0x10151843c`), and later reads the
property from connect, logout, request, and login-list paths. The reviewed
selection slice proves the property handoff and dynamic dispatch boundary; it
does not prove which concrete transport constructor populated the property for
every runtime route.

The class hierarchy metadata distinguishes `LocoAgent` (base `NSObject`),
`LocoTrailerAgent` (subclass of `LocoAgent`), and `LocoNWAgent` (subclass of
`LocoAgent`). `sendPushReceipt:` is inherited, while `sendPacket:tag:` has the
base LocoAgent implementation and a distinct NW transport wrapper. The
carriage-agent constructor/transport-selection branch remains bounded to the
property setter and its manager callers until the connection factory is traced;
this document therefore does not activate a default transport.

The request constructors have established header method/packet ID copying for
HINT and signed-32 revision/plus-revision storage for BLOCKSYNC. BSON/body
serialization, default field values, and constructor failure outputs remain
explicit gaps in this slice; no packet builder is inferred from selector names.

## Synthetic contract

The fixture records property handoff, dynamic receipt dispatch, and concrete
transport selection as separate inputs. It accepts only observed handoff facts
and preserves unresolved factory selection as an explicit gap.

## Provenance

- `sendCarriagePushReceipt:` carriage-agent read: `0x101515000`.
- `connectToCarriageServer:completion:` setter: `0x10151843c`; manager reads at
  `0x101518458`, `0x1015184f4`, and `0x101518524`.
- Class hierarchy: private Objective-C table extraction for `LocoAgent`,
  `LocoTrailerAgent`, and `LocoNWAgent`.
- Request constructor receipts: private Ghidra
  `parity/receipt-request-model/`; no proprietary artifacts are tracked.

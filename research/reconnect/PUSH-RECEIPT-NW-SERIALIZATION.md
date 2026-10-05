# Selected LocoNWAgent receipt serialization

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-05. Client build: macOS KakaoTalk 26.8.0.

The manager carriage route allocates `LocoNWAgent` and stores it as the
carriage agent. Its `sendPacket:tag:` dispatch reaches the Swift-facing
`LocoNWAgent` wrapper (`0x100d48dfc` → `0x100d49560`), rather than the base
`LocoAgent` implementation at `0x101773670`.

The wrapper obtains Objective-C `packetData` (`0x100d49778`), bridges the
result to Foundation `Data`, calls inherited `encryptPacketData:`
(`0x100d497b8`), bridges the encrypted data back, and calls
`NWConnection.send(content:contentContext:isComplete:completion:)`
(`0x100d498a0`). The content context is `NWConnection.ContentContext.defaultMessage`
and `isComplete` is true. After scheduling the send it calls
`toggleOutSegmentTimeout:false` (`0x100d498dc`). The source proves this
serialization and transport sequence, but does not prove the bytes produced by
`packetData`, the encryption output, or any server response.

The request object field inventory remains separate from wire serialization.
`LocoPushReceipt` inherits from `LocoModel`/`SGJsonObject` and declares
`method` (`NSString`) and `packetId` (`uint32`). `LocoHintPushReceipt` adds no
fields. `LocoBlockSyncPushReceipt` adds signed `int32 revision` and
`plusRevision`. These fields describe the source object available to
`packetData`; the serialized key order, field-presence policy and defaults, and BSON bytes
remain unresolved until the `packetData` implementation is independently
localized.

The NW send completion is a Swift `NWConnection.SendCompletion` closure. The
reviewed body constructs a weak-owner capture and passes it to the send call;
error delivery is represented by the `NWError` completion input. The available
receipt does not establish that this completion updates the LocoAgent pending
map or correlates a packet tag. The pending-map and socket-disconnect consumers
belong to the base Objective-C path and are kept as a separate explicit gap.

## Synthetic contract

The fixture checks the observed NW scheduling sequence, the object-field
inventory, and completion input shape independently. It records wire-body
serialization and pending-correlation as unresolved fields rather than
inventing BSON keys, defaults, ACK behavior, or retry behavior.

## Provenance

- Manager class reference: private
  `reconnect-start-lifecycle/parent-constructor-target.txt`.
- NW send wrapper and completion: private
  `socket-callbacks/report.txt` and `socket-callbacks/decompile.txt`.
- Base comparison path: private `rc-q5-sendpacket-method/report.txt` and
  `rc-q5-sendpacket-method/decompile.txt`.
- Object hierarchy and fields: private
  `reconnect-conf-model/otool-objc.txt` and `receipt-request-model/report.txt`.
- Pending/disconnect boundary: private `reconnect-pending-consumers/trace.txt`.

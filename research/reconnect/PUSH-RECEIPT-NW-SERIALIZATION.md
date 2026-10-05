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
and `isComplete` is true. The raw branch checks the `packetData` result before
continuing; a nil result returns without sending or toggling. A nonnil encrypted
result is bridged into the send content; a nil encryption result takes a
separate nil-content path. The connection presence gates the NW send. After
that guarded send section, the wrapper calls `toggleOutSegmentTimeout:true`
(`0x100d498dc`, `w2 = 1`). The `packetData` implementation
is `0x10175a1c0`: it reads `body`, calls `BSONData`, obtains the first BSON
length, sets the header body length, reads header `data`, and appends the
header bytes to mutable output. A zero first length skips the body append. A
nonzero length re-reads `body`, calls `BSONData` a second time, and appends
that second result. The source therefore does not justify assuming the first
and second conversion results are interchangeable. It proves this framing
and guard order, but does not prove BSON key order, field-presence/default
policy, encryption output, or any server response.

The request object field inventory remains separate from wire serialization.
`LocoPushReceipt` inherits from `LocoModel`/`SGJsonObject` and declares
`method` (`NSString`) and `packetId` (`uint32`). `LocoHintPushReceipt` adds no
fields. `LocoBlockSyncPushReceipt` adds signed `int32 revision` and
`plusRevision`. These fields describe the source object available to
`packetData`; the serialized key order and field-presence/default policy remain
unresolved even though the framing implementation is now localized.

The NW send completion is a Swift `NWConnection.SendCompletion` closure. The
reviewed body constructs a weak-owner capture and passes it to the send call;
error delivery is represented by the `NWError` completion input. The available
receipt does not establish that this completion updates the LocoAgent pending
map or correlates a packet tag. The pending-map and socket-disconnect consumers
belong to the base Objective-C path and are kept as a separate explicit gap.

## Synthetic contract

The fixture separates the packet-data framing operation from the NW wrapper.
Framing cases derive the header body length, mutable-data capacity
(`bodyLength + 22`), and output bytes from synthetic first/second BSON
results; they cover zero-length omission and a deliberately different second
conversion. NW cases then model only the wrapper's packet-data result guard,
encryption nil path, connection gate, send scheduling, and timeout argument.
It records BSON key order/defaults, completion closure behavior, and pending
correlation as explicit gaps rather than inventing wire fields, ACK behavior,
or retry behavior.

## Provenance

- Manager class reference: private
  `reconnect-start-lifecycle/parent-constructor-target.txt`.
- NW send wrapper and completion: private
  `socket-callbacks/report.txt` and `socket-callbacks/decompile.txt`.
- `packetData` framing implementation: private Ghidra decompile of
  `0x10175a1c0` and parity trace for `packetData`.
- Swift guard/timeout branch: private `nw-disasm.txt` receipt for
  `0x100d49560` through `0x100d498dc`.
- Base comparison path: private `rc-q5-sendpacket-method/report.txt` and
  `rc-q5-sendpacket-method/decompile.txt`.
- Object hierarchy and fields: private
  `reconnect-conf-model/otool-objc.txt` and `receipt-request-model/report.txt`.
- Pending/disconnect boundary: private `reconnect-pending-consumers/trace.txt`.

# Push-receipt send admission

Status: reviewed static source contract, runtime unexecuted. Observation date:
2026-10-02. Client build: macOS KakaoTalk 26.8.0.

`sendPushReceipt:` first enqueues a block on the LocoAgent owner queue. The
queued block reads the owner's execution-time status byte. Only status `3`
continues to packet access: it reads the packet header and packet ID, derives
the push-receipt request tag as the signed negation of the unsigned packet ID
through `tagForPushReceiptPacketId:`, and invokes `sendPacket:tag:`. Packet ID
`17` therefore yields tag `-17`, while packet ID `4294967295` yields
`-4294967295`.
Any other execution-time status exits without
packet send. The status is read when the queued block runs, so admission and
execution can observe different status values.

This bounded contract does not claim packet serialization, socket write
completion, timeout policy inside `sendPacket:tag:`, weak-owner queue lifetime,
or terminal KICKOUT/CHANGESVR handling. Those are separate source chains.

## Static provenance

- `sendPushReceipt:` metadata and IMP: `0x1017734f4`, type `v24@0:8@16`.
- Its queued block is `0x1017751d8`; it checks the owner status byte for `3`,
  then obtains `packet`, `header`, and `packetId` before deriving the tag and
  calling `sendPacket:tag:`.
- `tagForPushReceiptPacketId:` metadata and IMP: `0x101773658`, type
  `q20@0:8I16`; its body returns the signed negation of the uint32 argument.
- The fixture is input-derived and validates queue admission, execution-time
  status, and signed-negated packet-tag identity. Runtime queue races remain unexecuted.

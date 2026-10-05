# Push-receipt body composition

Status: reviewed static source chain, runtime unexecuted. This contract covers
object-to-dictionary composition before the `LocoPacket`/`packetData` framing
contract.

The inherited `JSONObject` implementation at `0x101355b04` starts from the
superclass JSON object, makes a mutable dictionary, and applies the static
`LocoPushReceipt` property mapping. `LocoPushReceipt` contributes `method` and
unsigned `packetId`; `LocoHintPushReceipt` declares no additional properties.
Its HINT mapping removes those two base fields and has no replacement fields,
yielding an empty dictionary. The BSON encoding of that empty dictionary is
the exact five-byte document `05 00 00 00 00`.

`LocoBlockSyncPushReceipt` contributes signed int32 `revision` and
`plusRevision`. Its static mapping phase maps those source properties to the
wire keys `r` and `pr`, preserving signed int32 values including zero and
negative values. These property facts and the no-own-property HINT fact are
version-scoped to the inspected macOS client build. The source receipts establish the keys and widths; BSON
mapping key order remains an encoder concern and is not asserted here.

The receipt-to-packet chain is two-stage. The receipt-side method at
`0x101355cac` reads `packetId` and `method`, obtains the receipt's
`JSONObject`, and calls `initWithPacketId:method:body:`. That initializer is
`LocoPacket` IMP `0x10175a0f8` with Objective-C types
`@36@0:8I16@20@28`; its `packetId` input is uint32 and its body is the
JSONObject. The packet's `body` getter is IMP `0x10175a3ac`, and `packetData`
then obtains that body and calls `BSONData` before framing. Thus the receipt
projection and packet framing are separate objects and separate guards.

The composition test constructs the inherited header property dictionary from
synthetic method/packet inputs, applies static removal, maps BLOCKSYNC fields,
and encodes the resulting typed dictionary as deterministic synthetic BSON.
It checks that HINT header properties do not leak into the empty body, that a
no-removal negative control differs, and that BLOCKSYNC emits only `r`/`pr`
with input-derived signed values. Sorted key order and the test-only uint32
leakage encoding are synthetic assertions; they do not claim the official
NSNumber/BSON support matrix or wire key ordering. The separate packetData
contract proves header/body framing and the second BSON conversion guard.

## Provenance

- `sg-jsonobject-methods/decompile.txt`: inherited `JSONObject`
  implementation `0x101355b04`.
- `nw-name-map/decompile.txt` and `nw-name-map/report.txt`: static mapping
  phase and BLOCKSYNC mapping IMP `0x1016bb150`.
- `reconnect-conf-model/otool-objc.txt`: class hierarchy, properties, and
  signed/unsigned field metadata.
- `nw-body-methods.txt`: selector/class metadata for `body` and
  `initWithPacketId:method:body:`; the exact LocoPacket getter and initializer
  traces are in the private wire-chain receipts.
- `nw-receipt-wire-chain-2026-10-05/receipt-init.txt` and `body-getter.txt`:
  the two-stage receipt JSONObject → LocoPacket construction and body getter.
- `nw-receipt-wire-chain-2026-10-05/packetdata-parity.txt`: `body` → BSONData
  → header/body framing selector order.

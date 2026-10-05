# Push-receipt body composition

Status: reviewed static source chain, runtime unexecuted. This contract covers
object-to-dictionary composition before the existing `packetData` framing
contract.

The inherited `JSONObject` implementation at `0x101355b04` starts from the
superclass JSON object, makes a mutable dictionary, enumerates the receiver's
class properties, and removes/replaces the static base fields through the
observed mapping phase. `LocoPushReceipt` contributes `method` and unsigned
`packetId`; `LocoHintPushReceipt` declares no additional properties. Its HINT
mapping removes the base fields and has no replacement fields, yielding an
empty dictionary. The BSON encoding of that empty dictionary is the exact
five-byte document `05 00 00 00 00`.

`LocoBlockSyncPushReceipt` contributes signed int32 `revision` and
`plusRevision`. Its static mapping phase maps those source properties to the
wire keys `r` and `pr`, preserving signed int32 values including zero and
negative values. The source receipts establish the keys and widths; BSON
mapping key order remains an encoder concern and is not asserted here.

The composition test uses synthetic header method/packet IDs and signed
revision boundaries. It checks that HINT header properties do not leak into the
empty body and that BLOCKSYNC emits only `r`/`pr` with input-derived values.
The separate packetData contract proves header/body framing and the second
BSON conversion guard.

## Provenance

- `sg-jsonobject-methods/decompile.txt`: inherited `JSONObject`
  implementation `0x101355b04`.
- `nw-name-map/decompile.txt` and `nw-name-map/report.txt`: static mapping
  phase and BLOCKSYNC mapping IMP `0x1016bb150`.
- `reconnect-conf-model/otool-objc.txt`: class hierarchy, properties, and
  signed/unsigned field metadata.
- `nw-receipt-wire-chain-2026-10-05/packetdata-parity.txt`: `body` → BSONData
  → header/body framing selector order.

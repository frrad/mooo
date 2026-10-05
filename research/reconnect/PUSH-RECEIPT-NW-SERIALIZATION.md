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
`LocoPushReceipt` inherits from `LocoModel` (which inherits from
`SGJsonObject`) and declares `method`
(`NSString`) and `packetId` (`uint32`). `LocoHintPushReceipt` adds no fields.
`LocoBlockSyncPushReceipt` adds signed `int32 revision` and `plusRevision`.
The bundled first-party `SGJsonKit` implementation enumerates the dynamic
class's properties through (but excluding) `SGJsonObject`, reads each value by
KVC, and puts it into a mutable dictionary. A nil or `NSNull` value is emitted
as `NSNull`; non-null values are retained unless they conform to `SGJson` (then
`JSONObject` is used) or `SGNumberArray` (then `numberArray` is used). Integer
zero is therefore retained as a value rather than treated as absent.

The inherited `JSONObject` implementation at `0x101355b04` begins from the
superclass JSON object and makes a mutable dictionary only when that object is
an `NSDictionary`; the non-dictionary branch returns the superclass result
unchanged. Its raw class reference at `0x102113298` resolves to the static
`LocoPushReceipt` class. The mutable path enumerates that class's declared
properties and calls `removeObjectForKey:` (`0x101901660`) for each property
name. It then enumerates `nameMappingDictionary` and applies the mapping block
through keyed lookup and keyed assignment. The mapping block receives the
mapping key as its first object argument and the mapping value as its second:
it looks up the second (source) key, skips absent sources, skips assignment for
`NSNull`, and removes a present source key in either case; otherwise it writes
the value under the first (destination) key and removes the source key. It does
not write `nil` for an absent source.

`LocoBlockSyncPushReceipt` overrides `nameMappingDictionary` at
`0x1016bb150` with a static two-entry dictionary. The private constant-data
receipt decodes its destination keys as `pr` and `r`, with source keys
`plusRevision` and `revision`, respectively. Therefore the observed mapping
phase renames those two fields to the short keys and removes their source
spellings. This proves static base-class removal plus subclass mapping behavior,
including its missing and `NSNull` guards. The framework serializer establishes
the property contribution and nil/scalar conversion boundary; final BSON key
order, HINT defaults, and packet-level omission policy remain unresolved.

`LocoHintPushReceipt` declares no own methods or properties and inherits
`LocoPushReceipt` directly. Consequently its local class contributes no
mapping dictionary. A separate app `LocoModel` implementation of
`nameMappingDictionary` at `0x10167bea0` returns nil, and its neighboring
`0x10167bea8` implementation also returns nil; the `0x10167beb0`
`initWithJSONObject:` path calls that selector while constructing a model.
The class metadata resolves `LocoPushReceipt`'s superclass pointer
`0x1021ae3c8` to `LocoModel`, whose superclass is
`_OBJC_CLASS_$_SGJsonObject`. Thus HINT reaches the nil `LocoModel` mapping
fallback through `LocoPushReceipt` → `LocoModel` → `SGJsonObject`;
`SGJsonKit` itself has no `nameMappingDictionary` method. No HINT body key or
default value is established. The static base removal is bounded to the
`LocoPushReceipt` property list (`method` and `packetId`).

The NW send completion is a Swift `NWConnection.SendCompletion` closure at
`0x100d4a840`. Its body weak-loads the owner and returns when that owner has
gone away. With an owner, it invokes `toggleOutSegmentTimeout:false` and then
branches on the `NWError` completion value. The success branch performs
cleanup only. Error branches inspect the POSIX error representation; only the
POSIX code `0x59` (decimal 89) takes the cleanup path, while other POSIX
codes and non-POSIX errors log and cancel the `NWConnection` read from the
retained weak-loaded owner at completion time when present. This is a
completion-time current-connection lookup, not a guarantee that the object is
the same connection instance used when the send was scheduled.
The reviewed closure body contains no pending-map lookup, request-tag
correlation, status write, or completion callback invocation. Pending-map and
socket-disconnect consumers from the base Objective-C path remain separate
and are not attributed to this NW closure.

## Synthetic contract

The fixture separates the packet-data framing operation from the NW wrapper.
Framing cases derive the header body length, mutable-data capacity
(`bodyLength + 22`), and output bytes from synthetic first/second BSON
results; they cover zero-length omission and a deliberately different second
conversion. NW cases then model only the wrapper's packet-data result guard,
encryption nil path, connection gate, send scheduling, and timeout argument.
The JSON-object projection fixture exercises the framework's observed
property-contribution and conversion branches, while the app-side
mapping-dispatch default and final BSON policy remain explicit gaps. The observed mapping phase is
bounded separately: dictionary versus non-dictionary input, absent source,
`NSNull` source, and ordinary source-to-destination rename are distinct cases
and must not be collapsed into a generic field-copy operation.
It records BSON key order/default policy, encryption output, and any server
response as explicit gaps. Completion vectors cover owner lifetime, success,
the observed POSIX `0x59`/89 cleanup predicate, neighboring POSIX and
non-POSIX values, replacement-connection cancellation identity, and the
other-error connection-cancel branch in
`rc-q5-push-receipt-nw-completion.json`; they do not invent ACK or retry
behavior.

The mapping cases are in
`rc-q5-push-receipt-json-mapping.json`. They derive the non-dictionary return,
absent-source no-op, `NSNull` removal, ordinary rename, and identity
assign-then-remove behavior from the observed guard order. The two BLOCKSYNC
cases use the recovered `pr`/`plusRevision` and `r`/`revision` pairs with zero
and nonzero synthetic values and model static `method`/`packetId` removal
before mapping. The fixture exercises each pair independently; it makes no
claim about NSDictionary enumeration order.

The framework conversion cases are in `rc-q5-sgjson-object.json`. They cover
nil/`NSNull`, scalar zero, nested `SGJson` recursion, and ordered number-array
projection as synthetic values; they do not claim app wire encoding.

## Provenance

- Manager class reference: private
  `reconnect-start-lifecycle/parent-constructor-target.txt`.
- NW send wrapper and completion: private
  `socket-callbacks/report.txt`, `socket-callbacks/decompile.txt`, and the
  exact-address `nw-send-completion/report.txt` plus `decompile.txt` receipt
  for `0x100d4a840`.
- `packetData` framing implementation: private Ghidra decompile of
  `0x10175a1c0` and parity trace for `packetData`.
- Swift guard/timeout branch: private `nw-disasm.txt` receipt for
  `0x100d49560` through `0x100d498dc`.
- Base comparison path: private `rc-q5-sendpacket-method/report.txt` and
  `rc-q5-sendpacket-method/decompile.txt`.
- Object hierarchy and fields: private
  `reconnect-conf-model/otool-objc.txt` and `receipt-request-model/report.txt`.
- Pending/disconnect boundary: private `reconnect-pending-consumers/trace.txt`.
- JSON projection and mapping guards: private `nw-push-receipt-methods/decompile.txt`,
  `nw-push-receipt-block/decompile.txt`, and the constant-data extraction under
  `nw-name-map/` (including the private `0x101f71678` memory receipt).
- First-party serializer implementation: private `sgjsonkit-source/decompile-object.txt`,
  `sgjsonkit-source/decompile-category.txt`, and `sgjsonkit-source/otool-objc.txt`
  from the bundled SGJsonKit arm64 slice.
- App model mapping fallback: private `nw-loco-model/decompile.txt` and
  `nw-loco-model/report.txt` for `0x10167bea0`, `0x10167bea8`, and
  `0x10167beb0`.

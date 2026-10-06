# LocoPacket data-decoder constructor contract

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

`LocoPacket` declares a retained `LocoPacketHeader` at ivar offset 8 and a
retained `NSDictionary` body at ivar offset 16. Its `initWithPacketData:`
implementation is `0x101759fb4`. A successful superclass initialization first
creates the header from the first 22 bytes and stores it, including when the
header initializer returns nil. The constructor then reads `header.bodyLength`.

When that length is zero, body decoding is skipped and the body remains its
default nil value. When it is nonzero, the constructor creates a data slice at
offset 22 whose length is **the complete `NSUInteger` input length minus 22**.
The declared header body length gates this branch but is not used as the slice
length. The slice is sent to `+[NSDictionary dictionaryWithBSONData:]`, and
the returned object is passed directly to `setBody:` without a result or error
branch. A nil decoder result therefore follows the same setter path as a
decoded object.

The bundled `dictionaryWithBSONData:` implementation is IMP `0x1017eb434`.
It creates a mutable dictionary, obtains the NSData byte pointer, advances
four bytes past the BSON document-length prefix, and walks elements with a
cursor helper. The helper never receives the NSData length and does not read
the four-byte declared document length; the synthetic vectors therefore
re-run with that prefix replaced by `1` and retain the same bounded result.
A zero BSON element type terminates the loop, so the valid empty-document
vector (`05 00 00 00 00`) returns an empty dictionary. The source-observed
cursor advances 4 payload bytes for signed int32 (`0x10`), 8 for signed int64
(`0x12`), and for strings (`0x02`) advances over the NUL-terminated key, the
four-byte string length, the string bytes, and its trailing NUL. The fixture
pins those per-element consumed widths, not only predecoded labels. Decoded
signed int32/int64 and other scalar values are inserted as dictionary
entries. An unknown element type makes the cursor helper return zero; the
outer loop then returns the dictionary accumulated so far. Repeated keys with nonnil decoded values are
assigned through `setObject:forKey:`, so a later nonnil value replaces the earlier
value. Bytes after a valid zero-type terminator are not inspected by this loop.

## Cursor-recognized null and undefined values

The cursor and value decoder have different supported-type boundaries.
The cursor recognizes BSON null (`0x0a`) and undefined (`0x06`), advances
past the type byte and NUL-terminated key with zero payload bytes, and
continues to the next element. The value helper (`0x1017eb504`) returns
nil for both types. The dictionary loop's nil-result branch at
`0x1017eb4bc` skips insertion and continues; it does not insert `NSNull`,
remove the key, or terminate parsing.

Consequently a null-only dictionary is empty, a field after null is still
decoded, and a null duplicate retains any earlier nonnil value. The synthetic
vectors distinguish `r=12; r=null; pr=3` (both integer values retained)
from `r=null; r=12` (the later integer inserted). Undefined follows the same
bounded skip-and-continue behavior. A cursor-unsupported type such as the
fixture's `0x7f` remains a separate stop-with-partial-dictionary path.

This is a BSON decoder boundary, distinct from the incoming model's
`NSNull` mapping and KVC guards. A generic decoder that inserts null and
overwrites duplicates would erase the retained `r` value before notice
projection. Full incoming eligibility must preserve the observed decoder
behavior rather than using the model's null guard to infer BSON behavior.
These vectors do not establish every other cursor-recognized type, nested
array behavior, malformed-pointer behavior, or downstream receipt acceptance.

The cursor's jump table at `0x101a4f38c` maps both `0x06` and `0x0a` to
`0x10164bcc0` with payload width zero. The shared cursor advancement at
`0x10164bcc0` consumes the type byte, key bytes, key terminator, and payload;
the next type is returned at `0x10164bcd8`. The value-helper dispatch and
outer nil-result branch were independently traced in the authorized
26.8.0 ARM64 client. This observation is static source evidence; live decoder
execution remains untested.

The decoder has no visible NSError result. Its cursor uses raw pointer reads,
`strlen`, and element lengths without an NSData-length parameter. Exact
truncated or malformed-input behavior is therefore an explicit source safety
gap: it may read beyond the supplied slice or fail at the parser/runtime
boundary. The synthetic fixture records only the bounded valid, terminator,
and unknown-type behaviors above and does not claim safe malformed-input
handling.

## Provenance

- `LocoPacket` metadata: private static metadata receipt, class declaration and
  ivars/properties.
- Constructor IMP `0x101759fb4`: private ARM64 disassembly receipt.
- BSON dispatch stub `0x1018c6ae0` resolves to
  `dictionaryWithBSONData:`; its receiver is `_OBJC_CLASS_$_NSDictionary`.
- Decoder IMP `0x1017eb434` and cursor helper `0x10164bb54`: private static
  disassembly receipt.
- The unknown-type diagnostic string is the private static literal
  `unknown type: ...`; it is recorded only as behavior, not copied into the
  implementation.
- Body setter dispatch `0x10190cf60` resolves to `setBody:`.
- Synthetic cases, including raw BSON hex, uint64 input-length arithmetic,
  duplicate-key replacement, and expected cursor widths, are in
  `research/fixtures/reconnect/rc-q5-packet-data-decoder.json`.

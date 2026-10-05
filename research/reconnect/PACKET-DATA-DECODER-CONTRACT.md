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
outer loop then returns the dictionary accumulated so far. Repeated keys are
assigned through `setObject:forKey:`, so a later value replaces the earlier
value. Bytes after a valid zero-type terminator are not inspected by this loop.

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
  `internal/protocol/sessionlogin/testdata/reconnect/rc-q5-packet-data-decoder.json`.

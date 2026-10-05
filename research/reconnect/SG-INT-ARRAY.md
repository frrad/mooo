# SG integer-array constructor boundary

Status: reviewed static SGJsonKit framework contract with bounded synthetic
vectors. Client build: macOS KakaoTalk 26.8.0 arm64.

`SGInt32Array` inherits `SGIntArray`; `SGInt64Array` inherits `SGLongArray`.
The framework-relative metadata and implementations expose the following
storage contract:

- `initWithValues:count:` calls the superclass initializer, copies `count * 4`
  bytes for `SGIntArray` or `count * 8` bytes for `SGLongArray` into `NSData`,
  stores the data bytes pointer, and stores the element count.
- `initWithNumberArray:` first creates mutable data, enumerates the supplied
  `NSArray`, calls `intValue` or `longValue` for each element, and appends the
  corresponding four- or eight-byte host integer. It then stores the data bytes
  pointer and source array count.
- `numberAtIndex:` directly loads the four- or eight-byte element and wraps it
  with `numberWithInt:` or `numberWithLong:`. The load has no bounds check, so
  out-of-range access is an explicit safety gap.
- `numberArray` allocates an NSArray with the stored count and wraps each raw
  element using the same width-specific NSNumber factory.

The fixture covers signed boundaries, empty arrays, the width-specific
`intValue`/`longValue` conversion, and the observed nil source-array empty
result. It uses little-endian arm64 synthetic bytes and does not activate
production array decoding. Superclass-init failure, nil elements inside a
nonnull source array, and unchecked out-of-range reads remain gaps.

The element fixture also records account-free Foundation captures for bool,
signed integer, finite and nonfinite double, and selected string values. The
four-byte path uses Foundation `intValue`; the eight-byte path uses
`longValue` (64-bit `long` on arm64). `NSNull`, arrays, and dictionaries raise
`NSInvalidArgumentException` for both accessors. The nil-element cases model
the source effect order: values appended before the exception remain the
already-built prefix, while the initializer itself does not return a completed
array. String cases are exact captured inputs only and do not define a string
grammar.

## Provenance

Framework-relative symbols from the authorized SGJsonKit binary:

- `SGIntArray initWithValues:count:` `0x1d04`; `initWithNumberArray:` `0x1dcc`;
  `numberAtIndex:` `0x1f84`; `numberArray` `0x1fa4`; `count` `0x20bc`; `data`
  `0x20cc`.
- `SGLongArray initWithValues:count:` `0x3cf0`; `initWithNumberArray:`
  `0x3db8`; `numberAtIndex:` `0x3f70`; `numberArray` `0x3f90`; `count`
  `0x40a8`; `data` `0x40b8`.
- `SGInt32Array` and `SGInt64Array` superclass metadata is recorded in the
  framework Objective-C metadata dump.

The executable bounded contract is in
`internal/protocol/sessionlogin/sg_int_array_contract_test.go` with vectors in
`internal/protocol/sessionlogin/testdata/reconnect/rc-q5-sg-int-array.json`.
The sanitized Foundation element probe is retained outside the repository at
`/private/tmp/mooo-sg-array-element-coercion-20261005.txt`.

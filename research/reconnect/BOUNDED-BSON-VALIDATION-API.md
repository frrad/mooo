# Bounded BSON validation API

Status: opt-in implementation preparation. This package is not wired into
`Packet`, `Session`, or the default incoming push path.

The official `dictionaryWithBSONData:` cursor starts after the four-byte BSON
document prefix and does not use the declared document length. The source
contract therefore preserves that behavior for valid input while adding Go
resource bounds around the supplied slice and recursive container depth.

The proposed API is:

```go
type BSONDecodeOptions struct {
    MaxBytes int
    MaxDepth int
}

type BSONDecodeResult struct {
    Document   map[string]any
    Partial    bool
    UnknownType byte
}

func DecodeObservedBSON([]byte, BSONDecodeOptions) (BSONDecodeResult, error)
```

The result is intentionally separate from an error. An unrecognized element
type returns the dictionary accumulated before that element with `Partial` and
`UnknownType` set, matching the observed outer-loop behavior. Bounds and
truncated known elements return errors because the Go adapter must not repeat
the source cursor's unbounded reads. The implementation is a future typed
notice-validation dependency; it does not replace the existing packet decoder
or silently make malformed notices eligible.

The bounded supported matrix is:

| Element | Result | Source-compatible boundary |
| --- | --- | --- |
| `0x01` | `float64` | IEEE little-endian payload |
| `0x02` | `string` | declared byte length and trailing NUL are checked |
| `0x03` | `map[string]any` | recursively decoded with `MaxDepth` |
| `0x04` | `[]any` | numeric-key order is preserved |
| `0x06`, `0x0a` | skipped element | no assignment; an earlier duplicate remains |
| `0x08` | `bool` | any nonzero byte is true |
| `0x10` | `int32` | four-byte little-endian payload |
| `0x12` | `int64` | eight-byte little-endian payload |

Unknown element types stop with the partial map. Duplicate ordinary values
replace earlier values; a later null/undefined element does not overwrite an
earlier value. Strings require a NUL within the supplied slice. The source
cursor's raw NUL scanning is represented with this bounded equivalent; the
declared BSON document length is still not used as the cursor limit.

The synthetic tests cover scalar values, nonzero boolean normalization,
duplicate/null preservation, partial unknown-type return, recursive document
containers, and explicit byte/depth bounds. They are source-contract tests,
not proof that every future typed notice accepts every BSON shape. Notice
constructor validation, SGJson property coercion, and Session binding remain
separate decisions.

Provenance is the private decoder receipt for IMP `0x1017eb434` and cursor
helper `0x10164bb54`, with the public bounded source contract in
`PACKET-DATA-DECODER-CONTRACT.md`.

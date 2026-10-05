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
| `0x02` | `string` | value helper scans to first NUL; cursor advances declared length + 4 |
| `0x03` | `map[string]any` | recursively decoded with `MaxDepth` |
| `0x04` | `[]any` | encounter order is preserved; numeric keys are not sorted |
| `0x05`, `0x06`, `0x07`, `0x09`, `0x0a`, `0x0b`, `0x0c`, `0x0d`, `0x0e`, `0x0f`, `0x11` | skipped element | cursor recognizes and consumes the source-width payload; no assignment |
| `0x08` | `bool` | any nonzero byte is true |
| `0x10` | `int32` | four-byte little-endian payload |
| `0x12` | `int64` | eight-byte little-endian payload |

Unknown element types stop the local dictionary/array loop and return its
partial value. Recursive cursors are bounded here by the supplied Go slice and
their own zero terminator, matching the source helper's lack of a nested end
pointer. The parent cursor then resumes at the nested element's declared
container width; the nested partial value is retained and the unknown type is
not propagated as a root partial result. A DBPointer (`0x0c`) is skipped by its
declared string length plus sixteen bytes; its value helper does not scan for a
NUL. Other recognized cursor-only widths are represented by the synthetic
vectors and remain separate from unknown-type handling.
Duplicate ordinary values replace earlier values; a later null/undefined or
other recognized-but-value-less element does not overwrite an earlier value.
The string value helper scans from its payload to the first NUL, while the
cursor advances by the declared string length plus four bytes. The source
cursor's raw reads are represented with bounded equivalents; the declared root
BSON document length is still not used as the cursor limit.

The synthetic tests cover scalar values, nonzero boolean normalization,
duplicate/null preservation, recognized cursor-only skip types, string
first-NUL/declared-width divergence, nested unknown continuation, array
encounter order, partial unknown-type return, recursive document containers,
and explicit byte/depth bounds. They are source-contract tests,
not proof that every future typed notice accepts every BSON shape. Notice
constructor validation, SGJson property coercion, and Session binding remain
separate decisions.

Provenance is the private decoder receipt for IMP `0x1017eb434` and cursor
helper `0x10164bb54`, with the public bounded source contract in
`PACKET-DATA-DECODER-CONTRACT.md`.

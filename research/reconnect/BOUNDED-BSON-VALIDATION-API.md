# Bounded BSON validation API

Status: used only as a shadow decoder. Production callers keep decoding with
mongo-driver; `internal/protocol/bsonshadow` decodes every incoming body with
both and reports differences (see "Shadow comparison" below). The observed
decoder never changes what callers receive.

The official `dictionaryWithBSONData:` cursor starts after the four-byte BSON
document prefix and does not use the declared document length. The source
contract therefore preserves that behavior for valid input while adding Go
resource bounds around the supplied slice and recursive container depth.

The proposed API is:

```go
type BSONDecodeOptions struct {
    MaxBytes int
    MaxDepth int
    MaxWork  int
}

type BSONDecodeResult struct {
    Document   map[string]any
    Partial    bool
    UnknownType byte
}

func DecodeObservedBSON([]byte, BSONDecodeOptions) (BSONDecodeResult, error)
```

`MaxWork` is a shared element-visit budget across recursive cursors. This is a
Go safety choice: the source nested cursor has no end pointer and the parent
resumes at the declared container width, so an input with overlapping declared
widths can revisit suffix bytes. The default is bounded from input size; callers
may lower it for stricter resource envelopes.

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
The clean-room adapter treats invalid UTF-8 in a value string as a skipped nil
value, preserving earlier duplicates and allowing later elements to be visited.
Invalid UTF-8 in a key is tolerated when that value is also skipped; for a
nonnil value it returns an explicit adapter error rather than attempting to
insert a nil key.

The synthetic tests cover scalar values, nonzero boolean normalization,
duplicate/null preservation, recognized cursor-only skip types, string
first-NUL/declared-width divergence, nested unknown continuation, array
encounter order, partial unknown-type return, recursive document containers,
and explicit byte/depth/work bounds, including invalid UTF-8 skip/error
branches. They are source-contract tests,
not proof that every future typed notice accepts every BSON shape. Notice
constructor validation, SGJson property coercion, and Session binding remain
separate decisions.

Provenance is the private decoder receipt for IMP `0x1017eb434` and cursor
helper `0x10164bb54`, with the public bounded source contract in
`PACKET-DATA-DECODER-CONTRACT.md`.

## Shadow comparison

`wireConn.readWithHeaderObserverAndProgress` is the single point every
incoming LOCO packet passes through (session read loop, the synchronous login
handshake and media connections). For each BSON body it runs
`bsonshadow.Compare`, which decodes the body with mongo-driver (as `bson.D`,
collapsed with struct-decoding semantics: a later duplicate key, including a
null, replaces an earlier one) and with `DecodeObservedBSON`, then reports
every field-level difference. Kinds: `mongo-rejects`, `official-rejects`,
`shadow-bounds`, `official-partial`, `official-dropped-invalid-utf8`,
`official-ignores-type`, `mongo-only`, `official-only`, `null-overwrites`,
`string-nul-truncated`, `type-differs`, `value-differs`, `array-length`.

The only case not reported is a mongo null or undefined for a key the official
result lacks: struct decoding gives that field its zero value, the same
observable state as an absent key. Key order inside a document is not
compared because the official result is an unordered dictionary; array order
is compared.

Reports are log-safe: paths, kinds and value descriptors (type and length),
never values. Keys that do not look like protocol field names are replaced by
a short hash. Raw bodies are written only to an explicitly configured private
dump directory (0700, files 0600), one JSON file per distinct body, with enough
data (method, header, body hex) to reproduce the comparison in isolation.
Turning a dump into a committed fixture requires manual sanitization.

Scope of the comparison: the observed decoder models only the macOS client's
LOCO dictionary decoder. That client also ships other structured-data readers
(a JSON layer, a separate general-purpose BSON library, and VoIP signaling
accessors), and which incoming methods, if any, route through them has not
been traced. `official-ignores-type` and similar kinds therefore mean "the
LOCO dictionary decoder would not surface this", not "the official client
never reads this type". If a method is later shown to use another reader,
its discrepancies must be reinterpreted or the shadow scoped per method.
(Source: frrad/kakao research note on LOGINLIST `rp` and BSON binary, macOS
26.8.0, static trace.)

Modes are `off`, `log` and `panic`. Test binaries default to `panic`, the lab
research CLI defaults to `panic`. The operator `mooo-lab chats list` command
and the bridge default to `log`
(`network.bson_shadow.mode`). These operator defaults retain diagnostics because any
remote sender able to trigger a discrepancy could otherwise crash it.
`MOOO_BSON_SHADOW` overrides the default mode. Bodies above 1 MiB are skipped
and counted.

Test harnesses that decode mooo's own outgoing requests bypass the shadow: the
first suite run flagged the LOGINLIST request's binary `rp` field, which the
official decoder recognizes but never assigns. That is mooo's request, not
server data, so it is out of scope for the comparison.

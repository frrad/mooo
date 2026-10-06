# Foundation bool and double scalar boundary

Status: platform-bounded synthetic contract. Observation date: 2026-10-04.
The Foundation values below were captured on macOS 26.6.2 (25G83), arm64,
using an `NSObject` synthetic `int32_t` (`Ti`) property. They are not a generic
Apple conversion policy and do not claim behavior for untraced outer projection
or malformed input handling.

The official scalar decoder helper is `0x1017eb504`. For the reachable normal
floating path, the outer type byte is `0x01`, the nested type byte is also
`0x01`, and the helper loads the raw eight-byte IEEE payload into `d0` at
`0x1017eb834`; it then sends `numberWithDouble:` through stub `0x1018f43e0`
(selector load `0x1018f43e4`). The reachable normal boolean path starts with
outer type `0x08`; after the key/string cursor at `0x1017eb59c`, it reads the
payload byte at `0x1017eb5ac`, compares it with zero at `0x1017eb910`, and
sends the nonzero result through `numberWithBool:` at `0x1018f43a0` (selector
load `0x1018f43a4`). Other nested numeric branches in the shared helper are
not claimed as normal bool/double input paths here. The signed integer
branches remain separately documented in `FOUNDATION-INT32-KVC.md`.

The private Foundation probe recorded these KVC results for the same boxed
factory domains: `NO` → `0`, `YES` → `1`; `-3.75` → `-3`, `3.75` → `3`;
`2147483647.0` → `2147483647`; `2147483648.0` → `-2147483648`; `1e40` →
`-1`; `-1e40` → `0`; positive infinity → `-1`; and NaN → `0`. Additional
captured vectors cover negative zero, values just below/above `2^32`, signed
`±2^63` and the nearest representable values below those limits, signed
subnormals, negative infinity, and signed NaN. The executable fixture models the
captured domain as truncation followed by signed-64 clamping and the `Ti`
low-word conversion; it does not claim that policy outside the listed domain. Bool objects
were `__NSCFBoolean` with ObjC type `c`; double objects were `__NSCFNumber`
with ObjC type `d`. These are captured platform outcomes, not a proposed
portable cast implementation.

The synthetic vectors are in
`research/fixtures/reconnect/rc-q5-foundation-bool-double.json`.
The test parses the captured bool/double input domains and uses explicit
out-of-range golden cases; it intentionally does not present a generic
floating-point conversion routine. Private provenance is
`/private/tmp/mooo-num-factory-probe.txt` and
`.lab/credential-storage/raw/cs1/disassembly.txt`; no account data or
proprietary binary is tracked.

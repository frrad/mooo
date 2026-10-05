# Push-receipt socket callbacks

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

The Objective-C method table identifies the callback ownership. `LocoAgent`
base methods contain `socket:didReadData:withTag:` at IMP `0x101774b3c`,
`socket:didWriteDataWithTag:` at `0x101774cb8`, and the partial-write/read
methods adjacent to them. The write callback performs the outbound timeout/state
update and forwards the tag to `didWrite:`; the `didWrite:` IMP `0x101773b14`
is an empty return. The read callback branches on tag zero: the zero path takes
the zero-tag receive update, while the nonzero path takes the tagged receive
update and then invokes `readHeader`.

A separate implementation at `0x1015f99e8` synchronizes a pending structure,
branches on socket callback state, and updates/removes a numeric pending entry.
The available metadata does not prove that this implementation is the same
class as the LocoAgent base method; it is recorded as a separate callback
implementation. The Swift-facing `sendPacket:tag:` wrapper at `0x100d48dfc`
delegates into `0x100d49560`, which handles `NWConnection.SendCompletion` and
is another dispatch boundary. These implementations must not be conflated.

The pending write body `0x1015f8fb0` synchronizes bookkeeping, computes the
pending count, branches when pending state is `2`, records associated values,
and updates the count. The disconnect path `0x100d44f40` is separately
observed to fan out failure handling across pending work. The reviewed receipts
do not identify a receipt-specific ACK parser, retry policy, or durable state
consumer. Those remain explicit gaps.

## Synthetic contract

The fixture covers write completion forwarding, the empty `didWrite:` endpoint,
read tag-zero and nonzero branches, pending callback states, and disconnect
failure fanout. It records observed effects only; it does not infer an ACK or
correlation result from a tag.

## Provenance

- LocoAgent callback table: private `rc-q5-write-callbacks/locoagent-class-methods.txt`.
- LocoAgent read/write callbacks: `0x101774b3c`, `0x101774cb8`; `didWrite:`
  `0x101773b14`.
- Separate pending callback implementation: `0x1015f99e8`.
- Pending write body: `0x1015f8fb0`; disconnect fanout: `0x100d44f40`.
- Swift wrapper: `0x100d48dfc` → `0x100d49560`.
- Private reports: `~/Library/Application Support/mooo-lab/ghidra/parity/rc-q5-write-callbacks/`, `reconnect-fail-pending/`, `reconnect-fail-pending-disconnect/`, and `socket-callbacks/`.

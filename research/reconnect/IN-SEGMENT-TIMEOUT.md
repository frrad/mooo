# In-segment read timeout contract

Status: reviewed static source contract, runtime unexecuted. Observation date:
2026-10-02. Client build: macOS KakaoTalk 26.8.0.

`toggleInSegmentTimeout:` has Objective-C type `v20@0:8B16` and implementation
`0x101773bdc`. It first reads the owner's configured in-segment timeout. When
that value is not strictly positive, it does not enqueue either branch. For a
positive value it queues one block on the main queue. The block tests the
captured enable byte exactly against `1`.

The enable branch rereads the timeout from the captured owner at block execution
and schedules `fireInSegmentTimeout` using the owner target and a nil object.
The disable branch does not reread the timeout; it cancels the exact owner,
`fireInSegmentTimeout`, nil-object tuple. The source forwards the execution-time
delay after the positive admission check, so a later zero or negative value is
represented as a scheduled operation in this contract. The block loads the owner
through a weak capture; owner lifetime and main-queue races remain gaps.

`fireInSegmentTimeout` has type `v16@0:8`, implementation `0x101773e10`, and
disconnects its owner. The callback's downstream error/fanout behavior is
covered by the separate socket-disconnect contract.

The read state machine uses this timeout as a socket read timeout: tag 1 disables
it before `didReadBody:`, while the read-length path obtains the configured value
and passes it to `readDataWithTimeout:tag:`. The exact external socket timeout
queue behavior and partial-read retry/error policy remain outside this bounded
contract.

## Static provenance

- `toggleInSegmentTimeout:` metadata and IMP: `0x101773bdc`; selector pointer
  `fireInSegmentTimeout` is used by the queued block.
- `fireInSegmentTimeout` metadata and IMP: `0x101773e10`; its reviewed body calls
  `disconnect` on the owner.
- The queue block is `0x101775088`; its enable/disable branch and exact selector
  tuple are visible there.
- LocoAgent read callbacks: partial-read disable at `0x1015f9654`, tag dispatch
  at `0x1015f9688`, corresponding callbacks at `0x101774a3c` and
  `0x101774b3c`; read-length timeout reads/calls are in the `0x1015f9fac` to
  `0x1015fa008` range.

The fixture is a strict characterization of the reviewed admission, reread,
queue, cancellation, and terminal-disconnect behavior. It does not claim a
runtime timer implementation or socket error policy.

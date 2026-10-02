# LocoAgent write callbacks

Status: reviewed static contract, runtime unexecuted.

The reviewed write delegate belongs to `LocoAgent`. A partial-write callback disables the out-segment timeout and returns. A complete-write callback performs the same disable first, then invokes the agent's empty `didWrite` hook. No higher-level completion, retry, error, or partial-progress forwarding was recovered from these LocoAgent callbacks. A similarly named trailer-agent callback is a separate class and is outside this contract.

The out-segment toggle has a separate guarded scheduler. The admission read obtains the configured timeout and does not enqueue when it is non-positive. The caller reads the configured timeout before enqueue and captures the enable byte in the queued block. Only an enable byte equal to `1` rereads the current timeout and schedules `fireOutSegmentTimeout` on the same owner with a nil object, using that reread value; every other byte cancels the exact owner/selector/nil-object tuple without rereading. The queued block loads its owner weakly, so owner lifetime at execution remains an implementation gap. Queue and callback execution races are also gaps. The setter's source of configuration overrides is also outside this contract.

The callback and timeout effects are intentionally separate: a write callback's disable is not evidence that the socket write succeeded, and enabling the out-segment timeout is not evidence that a completion callback will later report success or failure.

Evidence: RC-BIN-025 in `EVIDENCE.md`. The source is a version-specific binary-analysis receipt held in the private lab; no proprietary disassembly is included here.

# Login cursor advancement boundary

Status: static source supplement, 2026-10-02. This is a bounded contract for
cursor policy; it does not define profile replacement or durable checkpoint
transaction semantics.

The shared chat context owns two login cursors: a signed 64-bit `lastTokenId`
and a signed 32-bit blind-token cursor. Each update selects/dispatches its downstream update work only when the incoming
value is strictly greater under the signed width of that cursor. Equal and lower
values select no update work.
The two comparisons are independent: token update work can be selected while a
blind-token value is equal or lower, and vice versa. Downstream helper
synchronization, storage writes, queueing, and failure behavior remain gaps.

The login response carries these fields as `lastTokenId` (int64) and `lbk`
(int32). Parsing and response routing are separate from the shared-context
advancement policy. A manager-side helper checks `lastTokenIdLocked` before calling the shared
context: the clear state permits delegation and the set state blocks it. The
complete reset/identity boundary for that lock remains untraced. Implementations
must therefore expose that boundary as an explicit policy input instead of
treating every login response as a global replacement.

This contract intentionally does not decide how a durable checkpoint handles a
new profile, logout/reset, or a server cursor that is lower than the stored
cursor. Those are implementation decisions requiring a profile-generation or
reset policy. A pure reducer can model the reviewed shared-context rule first,
then let its caller select `advance`, `ignore`, or `reset-domain` explicitly;
that caller policy is a queued design boundary, not an observed durable write.

## Synthetic vectors

The vectors in `internal/protocol/sessionlogin/testdata/token-cursor-unresolved.json`
cover strict signed comparisons across negative and nonnegative pure-domain
values, the observed negative-current/zero-incoming assertion branch, independent
token widths, and the unresolved durable-policy branch. Wire
validation is separate and remains outside this reducer contract. A Go client
should fail closed on the observed assertion input rather than reproduce a
process abort; that is an implementation decision.
The pure-selection cases are executable against the independent reducer and
the profile-policy case remains explicitly unresolved. No durable checkpoint
or profile-replacement implementation is included here.

## Synchronous persistence dispatch boundary

After the strict comparison selects an update, the client obtains the shared
persistence singleton's nested database context and invokes its
`performBlockAndWait` operation. The MKNest implementation checks both its database and operation queue. With
both present, it invokes the block inline when already on the current queue;
otherwise it wraps the block through a write-block helper and calls
`addOperations:waitUntilFinished:` with a wait flag. The wrapper weakly loads
the owner, invokes the supplied block inside an autorelease pool, then invokes
`processChangedObjects` before draining the pool; downstream effects of that
processing call are not recovered. When either database or queue is
unavailable, the inspected body skips the block and exposes no error result.

The token block writes the token cursor. Its auxiliary loss-check path is
guarded by existing stored cursor values: an existing loss-check value is
positive, then the reread loss-check value equals the existing token value,
before the loss-check setter is reached with the incoming value. If either
guard fails, that auxiliary setter is skipped while the token setter remains
part of the block. The blind-token block writes the blind cursor without this
auxiliary branch.

The nested context is created lazily from a database path, key, and schema
builder. A missing or failed context construction can therefore prevent the
setter block from running. The inspected callback does not expose a durable
save result, rollback, retry, or error callback. Those behaviors, plus reset,
profile replacement, transaction isolation, and restart durability, remain
explicit gaps. A clean-room implementation may model the dispatch as a
serialized effect and return storage errors from its own backend; those are
implementation decisions.

The observed token zero assertion path is represented as a typed error in a
clean-room implementation rather than reproducing process termination. The
blind-token path has no corresponding zero assertion in the inspected body.

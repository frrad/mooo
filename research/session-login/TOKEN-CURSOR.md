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
They are RED characterization data for an independently implemented reducer;
no production implementation is included here.

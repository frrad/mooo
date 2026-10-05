# Incoming BLOCKSYNC state effects

This is a bounded, source-derived state contract for the 26.8.0 macOS client.
It is separate from queue admission and receipt construction. The executable
model in `incoming_blocksync_state_contract_test.go` records observed
in-memory effects only; it is not a datastore implementation.

The BLOCKSYNC handler calls `updatePlusBlockIds:plusBlockTypes:plusUnblockIds:
plusIsFull:plusRevision:` (`0x10140a9ac`) through
`performBlockAndWait:` (`0x10146aef4`). The captured write block is
`0x101433b48`. In the full-sync branch, matching users receive `friendType =
-4` and `purged = 0`. In the partial branch, resolved users receive
`friendType = -3`, the captured block type, `favorite = 0`, and `purged = 0`;
the corresponding chat-room, looked up through the user's `directChatId`, has
its favorite cleared when a nonzero chat identity resolves. A resolved user
with `userType = 0` is assigned `userType = 1`; the setter is conditional.
Resolved users are
updated in place; only an unresolved user is represented in the member list by
a numeric fallback. The incoming block-ID/type vectors are indexed together.
The source `numberAtIndex:` implementation reads the SGIntArray backing buffer
without a bounds check. The synthetic model refuses to reproduce an unsafe
short-array read and returns an explicit Go modeling error; this is an
implementation safety choice, not an official source error contract. A nil
type array's modeled numeric conversion remains zero.

The completion block (`0x101434018`) marks full synchronization only when the
captured full-sync flag is set. For nonempty member work, that flag update is
deferred into the member callback; empty member work takes the immediate
completion path. Both paths perform a separate revision update through
`setPlusBlockRevision:` (`0x101921920`). The model therefore keeps state
mutation and revision completion as distinct steps. The source trace does not establish transaction commit or rollback,
retry behavior, worker exception propagation, or completion-error handling;
those remain explicit gaps.

The plus-unblock arrays are consumed by a separate nested block in the same
write closure. The recovered block looks up users by ID with a constant
`linkId = 0`, assigns `userType = 1` when the current value is zero, clears
`hidden`, and skips `friendType = -4` for `newlyAdded` users. Missing users are
released without adding fallback work. Zero/empty array behavior, nil type
handling, and the remaining unblock state effects are not yet traced. Likewise, the reviewed
completion call proves a direct revision setter invocation but does not yet
establish whether an empty update still invokes it or how a setter failure is
reported. These are intentionally separate follow-up gaps rather than inferred
from the plus-block path.

For the traced plus-member path, the synthetic model distinguishes an empty
fallback list, which can take the direct revision-completion path, from a
nonempty fallback list, which first crosses the
`doPlusBlockMemberWithUserIds:completion:` callback boundary. The callback and
revision ordering is represented as a source-effect fixture; it does not claim
that the callback persists data or that worker failures propagate to the
caller.

The recovered `MKNest performBlockAndWait:` implementation does establish the
operation boundary. It obtains the database and operation queue, invokes the
write block inline when the current queue matches, and otherwise wraps it with
`createWriteBlock:` (`0x10146d580`), creates an `NSBlockOperation`, and calls
`addOperations:waitUntilFinished:YES`. The wrapper invokes the captured write
block with a weakly retained owner, then calls `processChangedObjects`
(`0x1018fb440`) before draining its autorelease pool. A missing database or
queue skips the write block without an exposed error. This proves synchronous
completion of the operation wrapper when the context exists, but it does not
prove that the underlying store has committed, nor does it reveal rollback or
retry behavior.

The wrapper's post-block `processChangedObjects` selector resolves to
`0x10146cf78`. That implementation locks the changed-object collection,
begins a database transaction, classifies deleted and dirty objects, calls
`deleteObject:` or `_save`, and then calls `commit`. It subsequently updates
the in-database flags, clears the changed collection, and dispatches a main
queue notification. Its exception path calls `revert:` for changed objects,
calls `rollback`, clears the collection, unlocks, and rethrows. This is direct
evidence for the generic store pipeline, but it does not prove that every
BLOCKSYNC field mutation is registered in the changed-object collection or
that the completion callback observes a durable commit; those linkage and
error-routing questions remain open.

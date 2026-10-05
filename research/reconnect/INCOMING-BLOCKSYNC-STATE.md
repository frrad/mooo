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
its favorite cleared. Resolved users are
updated in place; only an unresolved user is represented in the member list by
a numeric fallback. The incoming block-ID/type vectors are indexed together.
The source `numberAtIndex:` behavior for an allocated short type array remains
untraced; the synthetic model refuses to guess that case, while a nil array's
modeled numeric conversion remains zero.

The completion block (`0x101434018`) marks full synchronization only when the
captured full-sync flag is set, and performs a separate revision update through
`setPlusBlockRevision:` (`0x101921920`). The model therefore keeps state
mutation and revision completion as distinct steps. The source trace does not establish transaction commit or rollback,
retry behavior, worker exception propagation, or completion-error handling;
those remain explicit gaps.

The plus-unblock arrays are consumed by a separate nested block in the same
write closure. Its complete object lookup, zero/empty-array behavior, nil type
handling, and state effects are not yet traced. Likewise, the reviewed
completion call proves a direct revision setter invocation but does not yet
establish whether an empty update still invokes it or how a setter failure is
reported. These are intentionally separate follow-up gaps rather than inferred
from the plus-block path.

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

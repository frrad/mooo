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
the corresponding chat-room favorite is also cleared. An unresolved user is
represented in the member list by a numeric fallback.

The completion block (`0x101434018`) marks full synchronization and performs a
separate revision update through `setPlusBlockRevision:` (`0x101921920`). The
model therefore keeps state mutation and revision completion as distinct
steps. The source trace does not establish transaction commit or rollback,
retry behavior, worker exception propagation, or completion-error handling;
those remain explicit gaps.

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

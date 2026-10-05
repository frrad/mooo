# Incoming HINT and BLOCKSYNC queue boundaries

This note records a bounded static source contract for the KakaoTalk 26.8.0
macOS client. It does not enable receipt transport or claim that Go execution
timing matches the official database implementation.

The manager handlers invoke their optional delegate before constructing and
sending the corresponding push receipt. The HINT delegate implementation at
`-[BCLocoClient locoManager:didReceiveHintPushNotice:]` (`0x101414820`)
obtains `NTDataStore.nest` and calls `performBlock:completion:` through the
selector stub `0x1018f83c0`. The `MKNest` implementation is
`0x10146acfc`. When database and queue context are available, it either
invokes the block inline on the current queue or creates an `NSBlockOperation`,
sets its completion block, and adds it to the operation queue. The caller
returns after that admission boundary. On the same current queue the block is
invoked inline before the delegate returns; on another queue an operation is
submitted and the delegate returns without joining it. Therefore the manager's
receipt follows delegate return, but it is after inline work and may precede
completion of deferred work. If either database or operation queue is missing,
the traced implementation skips the block without an exposed error.

The BLOCKSYNC delegate implementation at
`-[BCLocoClient locoManager:didReceiveBlockSyncPushNotice:]`
(`0x1014165ac`) calls the selector stub `0x10193bfc0`, whose selector is
`updatePlusBlockIds:plusBlockTypes:plusUnblockIds:plusIsFull:plusRevision:`.
Its implementation is `0x10140a9ac`. That method captures the plus fields and
calls `MKNest performBlockAndWait:` through `0x1018f8400`; the implementation
is `0x10146aef4`. With a database and queue, `performBlockAndWait:` executes
inline on the current queue or submits a write operation with a wait flag on a
different queue. Thus the BLOCKSYNC update method returns across a synchronous
nest boundary before the manager constructs and sends its receipt.

Both handlers' source methods have no recovered Go-equivalent error return for
queue admission. The executable contract test therefore models same-queue
inline HINT work, other-queue HINT admission, missing-context skips, a
synchronous BLOCKSYNC wait, and inline call-stack exceptions that unwind before
receipt construction. It intentionally leaves deferred worker exception
propagation, completion callback errors, transaction durability, and exact
executor scheduling as explicit gaps.

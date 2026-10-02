# CHANGESVR logout dispatch

Status: reviewed static source contract, runtime unexecuted. Observation date:
2026-10-02. Client build: macOS KakaoTalk 26.8.0.

The manager's `locoManager:didReceiveChangeSvrPushNotice:` callback captures
the first object argument, the manager/request owner for this callback shape.
It submits a block through a shared main-thread dispatch helper. On the main
thread the helper invokes the block inline; off the main thread it dispatches
the block to the main queue. The block sends `logoutForChangeServer` to the
captured manager. The traced `logoutForChangeServer` body then calls
`clearCarriageAddress`, checks `hasMoreTicketAddresses`, conditionally calls
`moveTicketAddressCursor`, and sends `logout`. Server-change payload parsing,
session reset details, and observer effects are outside this traced slice.

The clean-room contract has two observed scheduling branches:

* main-thread callback: invoke `logoutForChangeServer` inline on the captured
  manager, then apply the ordered logout body;
* non-main-thread callback: dispatch to the main queue, then invoke the same
  selector on the captured manager and apply the ordered logout body.

The helper's scheduling and block invocation remain observable even when the
captured manager is nil. The block then performs an Objective-C message send to
the nil receiver, which is a no-op; consequently the logout body does not run.
Whether the surrounding manager should recover after this nil-receiver path is
untraced.

## Static provenance

- Callback IMP: `0x10141456c`, selector
  `locoManager:didReceiveChangeSvrPushNotice:`.
- Callback block body: `0x10141e1a0`.
- Shared thread dispatch helper: `0x1017ec120`; it tests the current-thread
  main-thread predicate and either invokes the block or dispatches it to the
  main queue.
- Block selector: `logoutForChangeServer`.
- Logout body IMP: `0x10151869c`; it invokes `clearCarriageAddress`, checks
  `hasMoreTicketAddresses`, conditionally invokes `moveTicketAddressCursor`,
  and sends `logout`.

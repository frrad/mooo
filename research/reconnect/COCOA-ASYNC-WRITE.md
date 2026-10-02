# CocoaAsyncSocket write queue contract

Status: reviewed static contract, runtime unexecuted.

The analyzed client is KakaoTalk for macOS 26.8.0. Its bundled arm64
CocoaAsyncSocket framework identifies itself as `CocoaAsyncSocket` version
`Pods-1`; the inspected framework slice has SHA-256
`5e436f579d2d64e100350303ad998483068b8badd210b62110bf2bd254c54132`.

The LocoAgent constructor creates a named delegate queue (`com.kakao.loco.socket`)
and calls `initWithDelegate:delegateQueue:` with the LocoAgent as delegate and
that queue as delegate queue. CocoaAsyncSocket's two-queue initializer stores
the delegate and delegate queue, then creates a separate internal socket queue
when no explicit socket queue is supplied. The client therefore has distinct
queues for socket I/O and LocoAgent callbacks.

`writeData:withTimeout:tag:` ignores zero-length data. For nonempty data it
creates a write packet and asynchronously enqueues it on the internal socket
queue. The socket queue performs the underlying write. Each positive incomplete
write delta dispatches `socket:didWritePartialDataOfLength:tag:` to the delegate
queue with the socket, positive bytes written for that invocation, and signed
tag. Multiple incomplete writes therefore produce one partial callback per
positive delta; zero-progress attempts do not dispatch a partial callback. A
completed write dispatches
`socket:didWriteDataWithTag:` with the socket and signed tag; the current write
is then ended. The framework's callback blocks use the delegate queue, while
the packet enqueue and OS write use the internal socket queue.

At the LocoAgent layer, `sendPacket:tag:` returns after submitting the socket
write. The agent then enables its out-segment timeout and receive-header timeout
as separate effects. Later partial or complete delegate callbacks disable the
out-segment timeout; completion then invokes the agent's empty did-write hook.
The cross-queue execution race remains an implementation concern: callback
delivery can occur after submission, and this source does not establish a
stronger wall-clock ordering between the two queues.

The synthetic characterization fixture is
`internal/protocol/sessionlogin/testdata/reconnect/rc-q5-cocoa-async-write.json`.
It keeps submission, internal-socket, and delegate events as separate ordered
streams so an implementation cannot pass by collapsing queue work into a
synchronous write. Runtime execution and lower-level socket error policy remain
unexecuted/untraced here.

## Provenance

The relevant framework methods are the arm64 implementations of
`writeData:withTimeout:tag:` at offset `0xa518`, its enqueue block at `0xa5e8`,
`maybeDequeueWrite` at `0xa7f0`, `doWriteData` at `0xa8ec`,
`completeCurrentWrite` at `0xadb4`, and callback blocks at `0xad80` and
`0xae74`. The client-side LocoAgent producer callback calls `sendPacket:tag:`
at client address `0x101775314`; the completion variant calls it at
`0x1017754d8`. These addresses are provenance for this client/framework build,
not stable API identifiers.

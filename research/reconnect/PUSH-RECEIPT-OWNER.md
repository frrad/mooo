# Push-receipt owner scheduling

Status: reviewed static source contract, runtime unexecuted. Observation date:
2026-10-02. Client build: macOS KakaoTalk 26.8.0.

`sendCarriagePushReceipt:` first dispatches a main-queue cancellation block for
`sendPingRequest:` on the manager/request-owner target. It then invokes
`sendPushReceipt:` inline through the current carriage-agent object and finally
dispatches a second main-queue block that reads the configured carriage ping interval and
schedules `sendPingRequest:` on the manager/request-owner target with that delay. The source proves enqueue/invocation order, but does not prove
which queued block executes first relative to the inline send or whether a nil
agent is supported; the fixture scopes to an agent-present call.

The push-receipt method's execution-time status gate and signed-negated packet
ID tag are specified separately in `PUSH-RECEIPT.md`. Ping packet serialization,
queue races, socket completion, and terminal notice consumers remain gaps.

## Static provenance

- Caller `sendCarriagePushReceipt:` IMP: `0x101514f8c`; its blocks are
  `0x101520444` (cancel) and `0x101520464` (schedule).
- The cancel and schedule blocks capture the manager/request-owner target. Both
  use selector `sendPingRequest:` and a nil object; the schedule block uses the
  configured delayed-selector mechanism.
- The inline dispatch targets `sendPushReceipt:` through `carriageAgent`.

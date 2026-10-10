# Regular-group failure recovery

Evidence date: 2026-10-10. Owned acceptance of selected-room Matrix outage,
refusal, lost acknowledgement, restart and process-stall faults is recorded
below. Media faults, owned server session termination and the remaining Mac
manager/consumer trace are recorded gaps.

## Observed production gap and regression

The live connector called `handleEvent` but ignored its false result. A failed
or backgrounded Matrix delivery therefore left its source position uncommitted
while admitting later events and continuing to report connected. The protocol
client's commit queue rejects later per-chat commits, so this observation does
not establish a cursor skip. It does establish stalled recovery and potential
out-of-order Matrix delivery without an actionable state transition.

A production-path regression sends two synthetic events through the running
connector with an open source stream and makes the first framework handling
result failed or queued. Both cases failed before the change: the owner remained
running. The pump now stops admission, reports `kakao-delivery-paused`, and uses
the existing bounded cleanup/reconnect policy. A second regression verifies
that the previous owner closes before replacement and that catch-up delivers
both messages in source order before live subscription. Outbound mutations do
not enter this replay path. Race-enabled connector tests and `make check` pass.

## Official-client trace

Method: read-only static inspection of the authorized macOS 26.8.0 arm64 binary,
using the existing serialized Ghidra project. No public prior art was used as
the starting point for this slice. These observations are static leads, not
executed parity fixtures or owned-account acceptance.

The regular carriage `LocoAgent.socketDidDisconnect:withError:` implementation
at `101774714` updates status through `setStatus:error:`, cancels scheduled
operations, and enumerates pending callback state. Other implementations of the
same selector belong to different socket consumers and must not be substituted
for the regular carriage path. `setStatus:error:` at `1017735b8` stores the
status and invokes an installed status-change block when present.

`failPendingRequestsWithError:` at `101774ce8` enumerates the pending callback
map, invokes each completion through block `101774f24`, and clears both
correlation maps. This confirms the shared callback ownership boundary already
recorded in session-login evidence SL-BIN-024. The installed manager block,
complete routing/recovery decisions, durable cursor effects, downstream UI
state and all failure branches still require explicit tracing for this slice.
The bridge's Matrix outage policy is an implementation decision; the native
client has no Matrix delivery boundary.

The manager's regular carriage connection method at `1015182f4` installs the
status block at `10151f174`; ARM64 block construction identifies this address
directly. It checks that the reporting agent is still the current carriage,
guards the initial completion with a one-shot captured flag, clears the
carriage on the disconnected branch, updates manager status and removes the
agent callback. Its main-queue block at `10151f2cc` cancels scheduled ping
requests. The callback argument ABI and complete numeric status meanings
remain unexecuted; static branches must not be promoted to semantic enum parity.

The candidate regular manager status setter at `101514a50` stores the status
and notifies a responding delegate via
`locoManager:didChangeStatusFromStatus:toStatus:`. Other `setStatus:` methods
exist in this binary; the delegate consumer and owner association still need
tracing before claiming the complete recovery/UI chain.
The concrete delegate at `101413b68` updates its logged-in flag, notifies its
delegate when that flag changes, and schedules `resetAndRecoverLocoLogin` on
the main thread for the numeric transition `25 -> 26` when neither logout nor
recovery is already active. The other same-selector entry was not a usable
concrete implementation. This establishes the guarded recovery handoff; the
reset/recovery routine, final logout, persistence and UI consumers remain gaps.
`BCLocoClient.resetAndRecoverLocoLogin` at `1014098ec` first invokes logout,
then schedules `recoverLocoLogin:` only if the stored user ID is positive and
the stored access token is nonempty. It cancels an earlier scheduled invocation
for the same recovery cursor before scheduling the next. Exact delay and the
recovery routine's subsequent branches remain unverified here. No live
credentials were read or exercised by this static inspection.

`recoverLocoLogin:` at `101409410` compares the supplied cursor with the
current recovery cursor before doing work. It marks recovery active and
schedules a delayed failure completion when starting an attempt. It calls
`login:completion:` only when the network monitor does not report unreachable,
the authentication component reports logged in, and recovery is not disabled;
it sets the disable flag before dispatch. Exact delay values, timeout races and
the login request's complete persistent effects remain unverified.

The login completion at `101435f38` cancels the delayed failure completion on
its nonzero-result branch, calls `didRecoverLocoLogin:`, then notifies
`locoClientDidRecover`. Its zero-result branch clears the disable flag and
dispatches a main-queue block at `101436014`. `didRecoverLocoLogin:` at
`101409714` cancels scheduled recovery for the current cursor, increments that
cursor, and clears both recovery flags. Its false-result branch invokes an
additional global block whose downstream behavior remains untraced. These
static observations do not establish bounded native retries or the full UI
failure contract.
The main-queue block at `101436014` schedules `recoverLocoLogin:` again using
the captured cursor. This closes the static failure-to-reschedule edge; the
floating-point delay argument still needs instruction-level ABI verification,
and this edge alone does not prove a maximum retry count.

## Remaining gaps

Owned acceptance below covers selected-room Matrix outage, refusal and lost
acknowledgement, restart during an outage, a process stall long enough to lose
the source session, and outbound sends across that stall. These remain open:

- Owned encrypted multipart (album or Mini text) partial success; only the
  synthetic production-path regression covers it.
- Media faults: interrupted inbound download, interrupted outbound upload and
  Disconnect during an in-flight transfer. Inbound conversion runs without a
  cancellable context, so a transfer can outlast the five-second shutdown
  bound, which then retains the source owner rather than releasing it. Owned
  media fault acceptance moves to the group photo slice.
- Server-initiated session termination (kickout, change-server) in an owned
  session; synthetic tests cover the policy.
- A reproducible ambiguous outbound send where Kakao applied the write but the
  bridge saw no reply. The status is regression-tested; the stall run below did
  not happen to produce one.
- An ambiguous send that did reach Kakao later arrives as a self-authored source
  message without a mapping and is bridged as a new Matrix event beside the
  failed original. That is visible rather than silent, but it is a duplicate in
  Matrix.
- The Mac recovery trace gaps listed above.

Each injected fault and source/Matrix mutation needs a private durable attempt
receipt before dispatch. Real account-specific artifacts remain outside tracked
files. The current implementation is not a claim of complete failure parity.

## Catch-up replay during a continuing outage

A live delivery pause reconnects after a short delay, and its catch-up replays
the paused event. If Matrix is still unavailable, that replay fails too.
Catch-up failures were never retried, so recovery stopped with a connect
failure until an operator reconnected; the first owned outage run recovered
only because Matrix returned while the SDK was still retrying one request. A
production regression with three source sessions reproduced the stop. An
unconfirmed Matrix delivery during catch-up now uses the same bounded recovery
budget as the live pause and reports `kakao-delivery-paused`; source-side
catch-up failures remain terminal. After five attempts (about two minutes plus
SDK retry time) recovery stops and needs an explicit reconnect.

## Outbound sends

A single Kakao send is never retried. Its Matrix status previously defaulted
to the SDK's retriable failure, which invites a manual resend of a message Kakao
may already have delivered. A server status reply is now reported as a certain
refusal. Any other failure, and an accepted send without a log position, is a
permanent failure whose notice says delivery is unconfirmed and asks the user
to check KakaoTalk before resending.

The homeserver resends an appservice transaction it did not see acknowledged,
and the SDK hands every copy of the event to the connector before any copy is
saved; its optional Matrix-message deduplication runs before the send and is
off by default. Owned acceptance reproduced this: one Matrix message reached
KakaoTalk three times. Each Matrix event now reserves a durable attempt record
in the bridge's key-value store immediately before its one source send. A later
copy, including one after a restart, is not sent and does not overwrite the
original event's status. Records are pruned after 30 days.

The same run deadlocked the bridge. The SDK holds a portal's event lock while
it calls the Matrix message, reaction and read-receipt handlers. Those handlers
waited for the connector gate, which the Kakao pump holds while it queues a
remote event into the same portal. A regression reproduces the cycle with the
real handlers. The Matrix handlers no longer take the gate. Source access blocks
are set in memory before they are persisted, so the per-send access check still
refuses a chat once removal is observed; a send admitted just before a
concurrent removal is processed may still proceed, as it would have just before.

## Owned encrypted Matrix outage acceptance

The revised bridge used the original B secondary profile and an existing owned
A/B/C regular group. Its encrypted portal initially held two historical maps.
A separately receipted native A baseline text produced one live mapping and a
matching durable source cursor. A localhost proxy then blocked only send
requests to that selected Matrix room; it recorded bounded attempt counters
without request bodies, credentials or source identifiers.

A sent one further synthetic text once. While Matrix HTTP attempts were being
rejected, the portal remained at three mappings and the source cursor remained
at the baseline. The connector eventually reported `kakao-delivery-paused` and
started recovery. A sent one later text while the fault remained active. A
second checkpoint read still showed no progress past the baseline. The operator
restored Matrix transport, and the same bridge process automatically reconnected
and delivered both texts without an explicit restart or repeat send.

Readback confirmed five total mappings with the initial two unchanged, exactly
five encrypted source-author events, the expected A ghost and timeline order
matching native source order. The retained Matrix SDK decrypted the baseline,
failed-delivery and later text to their exact expected fixtures. Durable source
progress reached the later text only after the two recovered mappings existed.
SIGINT to the recorded recovered bridge process completed successfully. This
establishes one controlled selected-room HTTP outage and automatic inbound text
recovery. It does not establish lost-acknowledgement recovery, initial delivery
before a room has any committed cursor, other failure types or multipart safety.
Subsequent read-only checks of the official B and C clients confirmed the
baseline, failed-delivery and later texts exactly once, in source order, in the
same three-member group.

## Owned fault acceptance, second run (2026-10-10)

Method: a fresh build of this branch used the original B secondary profile and
the same owned encrypted A/B/C regular group. The checkpoint migrated from
version 5 to 6 on first open with cursors intact; a private backup preceded
the run. A localhost proxy faulted only send requests for the selected room
and wrote a private receipt for each faulted request, recording the mode and
the request's transaction path segment but no body. A sent each synthetic text
exactly once through a guarded native helper with a private receipt written
before typing. A tester device in the room decrypted results with the Matrix
SDK. Per-step checkpoint and mapping readbacks were kept privately.

| Fault | Observed | Result |
|---|---|---|
| None (baseline) | One mapping; cursor equal to the latest mapping. | Pass |
| 403 `M_FORBIDDEN` for about 75 s | One live refusal and three catch-up replay refusals, each reported `kakao-delivery-paused` and retried within budget; no mapping or cursor progress. After restore, one event and the cursor advanced. | Pass; the catch-up retry fix was required |
| Applied, acknowledgement dropped, about 80 s | Seven send attempts across SDK retries and reconnect replays, all with one stable `mooo-history-` transaction ID. After restore, exactly one Matrix event. | Pass |
| 503, then SIGINT during the pause, restore, new process | Shutdown completed in 5 s with the in-flight event uncommitted; the new process delivered it once. | Pass |
| SIGSTOP for 4 minutes, with one native A text and one tester Matrix text sent during the stall | First run: inbound recovered once, but the outbound text reached KakaoTalk three times and one handler deadlocked. Rerun after the fixes above: the homeserver redelivered the outbound event twice more, both copies were refused, the text appeared once on A and once in Matrix, and the inbound text once. | Pass after fixes |
| None (final) | One inbound and one outbound text, each once. Clean SIGINT. | Pass |

All six native A texts in the run decrypted to their exact private fixtures,
once each, from the same sender, in source order. Native B and C clients were
not re-read in this run.

## Suppressed Matrix refusals

The SDK converts selected Matrix errors into an ignored-success result to avoid
bubbling expected room failures. A production regression through its real
`QueueRemoteEvent` path reproduced a false source commit after a forbidden send
with no persisted message mapping. The connector now verifies that an ignored
source message has an existing mapping before allowing its source commit. A
missing mapping or database read failure leaves progress retained for recovery.

Regression cases cover forbidden, not-found, bad-JSON, invalid-parameter and
bad-state errors, followed by restored delivery and duplicate reconciliation.
They verify one Matrix send and one stored mapping after recovery. The owned
acceptance below exercises the forbidden case end to end.
Ignored source messages in filtered or uncreated portals no longer authorize
source progress merely because the SDK returned ignored success.
The same SDK result also affected history's independent journal. A failing
regression reproduced false history completion after a forbidden Matrix send.
Both delivery paths now share the mapping check; the historical regression
verifies that progress remains at the original cursor and explicit resume
succeeds after delivery is restored.

The same check must cover the entire multipart message. A real SDK regression
delivered the first Mini text part, refused the second with `M_FORBIDDEN`, and
reproduced a false source commit because one mapping existed. Albums and Mini
messages already use upserts to resume missing parts; they now also expose
their complete expected part IDs to the commit check. An ignored SDK result
cannot authorize a commit while any expected mapping is missing. The regression
restores delivery, verifies only the second part is sent, then replays the
complete message and verifies two ordered events and two mappings without
duplicates. This is synthetic production-path evidence, not owned encrypted
multipart acceptance or lost-acknowledgement proof.

## Lost Matrix acknowledgement

Live `newMessage` conversion now attaches the same stable per-part transaction
identity used by history. The identity includes Matrix room and sender, bridge
and receiver, source message and part IDs, and event type. Retaining the existing
transaction namespace also prevents live/history overlap from generating a
second identity for the same part. The existing crypto and HTTP adapters remove
the private marker before encryption or plaintext transmission and preserve the
SDK's concrete connector and intent types. Duplicate converted part IDs fail
before any event send because they would alias a transaction.

A production-conversion/HTTP regression failed before the change because live
text had no stable transaction identity. Its synthetic server applies the first
request and drops the acknowledgement. A fresh connector/conversion path then
replays the source event with a different SDK URL transaction; the adapter sends
the same stable transaction and the server retains one applied event. This does
not by itself verify the complete encrypted SDK send or an owned homeserver's
transaction deduplication; the owned acceptance below does.

A double puppet on another homeserver gets its own SDK HTTP client, which the
bridge-wide adapter cannot reach. Failing closed there would have stopped live
delivery for that sender. Live conversion now leaves such parts unmarked and
keeps SDK transaction IDs, so that one case retains the pre-existing
lost-acknowledgement exposure; history still refuses it before sending. A
regression also showed that any other send in a live handling context, such as
an unmarked encrypted event, inherited the previous part's stable ID, which a
homeserver would deduplicate away. Unmarked live sends now clear the selected
identity and keep their own transaction.

## First admitted message before any commit

A production decoder/restart regression confirmed that a failed first live
message in a never-committed room was omitted from resume targets. Inventory
alone must continue to omit such rooms to avoid automatic historical backfill.
The client now persists the earliest validated live admission before exposing
that message to the application. This separate delivery start is not a login
cursor, a read watermark or a successful application commit.

Checkpoint version 6 adds the optional per-room delivery starts. Version 5
migration preserves committed positions, read watermarks and session state;
older supported migrations retain their existing read-watermark policy. A
delivery start authorizes recovery only from one position before the first
admitted message through the account's source-visible inventory maximum. The
start persists after retrieval and clears with a real application commit;
subsequent recovery uses that committed boundary. Explicit source deletion
removes the delivery start as well as the room's other continuity state.

Race-enabled regressions reopen the persisted checkpoint, verify that login
cursors and read watermarks remain untouched, assert the actual SYNCMSG request
uses the admitted lower bound, and commit the recovered events through the
production client. Owned-account acceptance of this new boundary is pending.
An older binary cannot read version 6 checkpoints; lab acceptance must use a
fresh build rather than reusing the previous experiment's binary.

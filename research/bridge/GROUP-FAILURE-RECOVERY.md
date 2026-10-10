# Regular-group failure recovery

Evidence date: 2026-10-10. Selected-room Matrix outage acceptance is recorded
below. Broader fault acceptance and the remaining Mac manager/consumer trace
are pending.

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

## Acceptance still required

- Owned A/B/C encrypted inbound text through network loss and a selected-room
  Matrix outage; exact content, identities, source ordering and deduplication.
- Restart/offline recovery and cursor readback before and after failed delivery.
- Controlled server/session termination without unsafe authentication retries.
- Partial media transfer and bounded shutdown/resource release.
- Ambiguous outbound acceptance with no automatic repeat and a useful error.
- Lost Matrix acknowledgements and multipart partial-success replay, which the
  live-pump regressions above do not cover.

Each injected fault and source/Matrix mutation needs a private durable attempt
receipt before dispatch. Real account-specific artifacts remain outside tracked
files. The current implementation is not a claim of complete failure parity.

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

## Suppressed Matrix refusals

The SDK converts selected Matrix errors into an ignored-success result to avoid
bubbling expected room failures. A production regression through its real
`QueueRemoteEvent` path reproduced a false source commit after a forbidden send
with no persisted message mapping. The connector now verifies that an ignored
source message has an existing mapping before allowing its source commit. A
missing mapping or database read failure leaves progress retained for recovery.

Regression cases cover forbidden, not-found, bad-JSON, invalid-parameter and
bad-state errors, followed by restored delivery and duplicate reconciliation.
They verify one Matrix send and one stored mapping after recovery. These are
synthetic SDK/connector tests; owned-account refusal acceptance is still pending.
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
not yet verify the complete encrypted SDK send, database recovery, or an owned
homeserver's retention of transaction deduplication. External double-puppet
clients without the installed transport currently fail closed; preserving their
live delivery support requires additional integration before this slice lands.

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

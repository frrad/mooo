# Startup bulk acquisition implementation

Implementation checkpoint: 2026-10-13. This is the source acquisition layer for
clean-login history, not completion of the three bridge flows.

## Contract and evidence

`Client.AcquireStartupHistory` consumes the complete chat inventory and current
LOGINLIST global `minLogId`. It selects regular direct/group rooms whose acquired
bulk cursor is below the inventory's last log and whose last log is at least the
global floor. Inventory previews, delivered-message positions and read watermarks
are not acquisition progress. Requests sort positive signed room IDs and pair
`chatIds` with persisted `sinces`, at most 100 rooms per request.

The request shape and empty/default response fields are covered against executed
Mac 26.8.0/build 2000 model evidence HST-EXEC-001/003. Coordinator selection and
retirement follow independently recovered static control flow, tested with a
scripted backend through the production client. Those synthetic coordinator tests
do not establish executed official coordinator parity. See
[official chain](HISTORY-STATIC.md) and
[model fixture](../fixtures/history-startup/model-runtime.json).

For EOF=true, retire the selected batch. For EOF=false, retire selected room IDs
strictly below the last returned room; retain the boundary and later rooms. Store
returned records before cursor retirement. A partial response advances each room
only to its highest durably acquired returned identity; sparse IDs are not counts.
A valid empty EOF ends that attempt without inventing a message/cursor. It does
not establish that inventory history has been fully imported or is unavailable.

SYNCMSG pages also reconstruct this acquired cursor; targeted GETMSGS, inventory
previews, delivery and read acknowledgements do not. Reopening derives progress
from durable pages and rejects a bulk journal that points past its available
pages. This closes the crash window between page publication and journal update.

## Pacing and failure behavior

Bulk requests share the existing profile's zero-held-count SYNCMSG admission
worker: one in-flight request, configured minimum spacing, durable error cooldown,
bounded demand and finite request duration. The default startup demand is 25
pages/one minute; a selected subset and smaller budget are available to the SDK.
Concurrent startup coordinators serialize and rebuild eligibility after the
previous invocation. A no-progress non-EOF reply stops with `ErrGapUnresolved`
and retains the longer cooldown. There is no automatic source retry loop.

A selected subset must contain unique positive IDs present as supported regular
rooms; an invalid selection fails before bulk dispatch. Production validation also
rejects unsorted/duplicate rooms and mismatched arrays, even though the native
request model accepts malformed empty/mismatched arrays. Responses require
positive room/message identities and increasing order within numerically ordered
rooms, so an invalid boundary cannot retire another room. These are deliberate
fail-closed production guards, not model-parity claims for malformed inputs.

The bridge's opt-in `network.startup_history` executes acquisition before group
discovery, inventory preview delivery and catch-up. It defaults to false. The
current slice caches older records; it does not automatically insert those older
records into Matrix. Failure stops bootstrap before live subscription; this is a
conservative bridge policy rather than a claim to reproduce the native callback's
failure continuation. Budget exhaustion reports pending room count and permits
the existing bootstrap to continue.

## Owned acceptance: HST-BULK-LIVE-008

On 2026-10-13, the production SDK ran against the existing authorized secondary
device for one disposable owned regular group, after verifying the exact three
owned participants. Its local authorization was cloned into a private directory
with empty local history while the original profile lease remained held. This is
an empty local store on an existing server device, not fresh device registration.

One explicitly selected MCHATLOGS page returned five records with EOF=true.
Reading the range from the acquired store returned five events without SYNCMSG.
After shutdown/reopen, HTTP and both LOCO dialers were denied: the same page,
including identities, order and projected content, was returned with zero network
attempts. Delivery/read cursors stayed unchanged; the original authorization file
hash stayed unchanged. No message send, read acknowledgement, UI interaction or
real Beeper deployment was used. Raw reply, identifiers and private receipts stay
outside Git. The sanitized
[acceptance fixture](../fixtures/history-startup/observed-sdk-bulk.json) records
only aggregate outcomes.

This verifies SDK acquisition/cache/restart for one five-record group. It does not
verify official fresh-startup UI, direct-chat read side effects, encrypted Matrix
history delivery, full pagination on that group, or support for every returned
message body. Existing vote-history fidelity gaps remain explicit.

## Remaining completion gates

- Observe the complete official clean-local-history startup through visible
  results, including source read effects; the earlier fresh Mac connection failed
  before history and remains a gap.
- Deliver acquired startup ranges through the actual bridge with durable Matrix
  deduplication, bounded dependency lookups and owned encrypted acceptance.
- Implement local-first room opening and backward history pagination with an
  explicit viewing dispatcher; standard appservice traffic does not invoke it.
- Consolidate ownership across profiles for the same account and account for
  metadata/media dependencies in pacing. Current admission remains profile-scoped.
- Trace remaining SYNCMSG callback semantics, deletion during startup and unknown
  content; do not equate EOF, an empty reply or a cached gap with full fidelity.

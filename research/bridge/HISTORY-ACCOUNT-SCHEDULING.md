# Account history scheduling and LOCO dependencies

Implementation checkpoint: 2026-10-14 UTC. This advances the three-flow goal's
account worker; it does not complete the HTTP dependency or end-to-end gates.

## Account ownership

Production profile opening now selects one scheduler per account in a private
scheduling directory. The default is the profile's parent directory, matching the
bridge's common `network.profile_dir`. Operators placing profiles for one account
in different directories must set the same `history_pacing.state_directory`.
Concurrent same-process opens reject conflicting roots or pacing policies rather
than silently creating parallel account workers. Different accounts remain
independent.

The account filename derives from a digest of the account identity. Its private
JSON record retains the account identity and cooldown; it is not public data.
The directory must be mode 0700; state and lease files remain owner-only. The
record is checked at open and reloaded under a stable file lock for each dispatch.
Corrupt, mismatched or unsafe state fails closed.

A shared in-process queue bounds pending demand across profiles. A nonblocking
OS file lease covers reload, cooldown waiting, one source request, and durable
publication, enforcing one in-flight acquisition across cooperating processes
using that directory. Lease waiting has cancellation and each demand has the
configured maximum duration. Each process bounds its own queue; there is no
cross-host coordinator or global queue across unrelated deployments. All processes
for one deployment must use the same scheduling root.

Device/session response flights remain within each profile. Identical simultaneous
requests coalesce there; profiles share timing and concurrency, never a response
from another device's possibly different history window. Closing one profile
cancels and joins its demand without stopping another profile. Registry ownership
is released only when that profile's pending work has finished. Shutdown joins
cooldown publication before releasing the profile lease.

## Durability and policy changes

Before source dispatch, reserve the longer error cooldown on disk. Validated
success replaces it with minimum spacing; errors retain the longer reservation.
A failed write poisons the shared in-process gate. A crash or missing response
therefore cannot reset admission to immediate dispatch.

The state records completion and error times as well as the effective reservation.
A later process/profile with a more conservative configuration also waits its own
interval relative to those times. A restart cannot shorten an existing reservation
merely by changing profile filenames or selecting a faster configuration.

Existing profile `.history-admission` records remain migration inputs. Under the
account lease, the worker inspects bounded sibling records and imports matching
account cooldowns before dispatch. It publishes a future imported floor even if
that demand later times out while waiting. Profiles using an explicit scheduling
root also contribute their own loaded legacy reservation to the active registry.
Unreadable legacy files fail closed because their account cannot be classified
safely. The maximum inventory scan is 4096 directory entries.

## Consuming paths and dependency validation

Zero-held-count SYNCMSG and startup MCHATLOGS use this scheduler through their
existing validated, durable acquisition paths. Each connected session inherits
its profile's admission object before it is published to callers.

The following read-only LOCO dependencies now use the same account scheduler:
CHATINFO, MEMBER, MEMLIST, GETMOMETA, GETMCMETA, CHATONROOM and GETMSGS. Typed response
decoding happens inside admission, before retiring the error reservation. CHATINFO
and MEMBER also validate the selected room there. GETMSGS validates requested
identities and persists current target projections before publishing a coalesced
reply. Each physical MEMBER batch is admitted separately; filtering all IDs out
sends no request.

Explicit read acknowledgements, writes and keepalive traffic retain their separate
paths. Empty valid metadata/exact-message responses retain their API semantics;
primary history non-progress still stops/backoffs rather than looping. No chosen
interval is asserted to be a Kakao server rate limit.

## Verification and remaining gates

Race-enabled tests exercise production profile opening and account admission,
including two device responses, shared queue bounds, closing one profile, legacy
migration/restart, stronger policy on reopen, explicit roots/policy conflicts and
an actual helper process contending on the same file lease. Scripted LOCO peers
exercise real Client history, CHATINFO and GETMSGS paths: dependencies wait behind
active history, malformed/wrong-room metadata retains cooldown, and shutdown
retains the profile lease until dependency publication finishes. No live account,
source stress probe or real Beeper was used.

Remaining work: authenticated HTTP reaction metadata and media/avatar/sticker
acquisition must join account accounting, with bounded bytes and validation.
Bootstrap authentication/inventory requests remain outside this scheduler.
Viewing dispatch and actual HTTP/owned encrypted history acceptance remain open.
Current tests prove synthetic scheduling and persistence behavior, not an official
client's pacing policy or the complete three-flow result.

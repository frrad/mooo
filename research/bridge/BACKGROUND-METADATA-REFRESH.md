# Background metadata refresh

## Native evidence and boundary

The recovered Mac 26.8.0 MEMBER chain filters invalid/self IDs, requests up to
500 identities, persists successful batches as users keyed by user/link identity,
and abandons later batches on failure. Adding users to the response's room is a
separate optional coordinator action. Member-list UI callbacks fetch only locally
missing profiles. LOGINLIST/LCHATLIST already upsert room data; CHATINFO is used
conditionally for unknown/invalidated rooms. The official client's serial dispatch
queues and timer callbacks do not establish a global request-spacing policy.

Sources: [chat metadata](../chat-metadata.md),
[profile chain and executed models](GROUP-MEMBER-PROFILES.md), and
[profile freshness research](PROFILE-REFRESH-COOLDOWN.md). Private provenance is
`group-member-profile-chain`, `group-member-profile-db-callbacks` and
`profile-refresh-cooldown-20261014`; reports reviewed 2026-10-14. Exact success
callback boundaries were previously independently resolved. Full native room
invalidation and background profile notification coverage remain gaps. No claim
that this worker reproduces native pacing or refresh triggers is made.

## Implemented behavior

This is bridge policy. It fixes [issue #372](https://github.com/frrad/mooo/issues/372),
where a once-a-minute group refresh ran five paced source reads under a
five-second deadline, and the bridge then treated the local deadline as a
connection failure and reconnected.

- **Room-first snapshots.** A room snapshot reads only the authoritative roster
  (MEMLIST). Startup discovery reuses LOGINLIST room data instead of CHATINFO.
  Profiles, personal room metadata and Boards come from persisted checkpoints;
  unknown optional state leaves existing Matrix state unchanged.
- **One queue per login.** Optional reads are bounded (1024 tasks) and coalesced
  per kind and room. One worker drains them, and every physical read keeps the
  SDK's account-wide pacing. The queue holds MEMBER batches, one account-wide
  personal metadata read (GETMCMETA), Boards (GETMOMETA), room info and avatars,
  plus periodic roster checks. A task gets two minutes from dispatch; waiting
  in the queue never counts against a caller's deadline.
- **Safe publication.** The event pump publishes results only after checking
  the active session, the room's snapshot generation and its removal/pause
  state. Enrichment results never carry a membership map. Avatars are uploaded
  in the worker so the pump never waits on a download.
- **Failure scope.** Only an error returned by a Kakao read can end the session,
  and only when it is a transport failure. Local deadlines and cancellation,
  Matrix errors and database errors never can. A failed task retries after 2,
  4, 8 ... minutes, capped at one hour.
- **Periodic roster checks.** The minute ticker rotates through managed groups
  and enqueues one roster check (MEMLIST) plus due enrichment. If the roster
  differs from the persisted one, or the group is paused (`MembershipPending`),
  the pump runs the same authoritative membership refresh a membership notice
  uses. A group paused by a failed refresh is therefore retried, and a lost
  leave notice is still reconciled.
- **Freshness.** Profiles keep the per-user policy from
  [PROFILE-REFRESH-COOLDOWN.md](PROFILE-REFRESH-COOLDOWN.md). Personal and Boards
  checkpoints are fresh for 15 minutes and are invalidated by the corresponding
  push notices.

## Owned-account acceptance (2026-10-14)

Setup: owned lab accounts and the standard bridge with a local Synapse. The
bridge was built from this branch plus a private packet-counting overlay; that
overlay is not in the repository. The test used an existing three-member owned
group. Before the run, one member changed their nickname in the official
Android client (26.8.x), so the bridge's stored nickname was stale.

1. **Cold start with an injected fault.** After CONNECTED, the overlay forced
   one local history-admission failure. That arms the account-wide 30-second
   error cooldown without sending anything to Kakao. Startup made only MEMLIST
   (one per group) and CHATONROOM reads: no CHATINFO, MEMBER, personal metadata
   or Boards reads. Queued enrichment waited out the cooldown, then issued two
   MEMBER reads, one GETMCMETA and one GETMOMETA per group. The new nickname
   reached the Matrix room. The session stayed connected through several
   profile ticks: one CHECKIN and one LOGINLIST, no reconnect, and no repeat
   MEMBER reads within the freshness window.
2. **Warm restart.** A restart inside the freshness window made no MEMBER,
   GETMCMETA or GETMOMETA reads; persisted enrichment was reused.
3. **Fixed build.** The run was repeated after the review fixes described above
   (source-only failure classification, retry backoff, periodic roster checks,
   personal-name preservation in room-info refresh). Each tick issued one MEMLIST
   with no reconnect.

Startup is still paced at about two seconds per read: one MEMLIST per group and
one CHATONROOM per bridged chat, about 30 seconds for this account. Moving those
reads behind CONNECTED is a separate question.

## Remaining gaps

- Native room invalidation and background profile notification triggers are not
  fully traced. No claim is made that the worker reproduces native pacing.
- Personal metadata cached in a checkpoint takes precedence over a snapshot's
  inventory value. This assumes the checkpoint is the fresher of the two, which
  holds for every write path in this change.

# Account-wide member profile reuse

## Evidence

Research date: 2026-10-14. Target: official KakaoTalk for Mac 26.8.0,
arm64, build 2000. Method: read-only static selector/caller indexing and exact
method analysis of the authorized binary, alongside the previously recovered
MEMBER coordinator, persistence blocks and member-list UI callbacks. No live
account requests were made for this research.

The native client upserts users by `(userId, linkId)`, independently of room
membership. The member-list UI requests MEMBER only for IDs without local user
records. See [chat metadata](../chat-metadata.md). This is evidence for persistent
identity reuse, not for a specific expiration interval.

A new review of `profileUpdatedAt` caller references found its getter in
`needToShowNewBadge`; that method makes a date comparison to determine badge
visibility. Previously traced HTTP self-profile success callbacks set the
stored timestamp. The exact `updateProfileWithUser:chatId:completion:` method
checks user presence, OpenChat and Plus Friend eligibility, chooses access
permission, then dispatches a profile API. No timestamp admission check is
visible in that method. These are distinct from the regular MEMBER API.

Private provenance: `profile-refresh-cooldown-20261014` method report and
read-only decompilation, plus the `profileUpdatedAt` selector call index.
Static indexing cannot exclude indirect or alternate callers. Native background
invalidation, persistent MEMBER freshness timestamps and a global per-user
cooldown remain untraced. No native TTL or pacing parity is claimed.

## Implementation contract

Bridge policy for ordinary member nickname/avatar enrichment:

- Key each record by the observing Kakao account and target user, independent of
  room. Scope it to the bridge database; separate observers never share results.
- Persist only the nickname and thumbnail/full avatar URLs needed by Matrix,
  not access permits or other MEMBER credentials. Profile contents remain private
  operator data in the bridge database and are never logged.
- A successfully returned profile is reusable for 15 minutes from completion.
  Reuse it in any currently authorized ordinary room requesting that identity.
  Empty returned nickname/avatar fields are confirmed clears. An absent record
  is not a confirmed clear.
- Before a fetch, atomically reserve that user's next attempt for two minutes.
  Concurrent callers and new connector processes reuse an existing profile or
  leave an unresolved member without enrichment. They do not start duplicate
  fetches. Failed and missing responses retain the reservation, so restart loops
  cannot bypass the attempt cooldown.
- A successful result replaces only its own reservation, extends freshness to
  15 minutes and publishes the minimal profile. An obsolete completion cannot
  overwrite a newer reservation. Partial successful batches are retained.
- Stale profiles remain available during failed/missing attempts; the original
  freshness timestamp is not advanced by cache reads. After cooldown expiry the
  next refresh demand can request the user again.
- New requests still use the selected room's MEMBER endpoint and the existing
  global account scheduler and physical batching. No direct request bypass is
  introduced. Invalid storage or failed durable reservation stops enrichment
  before fetching. Cached profiles never establish a roster or authorize joins.

This intentionally trades up to 15 minutes of nickname/avatar freshness for
cross-room and cross-restart reuse. Full HTTP profile views, OpenChat identities,
roster refreshes and room display metadata are not cached by this feature.
There is no automatic timer for every stored user: existing room refresh and
membership demands consume the policy. The proposed asynchronous account refresh
worker and issue #372's local-deadline reconnect fix remain separate work; this
cache alone does not fix the five-read synchronous refresh budget.

## Verification

The production snapshot regression first reproduced 13 MEMBER calls across 12
rooms and a recreated connector, then passed with one call. Synthetic regression
coverage also closes and reopens the SQLite bridge database, recreates connectors
repeatedly without advancing freshness, advances time to expiry, races independent
SQL reservations, and completes an obsolete response after a newer refresh.
Other cases retain stale/partial profiles after failure or cancellation, retry
missing IDs only after cooldown, separate observing accounts, reject invalid or
unwritable storage and oversized JSON, and exclude unrequested users and access
permits. A second failing regression exposed membership-notice handling discarding
partial profile results; it now applies the completed enrichment without changing
the notice's member identities. These tests exercise production code and are not
native timing-parity fixtures. No owned-account live freshness test was performed
for this change.

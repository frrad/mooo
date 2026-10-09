# Regular-group membership and access lifecycle

Evidence date: 2026-10-09. Official Mac 26.8.0 arm64 tracing, executed
synthetic feed fixtures, production regressions and controlled owned A/B/C
encrypted-room acceptance. Remaining gaps are explicit below.

## Independently traced removal contract

The DELMEM transport handler delegates its typed notice to the manager. The
manager schedules a database block; its callback address was independently
resolved from the binary's stack-block setup. That block finds the room using
`chatLog.chatId` and compares `chatLog.feed.leaver.userId` with the current
account ID. If they match, it calls the room deletion method. Otherwise, a
present log is processed as a chat message with status 3, then the room removes
the leaver by user ID. The room deletion method deletes messages, chat metadata
and chat threads before calling the room-store deletion method. This establishes
a distinct self-removal path; it is not merely another peer profile update.

The previously traced LEFT block looks up its room, calls room deletion, updates
the last token ID and invokes a separate calendar path for TeamChat. The
member-removal helper inspects active member IDs and open-group/link state and
performs database-context work. Complete nested transaction/failure handling,
active-count updates, downstream UI effects, member-store deletion and omitted
history reconciliation remain gaps. See the older
[identity inventory](../membership-chat-change-inventory.md) for decoder models
and the bounded NEWMEM/DELMEM/LEFT provenance.

Private first-party reports: membership-live-removal, delmem-push-chain,
delmem-db-block, left-chain and left-block. Proprietary disassembly and
decompilation remain outside Git. The live removal callback differs from the
older omitted-history database consumer and must not be conflated with it.

## Bridge decisions and required verification

The existing connector converts peer join/removal notices and LEFT into Matrix
membership updates. Its member-delta path discarded the current account ID,
which incorrectly suppressed DELMEM self-removal. A failing production-path
regression now reproduces that suppression; the correction retains an explicit
self leave with the logged-in event sender. Matrix history retention rather than
deleting the Matrix room is a bridge decision, not official-client storage parity.

The initial access implementation now records a per-chat removal flag in portal
metadata before attempting the Matrix leave and verifies its database readback.
An in-memory block also remains if persistence fails. Self DELMEM uses an explicit
leave without fetching metadata using the departed account. Text/image sends,
reaction mutation/removal and read operations check access; event delivery refuses
content for a durably removed chat. Regression coverage reproduces outbound sends
after a failed Matrix leave and verifies the correction, stale-cache/reload
blocking and withholding later inbound content. The flag must not be manually cleared
as a substitute for verifying source rejoining.

The membership checkpoint now pauses a group before refresh, persists the fresh
full roster, and independently reads Matrix membership after framework handling.
Every source ghost must be joined; any ghost outside the source roster must be
neither invited nor joined. Only then does a saved/read-back checkpoint clear
the pause and a previous removal flag. Duplicate/nonpositive raw IDs, a missing
creating account, mismatched snapshots, failed Matrix updates and failed saves
leave access blocked. Delayed peer notices therefore select current membership
instead of overwriting a newer roster with their old join/leave delta.

Regular-group inventory refresh and live peer membership notices consume this
path. Direct-room deltas retain their prior path. Tests cover a departed ghost
remaining joined despite framework success, durable pending state after reload,
and source rejoining that cannot resume forwarding until all Matrix identities
converge. Completed inventory reconciliation now marks previously managed, bound groups
absent from the inventory as removed before refreshing present groups. It applies
and independently verifies the creating account's Matrix leave. Regressions
cover failed Matrix updates, durable removal after reload, and continued
source blocking after Matrix departure. Unknown legacy room types remain a gap:
absence alone is not used to infer that an unclassified portal was a regular
group. Double-puppet roster convergence is not covered by the owned test setup.

Owned acceptance described below covers peer departure offline and online,
explicit rejoining, bridge-account departure and rejoining, exact encrypted
traffic, native visibility, ordering and mapping stability. Final committed-head
restart verification, CI and squash merge remain pending. No separate forced
administrator-removal experiment was performed; its decoded removal path has
synthetic production coverage and first-party tracing, not observed parity.

## Corrected wire-to-model boundary

A controlled owned-account leave on 2026-10-09 exposed an acceptance failure:
the bridge rejected the membership notice and retained the previous Matrix
roster. No subsequent test content was sent; the bridge was stopped. A fresh
source roster confirmed the departure. One explicit invitation restored the
owned participant, and a separate planned leave was captured using the original
secondary profile. The observed `DELMEM` carries a type-zero `chatLog` with BSON
int64 chat/log IDs and a JSON string in `message`, containing `member.userId`,
`nickName`, `feedType`, `kicked`, `memorial` and `hidden`. It does not carry an
embedded `feed` document or `member.userType`.

Independent Mac tracing resolves `LocoChatLog.feed` through
`extractFeed:attachment:` for type zero, then `initWithJSONTextString:`. The
feed model's name mapping translates `member` to `leaver` and `members` to
`invitees`. The isolated Mac harness executed both synthetic leave and join
message inputs through these accessors and confirmed absent `userType` defaults
to zero. These executed cases are in
[message-feed.json](../fixtures/membership/message-feed.json). The earlier
embedded-feed decoder tests incorrectly treated model property names as wire
fields; they have been replaced with message-JSON inputs.

The production decoder now reads bounded JSON, requires the expected feed type,
keeps int64 member identities exact, and rejects duplicate fields, invalid IDs,
duplicate invitees and invalid numeric ranges. These strict rejection rules are
bridge admission decisions; complete official malformed-input permissiveness is
not claimed. A failing fixture test preceded the decoder correction. A second
failing regression reproduced delivery of later content after an undecodable
membership notice. Such `DELMEM`, `NEWMEM` and `LEFT` failures now terminate typed
admission, release the source owner and report `kakao-group-membership-invalid`
without automatically reconnecting. Reconnection must verify fresh membership.

The observed invitation response uses JSON `members`. A raw live `NEWMEM`
push capture and full official default/failure coverage remain gaps. The native
bridge-account invitation exercised live roster restoration without a new
ordinary message. Raw captures and proprietary reports remain private.

## Membership-only catch-up ceiling

Owned acceptance reproduced a separate restart failure after a membership-only
change, before any subsequent ordinary message. `SYNCMSG` returned one log in
the requested interval and a later type-zero leave feed beyond the login-derived
maximum. The client rejected the entire page as a protocol error. A fresh
ordinary message raised the ceiling and made the same catch-up path succeed;
that did not fix the membership-only case.

A failing production-path regression reproduces this overlap with synthetic
positions. Bounded catch-up now filters records beyond its selected maximum
without delivering or committing them. Verified full roster reconciliation runs
before catch-up, so ignoring an out-of-interval feed cannot restore stale member
access. The extra log remains eligible for a later bounded interval. This is an
explicit bridge boundary decision; the official Mac's treatment of a page
containing records beyond its requested maximum remains an untraced gap.

Local decoder and lifecycle checks passed before this ceiling correction. Owned
offline departure converged to two members before encrypted traffic: exact A
identity and decryption, one message each way on remaining native clients, and
four prior mappings unchanged. Rejoining restored all three ghosts; recovery
delivered the previously submitted C message once without resending, decrypted
correctly, and preserved six earlier mappings. All three native clients displayed
the post-rejoin messages once. A subsequent live C departure converged to two
members with C left on Matrix and no decoder error. The corrected membership-only restart reached connected state without another
ordinary message, kept the two-member checkpoint and C's Matrix leave, retained
one room binding, and left all nine prior message mappings unchanged. Full
`make check` and worktree secret scanning passed. Final committed-head acceptance, CI and merge remain pending.

## Bridge-account departure and Matrix permissions

Owned self departure persisted `SourceRemoved` before Matrix application and
reported `kakao-group-access-removed`. An encrypted Matrix test message submitted
after departure produced no source mapping; all nine prior mappings remained
unchanged. A source message was submitted once by the remaining participant.
The departed bridge account did not forward it while absent.

Acceptance exposed two Matrix framework interactions. A self ghost used as the
membership-event sender could be rejoined while removing its associated Matrix
user. Separately, the generic remote-event queue calls `MarkInPortal` before
processing the event, inviting that user even for a departure. Failing
production-path regressions preceded correction. Source departure now uses a
bounded direct Matrix membership path, removes the associated user first, and
always attempts the ghost's self leave even if that removal fails. It preserves
member profile fields and independently verifies both identities have left.
The ordinary admission queue is not used for departures.

A Matrix owner with power equal to the bot cannot be kicked by that bot. This
permission failure is retained as an actionable access-removal state, including
bootstrap failure; it never restores source access or reports convergence. The
owned Matrix test user voluntarily left to resolve this test-room permission
condition. A subsequent fresh restart reached connected state with both
identities left and source removal still durable, without inviting the user
back. One native invitation from the remaining participant restored the source
roster and cleared removal only after verified Matrix convergence. The post-rejoin native message decrypted exactly with the expected stable
Matrix ghost. Both remaining native clients displayed it and the encrypted
Matrix reply once. C remained left. All nine prior mapping rows were unchanged;
exactly two ordered mappings were added, with no mapping for the encrypted send
or native content submitted while the bridge account was absent. Final restart
verification remains pending.

Transport readiness preserves a removed or pending group's actionable state;
a successful connection does not hide per-chat forwarding blocks. Verified live
rejoining restores the ready state only when no other chat remains blocked.

# Regular group discovery

Evidence date: 2026-10-09. References: authorized KakaoTalk for Mac 26.8.0
arm64 static tracing and controlled owned Android 26.8.2 observations.

## Source contract

The Mac membership notice handler first persists the associated membership feed
message. It collects invitee identities and checks the locally stored room.
An unknown room, or a room requiring refresh, triggers `CHATINFO`, except for
feed type 4. An existing room with missing invitee profiles triggers `MEMBER`.
The significance and producer coverage of the refresh flag and feed exclusion
remain gaps; they are not generalized into a complete group lifecycle claim.

`CHATINFO` sends the selected `chatId`; its response supplies `chatInfo` and
optional `bmids`. The coordinator rejects a missing or non-success response.
For a successful linked-room response, it first checks whether the link record
exists and obtains missing link information. Ordinary room data proceeds through
a database queue to the shared room upsert, then replaces blinded-member IDs
only when provided. A completion runs afterward. Failure reports through the
completion rather than performing the successful room update. The request,
response mappings and common room/member upserts are described in
[chat-metadata.md](../chat-metadata.md).

Private analysis reports reviewed: group-discovery-newmem,
group-discovery-chatinfo-blocks and group-discovery-persistence. Callback
addresses, disassembly and decompilation remain outside Git. Guessed interior
addresses in the exploratory persistence report did not establish callbacks;
block addresses were independently resolved from the binary before review.
Remaining source gaps include complete downstream UI notification behavior,
all room-upsert revision/failure branches, and alternate native creation entry points. Login inventory deletion,
paging and EOF behavior are covered by [operator-chat-list.md](../operator-chat-list.md).

## Implementation decisions

The bridge requests a complete zero-token login inventory while preserving
committed message positions and LBK. Only ordinary `MultiChat` entries without
a link identity are candidates. It resolves fresh room metadata and a complete
source roster before enabling portal creation, then queues a resync keyed by
source chat ID and login ID. Duplicate inventory IDs are reduced to one event.
Incomplete inventory, failed metadata, unsupported room types, missing rosters
and unsuccessful Matrix handling stop startup before live subscription.

A newly received membership-add event may create a portal only after the same
source snapshot validation; a removal cannot create one. Existing portals retain
the ordinary membership refresh path. Discovery does not request historical
messages or explicitly mark them read. Existing catch-up retains its separately
documented source read-side-effect limitation.

Metadata is validated before queue admission because bridgev2 may fall back to
its network metadata API if an event provider fails during room creation. The
creation provider returns the validated snapshot, scoped to the exact portal.
Existing Boards revision checkpoints participate in startup snapshots so the
new path does not discard the announcement revision guard.

## Verification status

Production connector regressions reproduced missing startup discovery and
missing membership-event portal admission before implementation. Synthetic
coverage includes full inventory filtering, duplicate entries, preserved roster
identities without fabricated profiles, source title, foreign portal rejection,
and startup failure before live subscription. Scripted encrypted LOCO backend
recovery tests use the full inventory option and still exercise catch-up commit
boundaries, lease release and ambiguous-send behavior.

Owned A created separate regular groups containing A/B/C while the bridge was
stopped and connected. In each case the explicit Android new-room action first
produced a local draft: the selected title and a room-created-when-chat-begins
notice were visible, recipients had no room, and the completed secondary inventory
remained unchanged. One synthetic native first send created the server room.
Offline discovery created its Matrix portal on startup before any additional
native message; live creation produced one new portal while connected. Both had
the exact source title and all three stable Kakao identities. A fresh selected-room
`CHATINFO`/`MEMLIST`/`MEMBER` probe corroborated ordinary type, shared title type 3,
three unique roster IDs and two non-self profiles. The sanitized observed fixture
projects these responses into the production discovery path.

Both portals passed Megolm-encrypted A/C inbound decryption with distinct correct
senders and encrypted Matrix outbound text visible exactly once on A/B/C. Normal
restart preserved portal IDs, message-part IDs and retained Matrix device keys.
A C message sent while stopped arrived once after restart; old and new encrypted
messages decrypted. Discovery itself did not send a bootstrap or backfill the
previously uncommitted offline setup message. Historical backfill remains separate.
These observations use the existing owned secondary profile, without cloning or
new enrollment. Synthetic messages, private receipts and selected-room evidence
were retained outside Git; raw event captures and runtime logs are transient.

The first-message boundary is an Android UI observation, not a universal protocol
requirement. Mac `sendTextMessage:` prepares a room when none exists, with the
original send captured for completion; `prepareSendMessage:` routes ordinary rooms
to `createChatRoom:`. Draft group-title/profile setters retain pending values.
Mac also has an independent `prepareChatRoom:` preparation entry point and a
LiveTalk creation caller. Their complete UI and callback/failure chains remain
untraced. No claim is made that generic `CREATE` requires an ordinary message,
or that these alternate entry points, linked rooms, Secret Chat, arbitrary group
sizes, membership removal or every source failure path have passed acceptance.

Private source reports additionally reviewed: group-discovery-create-trigger,
group-discovery-draft-send and group-discovery-draft-callers. Live logs did not
provide a trustworthy event-type discriminator for the online creation trigger;
NEWMEM admission is covered by the production-event regression, while the live
observation proves portal creation and convergence for the native workflow.

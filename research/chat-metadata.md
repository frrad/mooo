# Chat metadata parity: chat info, member lists, and member profiles

Status: first-party static trace, macOS KakaoTalk 26.8.0 arm64, reviewed
2026-09-30. Bridge milestone B2, first dossier. No live probe was performed.

This dossier records behavior recovered from the authorized official Mac client
with Ghidra, following the seven-layer parity definition in
[`protocol-parity.md`](protocol-parity.md). It is implementation-neutral: no
decompiler output, addresses, account values, or proprietary assets are
included. Friend/contact synchronization is a separate HTTP surface and is not
covered here; see [Explicit gaps](#explicit-gaps).

## Summary

| Command | Request | Response | Official callers | Persistence |
| --- | --- | --- | --- | --- |
| `CHATINFO` | `chatId` | `chatInfo` (chat data), `bmids` | NEWMEM, sync-join, message-push fallback, notification actions, several UI paths | Shared room upsert used by login-list chat data, then blinded-member IDs |
| `MEMBER` | `chatId`, `memberIds` | `chatId`, `members` | NEWMEM, room upsert, unknown-member merge, friend refresh, chatbot paths | Per-member user upsert; optional room-member add |
| `MEMLIST` | `chatId`, `token` | `token`, `type`, `memberIds` | Member-list UI only | Room member-list ID set; missing profiles fetched through `MEMBER` |
| `GETMEM` | `chatId` | `members`, `token` | None found | Not reachable in this build |

`MEMBER` is the profile-resolution primitive. `CHATINFO` is the room-metadata
primitive. `MEMLIST` is a UI-initiated roster refresh that delegates profile
resolution to `MEMBER`. The `GETMEM` request and response models ship in the
binary, but its only coordinator has no caller, so it is not part of Mac parity.

## Model decoding rules

All models in this dossier derive from one base JSON model. The traced decoder
behaves as follows:

1. The response body is converted to a dictionary.
2. A model may declare a wire-key-to-property mapping. For each mapped wire key
   present in the dictionary, a value that is not null is copied to the property
   name and the wire key is removed. A null value is removed without being
   copied, so null is equivalent to absence.
3. Every declared property then reads the dictionary key equal to its property
   name. An absent or null value leaves the property at its zero value (0, false,
   or nil). A present value is assigned with key-value coding. Nested model
   properties and typed number-array properties are constructed recursively from
   the value.
4. A nil top-level dictionary raises an exception in the official client.
   Wrong-type values reach key-value coding without a type check. The official
   client's behavior on malformed input is therefore an exception or a coerced
   value, not a structured error.

Implementation decision: the clean-room decoder fails closed with a protocol
error on wrong wire types and on a missing required identity field. It does not
emulate the official coercions. Null and absence are treated identically, as the
official client does.

The response base class exposes `status`, `errMsg`, `errUrl`, and `errUrlLabel`.
`isSuccess` is exactly `status == 0`. The traced completions in this dossier
check `isSuccess` or `status == 0` only. None of them treat `-310` (partial
success) as success.

Requests serialize their declared properties under their property names. None
of the three request models here declares a wire-key mapping.

## `CHATINFO`

### Request and response

| Request field | Type |
| --- | --- |
| `chatId` | int64 |

| Response wire key | Property | Type |
| --- | --- | --- |
| `chatInfo` | chat data | chat-data object (below) |
| `bmids` | blinded member IDs | array |

### Chat-data object

The same chat-data model is used by `LOGINLIST`/`LCHATLIST` `chatDatas` entries.
Its wire-key mapping:

| Wire key | Property | Type |
| --- | --- | --- |
| `c` | `chatId` | int64 |
| `t` | `type` | string |
| `a`, `activeMembersCount` | `activeMemberCount` | int32 |
| `n` | `newMessageCount` | int32 |
| `s` | `lastSeenLogId` | int64 |
| `ll` | `lastServerLogId` | int64 |
| `l` | `lastChatLog` | chat-log object |
| `i` | `displayUserIds` | int64 array |
| `k` | `displayNicknames` | string array |
| `p` | `pushAlert` | boolean |
| `m` | `meta` | room-meta object |
| `chatMetas` | `chatMetas` | array of chat-meta objects |
| `mmr` | `metaMaxRevision` | int64 |
| `jn` | `joinedAtForNewMem` | int32 |
| `ii` | `inviterId` | int64 |
| `li` | `linkId` | int64 |
| `otk` | `linkToken` | int32 |
| `bmids` | `blindMemberIds` | array |

The chat-data initializer adds a pre-mapping step that the table above does not
show:

- It resolves a room-type enum from the raw dictionary key `type` before the
  mapping runs. It does not read `t` for this step.
- If a `displayMembers` array is present, it derives `displayUserIds` from each
  element's `userId`, `displayNicknames` from each `nickName`, and `suspicions`
  from each `suspicion`. When there is exactly one display member, it also
  derives `displayImageUrls` from `pi` for room types `OD` and `OM`, and from
  `profileImageUrl` for every other type. `displayMembers` is then removed.
- If `chatMetas` is present, it is decoded as an array of the chat-meta model
  (`type` int32, `revision` int64, `authorId` int64, `content` string,
  `updatedAt` int64).

The room-type strings map in this order: `DirectChat`, `MultiChat`, `PlusChat`,
`OD` (open direct), `OM` (open group), `MemoChat`, `PartnerChat`.

The room-meta object `m` has `name`, `imageUrl`, `fullImageUrl`,
`chat_category`, and the booleans `favorite` and `chat_hide`. Those two booleans
accept the string `"true"` as well as a boolean. `m` may also arrive as a JSON
string, which the initializer parses.

Observed fact versus open question: the initializer consults `type` while the
mapping populates `type` from `t`. The binary cannot tell which key the server
sends in a `CHATINFO` response. The answer affects only the display-image key
choice for single-member rooms. This is recorded as a live-confirmation
question, not an inference.

### Orchestration and persistence

1. The coordinator sends `CHATINFO` and constructs the response model from the
   reply packet. A missing packet yields a nil response.
2. If the response is nil or `status != 0`, the client logs the failure and
   invokes the caller's completion with the response. Nothing is persisted.
3. On success, if `chatData.linkId > 0`, a synchronous database block checks
   whether the OpenChat link is stored locally.
   - If the link is missing, the client first issues `INFOLINK` for that one
     link ID. If `INFOLINK` succeeds, the chat data is applied to the room
     store and the completion runs. **If `INFOLINK` fails, neither the chat data
     nor the completion is applied or invoked.** On this deferred path, the
     `bmids` value is not applied.
   - If the link is present, processing continues as below.
4. Otherwise, an asynchronous database block upserts the room from
   `chatData` through the same room-construction routine used by the
   login-list chat-data handler. If both the room and the response `bmids` are
   non-nil, it replaces the room's blinded-member IDs. The caller's completion
   then runs on the main thread with the response.

This is the same deferral pattern that the login-list path uses for OpenChat
links (see [`message-continuity.md`](message-continuity.md) and
[`protocol-parity.md`](protocol-parity.md)).

## `MEMBER`

### Request and response

| Request field | Type |
| --- | --- |
| `chatId` | int64 |
| `memberIds` | int64 array |

| Response field | Type |
| --- | --- |
| `chatId` | int64 |
| `members` | array of member objects |

### Member object

| Wire key | Property | Type |
| --- | --- | --- |
| `userId` | `userId` | int64 |
| `nickName` | `nickName` | string |
| `pi` | `profileImageUrl` | string |
| `fpi` | `fullProfileImageUrl` | string |
| `opi` | `originalProfileImageUrl` | string |
| `type` | `type` | int32 |
| `ut` | `userType` | int32 |
| `accountId` | `accountId` | int32 |
| `linkedServices` | `linkedServices` | string |
| `statusMessage` | `statusMessage` | string |
| `countryIso` | `countryIso` | string |
| `suspended` | `suspended` | boolean |
| `memorial` | `memorial` | boolean |
| `accessPermit` | `accessPermit` | string |
| `suspicion` | `suspicion` | string |
| `pli` | `profilelinkId` | int64 |
| `mt` | `openLinkUserMemberType` | int32 |
| `pfId` | `channelId` | int64 |

This is not the model used by `NEWMEM` invitees and `DELMEM` leavers. Those
feed entries use a separate three-field feed-member model with no wire-key
mapping: `userId` (int64), `userType` (int32), and `nickName` (string). A feed
member's user type is therefore keyed `userType`, while a `MEMBER` profile's is
keyed `ut`. Decoders must not share one member parser across the two.

### Orchestration

The public coordinator takes a chat ID, a link ID, a member-ID list, and an
`addChatRoomMember` flag. A three-argument convenience form exists.

1. A nil member list completes immediately without a request.
2. The list is filtered. IDs less than 1 and the logged-in user's own ID are
   removed. If nothing remains, the completion runs without a request.
3. The remaining IDs are sent in batches of at most 500.
4. On a successful batch, a synchronous database block persists the batch (see
   below). The next batch is then requested with the remaining IDs. The
   caller's completion runs on the main thread after the final batch.
5. On a nil response or `status != 0`, the client logs the failure and runs the
   completion with failure. Later batches are abandoned. It does not retry.

### Persistence

For every returned member, the client upserts a user record keyed by
`(userId, linkId)` from the member object. When `addChatRoomMember` is set, it
looks up the room by the **response** `chatId`. If that room exists, it adds
the members to the room.

The user update copies `userType`, `nickName`, `profileImageUrl`,
`fullProfileImageUrl`, `statusMessage`, `accountId`, `suspended`, `countryIso`,
and `memorial`. It also copies `channelId` into the user's extension data when
non-zero, `accessPermit` only when non-empty, and a suspicion classification
derived from `suspicion`. It then recomputes the display name.

For OpenChat users (`linkId > 0`), the friend type is forced from the member
`type`: 9 stays 9, and anything else becomes an OpenChat-profile sentinel. The
profile link ID and OpenChat member type are also recorded. For ordinary users,
member `type` 9 interacts with the stored friend type. The exact state table is
an open question.

Adding members to a room skips members whose `type` is 9. For the rest, it
initializes per-member read watermarks from the room's last log ID. It tops up
the room's display-member list when that list holds fewer than 20 IDs. In an
open group chat it may issue `INFOLINK` for the room's link when a
host-type member (OpenChat member type 8) is added.

## `MEMLIST`

| Request field | Type |
| --- | --- |
| `chatId` | int64 |
| `token` | int64 (the room's stored member-list token) |

| Response field | Type |
| --- | --- |
| `token` | int64 |
| `type` | string |
| `memberIds` | array |

The coordinator reads the stored room's token and sends `MEMLIST`. The only
callers are two member-list views, so it is UI-initiated, not part of login or
push handling.

On `status != 0`, the completion reports failure. On success with non-empty
`memberIds`, the client collects the IDs that have no local user record. If
there are none, it stores `memberIds` as the room's member-list ID set, a set
separate from the room's member objects, and completes successfully. Otherwise
it calls `MEMBER` for the unknown IDs, stores the member-list ID set, and
reports the `MEMBER` outcome. The traced blocks do not persist the response
`token` or `type`.

## Membership push follow-ups

The `NEWMEM` manager path (see
[`membership-chat-change-inventory.md`](membership-chat-change-inventory.md))
collects the invitees' `userId`s and looks up the room by `chatLog.chatId`.
Depending on a database-side room check and the feed type, it may issue
`CHATINFO` for the chat. It then issues `MEMBER` for a filtered subset of the
invitee IDs, using the room's link ID. A feed-type-4 branch skips the
`CHATINFO` step in the traced flow. The exact room-existence and filter
predicates are not yet resolved.

`CHATINFO` is also issued by the sync-join push handler, by a message-push
fallback that fetches room information for an unknown chat, and by several UI
and notification-action paths.

## Implications for the clean-room client and bridge

Implementation decisions that follow from the traced behavior:

- Expose `ChatInfo(chatID)` and `Members(chatID, userIDs)` as explicit,
  non-retrying client calls. `Members` applies the official filtering: drop
  IDs below 1 and the account's own ID, and send nothing when the list is
  empty. It batches at 500 and stops at the first failed batch.
- Treat only `status == 0` as success for these three commands.
- Decode the shared chat-data object once, reusing it for `LOGINLIST`
  `chatDatas`, and preserve `displayMembers` derivation and room-type strings.
- Keep the OpenChat `INFOLINK` deferral out of the first API. The bridge's
  first scope is direct and ordinary group chats. A positive `linkId` is
  surfaced to the caller rather than silently resolved.
- Portal name and avatar: derive from `meta.name`/`meta.imageUrl` when present.
  Otherwise use the display-member nicknames and image. Ghost profiles come
  from `MEMBER` member objects.

## Explicit gaps

- **Live encoding:** whether `CHATINFO` sends the room type as `t` or `type`,
  whether it includes `displayMembers` or the flattened `i`/`k` arrays, the
  integer widths the server chooses, and whether `bmids` is present for
  ordinary rooms. One bounded owned-account probe can answer these.
- **Room upsert internals:** the shared room-construction routine used by
  `CHATINFO` and the login-list handler is not traced field by field here.
  Merge, replacement, and member-set effects remain open.
- **User upsert creation path:** creation versus update of a new user record,
  and the ordinary-user friend-type state table for member `type` 9.
- **NEWMEM predicates:** the room-existence flag, feed-type-4 branch, and
  invitee filter.
- **MEMLIST token semantics:** where the room's member-list token is written,
  and whether an unchanged token yields an empty `memberIds`.
- **Friend/contact synchronization:** the HTTP full and token-based friend sync
  coordinators are identified but not traced.
- **Malformed input:** the official client raises an exception or coerces. The
  clean-room fail-closed behavior is an implementation decision, not parity.

## Evidence trail

- Client: official macOS KakaoTalk 26.8.0 arm64, inspected 2026-09-30.
- Method: read-only Ghidra class metadata (ivars, properties, method lists,
  superclasses), constant wire-key dictionaries and command-name strings,
  annotated disassembly of coordinators, request senders, completion blocks,
  database blocks, and model initializers. The bundled JSON-model framework's
  initializer was disassembled for the generic decoding rules.
- No account, message, network, or live-server action was performed.
- Public transfer: command names, field names, wire keys, types, ownership
  boundaries, and control-flow conclusions only. Private addresses, decompiler
  output, build paths, and helper output remain in the external lab directory.

## Observed regular-group shared name (2026-10-09)

A controlled three-owned-account Android 26.8.2 group showed the chosen shared
name on every native client. CHATINFO for the bridged participant returned
MultiChat, absent `m`, and a type-3 `chatMetas` entry with matching plain-string
content. The connector previously fell back to display nicknames and now uses
that shared name. Full name-consumer/persistence/notification parity remains
untraced; see [the group validation](bridge/GROUP-MESSAGING-VALIDATION.md).

## Android shared metadata enum (2026-10-09)

Static inspection of the owned official Android 26.8.2 APK (version code
29260820), classes10.dex, recovered the enum in `ChatSharedMeta.kt`
(obfuscated class `awa`, nested enum `a`). Its constructor stores an explicit
integer `type`; `getType` returns that value. These are wire values, not enum
ordinals:

| Official enum member | Wire value |
| --- | ---: |
| None | 0 |
| Notice | 1 |
| KakaoGroup | 2 |
| Title | 3 |
| Profile | 4 |
| Tv | 5 |
| Privilege | 6 |
| TvLive | 7 |
| PlustChatBackground | 8 |
| DailyCard | 9 |
| DailyCardProfile | 10 |
| OpenLinkChannelChat | 13 |
| OpenLinkBotCommand | 14 |
| Warehouse | 15 |
| Voiceroom | 16 |
| VoiceroomCount | 17 |
| Cecall | 18 |
| CecallCount | 19 |
| OpenLinkChatBackground | 20 |
| ChatBot | 21 |
| WebBanner | 22 |

The JSON constructor reads `type` and resolves it by comparing the enum's
stored wire values; unknown values resolve to None in that client. The shared
metadata factory dispatches Title to `ChatTitleMeta.kt` (obfuscated `ybb`).
Its title projection reads the inherited content, leaving the title unset for
empty content and otherwise returning that content directly. This confirms the
name and plain-string interpretation of the type-3 entry independently of the
previous native/wire observation. Confidence is high for this enum and model
chain; revision precedence, personal-name precedence, downstream database/UI
consumers, and notification behavior are not established by this trace.

Production names the consumed value `chatmeta.SharedMetaTitle`; unused enum
members remain research facts until a consuming feature needs them. The wire
model continues preserving unknown integer types rather than discarding them.
No decompiled implementation is included in the repository.

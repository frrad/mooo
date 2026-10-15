# Metadata request triggers

Status: static trace of the official macOS KakaoTalk 26.8.0 arm64 client
(build 2000) plus one owned-account live observation, 2026-10-14. This document
replaces bridge-invented polling with triggers derived from the official client.
It covers every Kakao request the bridge sends without a direct Matrix-user
action: LOGINLIST/LCHATLIST, CHATINFO, MEMLIST, MEMBER, GETMOMETA, GETMCMETA,
CHATONROOM and the HTTP profile calls under `/talk/profile25/`.

## Method

- **Static.** Read-only Ghidra queries on the authorized binary. For each
  request, every call site of its coordinator and sender selectors was resolved
  to its owning class and method. Call sites inside blocks were followed back to
  the method that builds the block. Gating methods were then read in full:
  login-list completion, LCHATLIST pagination, GETMCMETA admission,
  room upsert, message conversion, CHATONROOM completion, CHGMETA and
  CHGMOMETAS consumers, SYNCEVENT and SYNCACTION handlers, and the main timer
  controller. Private reports are `ghidra/parity/triggers-20261014-a` through
  `-g` and `ghidra/queries/triggers-20261014`.
- **Live.** The standard bridge, logged in as lab account B, ran with a private
  packet-observer overlay that records the method, packet ID, status and
  top-level key names (never values) of every inbound LOCO packet. Lab account A
  then changed their nickname and avatar in the official Android 26.8.2 client.
  See [Live observation](#live-observation-2026-10-14).

Limits of the static method. Selector-stub indexing finds every Objective-C
message send, including those from Swift code to `@objc` methods. It cannot see
pure-Swift calls, `performSelector:` with a computed selector, or notification
dispatch, so those are listed as gaps where they matter. A call site counts as
UI-only when every path to it starts in a view or window controller or in a
user action handler.

Classification:

- **(a) login/reconnect**: the LOGINLIST/LCHATLIST chain or the reconnect path.
- **(b) push**: handling of an inbound LOCO push.
- **(c) UI**: a user action or a visible view in the official client.
- **(t) timer**: a native timer.

## Trigger table

| Request | Native triggers (Mac 26.8.0) | Class | Bridge today | Verdict |
| --- | --- | --- | --- | --- |
| LOGINLIST | `login:completion:` at first login and from the reconnect retry (`recoverLocoLogin:`) | a | Every connect and reconnect | Parity |
| LCHATLIST | LOGINLIST completion pages until `eof` | a | Pages until the end (cap 20) | Parity |
| GETMETA (shared room meta) | After the last LCHATLIST page, for rooms whose chat data `mmr` exceeds the stored room revision | a | Not sent; CHATINFO is used instead | Out of scope here; see gaps |
| GETMCMETA | After the last LCHATLIST page, only when the login-list `mcmRevision` exceeds the locally stored revision. Also driven by the CHGMCMETA push consumer, which merges the push payload | a, b | Queue task: snapshot without personal meta (15-min freshness), CHGMCMETA (forced), minute tick | **Deviation**: replace the time gate with the login-list revision gate; drop from the tick |
| CHATINFO | NEWMEM; SYNCJOIN; MSG for a room the client does not know; HINT for an unknown room; notification click; deep-link scheme handlers; VoIP and scheduled-message UI | b, c | Portal creation/resync, CHGCHATST/CHGMETA resync, NEWMEM/DELMEM, invites, backfill, group create, room-info queue task | Push-driven uses match; no native timer exists |
| MEMLIST | `doChatMemberList` ← `getChatMemberList` ← member-list views (join-option and multi-profile designation views) only | c | Every new group's first snapshot; a membership notice or catch-up membership feed (event-driven, same path); a Matrix invite; fallback for a managed group whose catch-up interval was not recovered | Deviation only on events and fallbacks: no reconnect sweep since 2026-10-15 |
| MEMBER | NEWMEM invitees; a message whose author has no local user record or an empty nickname (message conversion, any source: push, SYNCMSG, MCHATLOGS); CHATONROOM response `unknownMemberIds` and missing access permits (chat open); MEMLIST unknown IDs (member list); ADDMEM/JOINLINK completions; CHGMETA chatbot list; PlusChat display-member creation | b, c | Snapshot enqueue, NEWMEM/DELMEM, **minute tick** (15-min per-user freshness) | **Deviation**: native has no time-based MEMBER; the tick and freshness window are bridge-invented |
| GETMOMETA | CHATONROOM completion when the response carries Boards revisions (`doGetMoimMeta` compares them), and OpenChat JOINLINK | c | Every snapshot, every reconnect (15-min freshness), CHGMOMETAS (forced), minute tick | **Deviation**: native merges the CHGMOMETAS payload and never polls |
| CHATONROOM | The response carries member IDs (`a`), read watermarks (`w`) and Boards revisions. Sent on opening a chat window (`FCChattingController chatOnRoom:`: create, join, rejoin, open Plus chat, first send), voice/face call setup; on reconnect (`locoClientDidRecover`) only for **currently open** chat windows | c, a | Startup sweep over every bridged portal; Matrix viewing heartbeat; reaction-sync command | **Deviation**: the startup sweep has no native counterpart; the viewing heartbeat matches chat open |
| `/talk/profile25/other` | Profile view (`FSProfileHomeViewController updateUserAndNotify:`), add-friend module, deep link, blocked-channel menu | c | Not sent | Parity (not sent) |
| `/talk/profile25/me`, `/me/refresh` | Own profile view and profile edits | c | Not sent | Parity (not sent) |
| HTTP friend sync (`/talk/friends/sync`) | **Native timer**: a 60-second `FCTimerController` timer calls `checkUpdateFriends`; when more than 600 s have passed since the last update it schedules a friend sync after a random delay, skipped while the Mac is idle, its screen is covered or it is asleep | t | Not sent | Gap: the only native periodic profile source, and it covers friends only |

### What the trace rules out

- **No regular-user profile push.** The client's push delegate
  (`locoManager:didReceive…PushNotice:`) has 36 handlers: BlockSync,
  ChangeChatStatus, ChangeSvr, ChgLogMeta, ChgMCMeta, ChgMeta, ChgMoMeta,
  DecUnread, DelMem, Feed, Hint, Invoice, KickOut, Kicked, Left, LinkDeleted,
  LinkKicked, LinkUpdated, MInvoice, Msg, NewMem, Spush, SyncAction,
  SyncBlind, SyncCreate, SyncDeleteMessage, SyncEvent, SyncJoin, SyncLinkCr,
  SyncLinkDl, SyncLinkPf, SyncLinkUp, SyncMemType, SyncModifyMessage,
  SyncRewrite and SyncSafeBot. Only SyncLinkPf concerns a profile, and it is an
  OpenChat link profile. Spush and Hint were traced earlier
  ([group member profiles](GROUP-MEMBER-PROFILES.md)).
- **SYNCEVENT** posts one notification carrying chat ID, author, event type,
  count, log ID and room type. Its only observer is the chat window, which runs
  the OpenChat reaction animation. It triggers no request.
- **SYNCACTION** is forwarded to the core delegate, which updates the typing
  indicator for the chat window. It triggers no request.
- **No MEMBER, MEMLIST, CHATINFO, GETMOMETA or GETMCMETA timer.** No caller of
  these coordinators is reachable from a timer, and the main timer controller's
  60-second tick only performs the friend-sync check.
- **CHGMOMETAS and CHGMCMETA carry their own payloads.** The consumers merge
  the push into the stored model (`updateWithChgMoimMetaPushNotice:`,
  `updateWithChgMCMetaPushNotice:`). Neither fetches.
- **CHGMETA → MEMBER** is limited to the chatbot-list meta type: it fetches
  chatbot user IDs that have no local record.

## Live observation (2026-10-14)

Setup: lab accounts A and B, one existing three-member regular group with
both, local Synapse. The bridge was built from `main` at 58ace6de (PR #376)
plus a private packet-observer overlay that is not in the repository. B's
bridge started at 23:08:09 UTC and reached CONNECTED after its startup reads.
A used the official Android 26.8.2 client. Each action had a private receipt
written before the tap.

| Time (UTC) | Action on A | Inbound packets at B in the following window |
| --- | --- | --- |
| 23:09:19 | Nickname changed and profile saved | None for 3 min 12 s. One PING reply (the bridge's own keepalive). |
| 23:12:34 | Avatar set to a synthetic striped image and saved | None for 3 min 6 s. One PING reply. |
| until 23:24:40 | — | Only PING replies, every 3 minutes. |

At 23:24:40 the bridge's minute tick reached the group after the 15-minute
profile freshness from its startup MEMBER read (23:08:42) expired. It sent
MEMBER, then GETMCMETA and GETMOMETA. Only then did the Matrix ghost show the
new nickname and an avatar. No push of any kind reached B because of A's
edits. This matches the static result: nothing pushes a regular member's
profile change to other participants. A native Mac client in B's place would
show the old nickname and avatar until B opened A's profile, or until a
friend sync reported A (if A is B's friend; this run did not check the
friendship).

Not tested: a message from A after the change. The trace says native would
not refetch A then, because A already has a local record with a nickname.

## Implications for the bridge

The native client refreshes a regular member's nickname and avatar only when:

1. the member joins (NEWMEM),
2. a message from them arrives and the client has no record of them, or has a
   record with an empty nickname,
3. the user opens the chat window (CHATONROOM reports unknown members),
4. the user opens a member list (MEMLIST, then MEMBER for unknown IDs),
5. the user opens the member's profile (HTTP `/talk/profile25/other`), or
6. the member is a friend and the 10-minute friend sync reports them changed.

Paths 2 to 4 only fetch members the client does not know yet; none refetches a
known member. A known non-friend member who changes their nickname therefore
stays stale on the native Mac client until the user opens their profile view.

Mapping each native trigger to the bridge:

| Native trigger | Matrix moment | Bridge action |
| --- | --- | --- |
| Login-list completion | Connect/reconnect | LOGINLIST/LCHATLIST; GETMCMETA only when `mcmRevision` rose |
| NEWMEM, DELMEM, CHGMETA, CHGMCMETA, CHGMOMETAS, SYNCJOIN | Push arrival | As today; apply CHGMOMETAS/CHGMCMETA payloads instead of refetching |
| Message from an unknown author | Message bridging | MEMBER for that author only, also when its stored nickname is empty (at most once per 2-minute attempt cooldown) |
| Chat window open | Matrix viewing heartbeat (`HandleMatrixViewingChat`) | CHATONROOM, then MEMBER for unknown members and GETMOMETA when revisions changed |
| Member list open | No Matrix equivalent | Not sent; the roster comes from login chat data and membership pushes |
| Profile view | No Matrix equivalent | Not sent |
| Friend sync timer | None | Not implemented; candidate source for friend profile freshness |

## Gaps

- **Idle official Mac capture.** Not performed. No official Mac session is
  logged in, and recent fresh secondary logins on the lab accounts hit Kakao's
  subdevice-login restriction. A new login only to observe idle traffic was not
  justified. The static trace found one native timer that sends anything
  (friend sync); live confirmation of its 600-second period remains open.
- **Friend-sync payload.** The success block parses three arrays and upserts
  friend records in one database block. Which profile fields it overwrites,
  and whether non-friend chat members appear, is untraced. The path link
  between the Brewer `updateSyncWithToken` call and `/talk/friends/sync` is a
  string reference in the same Swift type, medium confidence.
- **Pure-Swift and computed-selector callers** are invisible to the index.
- **Room-type constants.** The PlusChat condition on room upsert reads room
  type 2 under the room-type order recorded in
  [chat metadata](../chat-metadata.md).
- **GETMETA** (shared room meta, revision-gated at login) is the native path for
  room names and images. The bridge uses CHATINFO instead; that substitution is
  outside this document.

## Offline membership through catch-up (2026-10-15)

Owned observation, Android 26.8.2 primaries with the bridge (lab account B)
stopped. Lab account C left a three-member group, then A re-invited C. On
restart, the group's LOGINLIST entry had `ll` past the committed cursor but no
`l` (last chat log). A private probe running the client's catch-up up to `ll`
without committing returned the membership changes as type-0 chat logs:

| Change | feedType | Message keys | Chat-log fields |
| --- | --- | --- | --- |
| Leave | 2 | `member` {`userId`, `nickName`}, `kicked`, `hidden`, `memorial` | `logId`, `prevId`, `authorId`, `sendAt` |
| Invite | 1 | `inviter` {`userId`, `nickName`}, `members` [{`userId`, `nickName`}], `byLink` | same |

These are the shapes the NEWMEM/DELMEM push decoder already reads. The bridge
previously decoded them as gaps and never caught up a chat with only `ll`, so
the startup MEMLIST was its only source for offline membership. Now:

- A login chat entry without `l` targets catch-up at `ll`, as the Mac client
  marks a loss from `lastServerLogId` in that case.
- Feed types 1 and 2 decode as `MemberAdded`/`MemberRemoved`, positioned at
  their log, and take the push path (authoritative refresh and Matrix
  convergence). The applied feed is committed.
- Reconnect discovery uses a managed group's stored verified roster instead of
  MEMLIST. MEMLIST remains for a new group, and as the fallback when catch-up
  reports an unrecovered gap or the profile never delivered from the chat.

Live acceptance with this change. Two offline leave/invite cycles were
pending, and the previous build's startup MEMLIST had already removed C from
the Matrix room. The restart caught the group up with SYNCMSG and applied four
membership feeds, each with one CHATINFO + MEMLIST, as a push would. It sent
no roster read for the other five groups, and C rejoined the Matrix room. A
further plain restart sent only LOGINLIST before connecting: no SYNCMSG and no
MEMLIST.

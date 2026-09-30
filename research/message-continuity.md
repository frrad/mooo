# Message continuity and recovery

Status: durable resume and synthetic recovery implemented; one bounded live
`SYNCMSG` recovery succeeded, while full offline/restart validation remains.

## First-party observations

The authorized KakaoTalk macOS 26.8.0 binary was inspected before implementation.
Raw disassembly, decompiler output, addresses, and proprietary artifacts remain in
the private lab workspace.

The current client has a message-recovery operation with these observable
properties:

- the carriage command is `SYNCMSG`;
- its request constructor takes `chatId`, `cur`, `max`, and `cnt` in that order;
- the wire widths are int64 for the three identifiers/cursors and int32 for the
  count;
- the normal page limit is 300;
- a successful response exposes an ordered `chatLogs` collection;
- a successful empty page may omit or null `chatLogs` rather than sending an empty array;
- the client distinguishes a room's `lastLogId` from `lastSyncLogId`; and
- failure to make progress toward the known maximum creates explicit missing-
  history ("loss mark") state rather than silently advancing the synchronized
  cursor.

This confirms that non-consecutive log IDs alone are not evidence of a gap. A gap
exists when the client has a known target maximum and bounded `SYNCMSG` recovery
cannot make progress to it.

Confidence is high for the command, request shape and widths, response collection,
page limit, and no-progress behavior (static binary analysis, 2026-09-29). Cursor
inclusivity and server retention/error boundaries still require a controlled live
experiment.

A targeted follow-up of the official `handleLChatListResponse:` path confirmed
the surrounding state semantics that the first narrow pass missed. On successful
or partially successful pages, the Mac client separately enumerates `delChatIds`
and `chatDatas`; it applies those records to existing state and updates the token
cursors only on a status-zero end-of-list. A partial-success page terminates the
list phase without committing global cursors. It does not treat each page as a
replacement inventory.
The `SYNCMSG` success callback likewise enumerates the response collection using
normal Objective-C messaging, for which a nil collection naturally produces zero
iterations. These observations independently support merging delta chat pages and
treating absent/null `chatLogs` as an empty page.

A deeper first-party pass located the official loss-mark storage transitions.
The Mac client creates a synthetic stored marker at a log boundary after checking
the neighboring normal messages and existing markers. Successful synchronization
can remove one marker by exact log ID or remove markers across a bounded range;
the range path clamps its lower bound to the room's minimum retained log ID before
querying and deleting the matching objects. The surrounding `SYNCMSG` callbacks
also perform post-sync thread updates and may request member metadata. This proves
that missing history is durable room state in the official client, not merely a
transient error return. Exact marker-message presentation and every callback's
range-bound source remain implementation details under review.

## Implementation contract

`Client.Open` creates a separate version-3 checkpoint beside the selected private
authentication state. It is owner-only, rejects unknown fields and future
versions, migrates version 2 atomically, and uses synced temporary-file replacement
plus directory syncing. The existing per-profile lease covers both files.

The checkpoint contains:

- the chat-list token and blind-token cursor returned by a fully completed login;
- a sorted inventory of the latest server-observed chat recovery targets; 
- a sorted `(chatId, maxLogId)` boundary for application-committed messages;
- a sorted inclusive unresolved-history interval per chat; and
- clean versus interrupted shutdown state.

Observed targets and committed maxima are deliberately distinct. A target says
which history the client still needs to reach; it is never sent to Kakao as an
acknowledgement until the application explicitly commits that event. A full
login replaces the known-chat inventory, while a token-based delta login merges
the returned chats and removals into it. This distinction is required because a
live delta login returned only changed chat documents: treating that response as
a complete inventory made an unchanged direct chat disappear across processes.
The regression test reopens the checkpoint, installs an empty delta response,
and proves both that the target survives and that it does not leak into
`LOGINLIST maxIds` before commit.

The next process feeds those positional chat/max pairs and global cursors into
`LOGINLIST`. A socket is never serialized and QR authorization is never repeated.
During login, page deltas accumulate independently from the global cursor. A
status-zero EOF atomically makes both eligible for checkpoint installation;
`LCHATLIST -310` and accepted `LOGINLIST -305` retain their useful inventory
deltas while leaving the previous global cursor intact.

`Client.Events` suppresses messages at or below the committed boundary and exact
duplicates observed within the current process. Merely receiving an event does not
advance the checkpoint. The application calls `Client.CommitEvent` only after it
has durably stored or bridged the message. This makes a crash before commit replay
the message rather than lose it. Commits may proceed independently across chats,
but the client rejects a commit that skips an earlier delivered message in the
same chat, preventing concurrent handlers from advancing that chat's maximum out
of order.

`Client.CatchUp(chatId, targetMax)` starts from the committed boundary, requests
bounded 300-item `SYNCMSG` pages, validates increasing log IDs within the requested
range, and returns typed but uncommitted events. It tolerates one inclusive repeat
of `cur`. An empty/non-progressing page before `targetMax`, an invalid ordering, or
more than 100 pages fails closed. A bounded no-progress/page-limit failure first
persists the complete unresolved interval from the committed boundary through the
target. A later successful recovery removes only the recovered prefix, preserving
any newer outstanding tail. Protocol/network failures do not invent gap state.
The caller commits returned events in order after durable handling.

The clean-room checkpoint deliberately stores an implementation-neutral interval
rather than copying the official client's synthetic database-message model. The
three state dimensions remain separate: a server-observed target is work to fetch,
a history gap records failed bounded recovery, and a committed cursor alone says
the application durably handled a message. None of the first two is sent as an
acknowledgement.

`Client.InitialSyncTargets` reads those ceilings from the merged durable inventory.
The inventory is populated from the live-validated chat-data shape: top-level
int64 `c` identifies the chat and embedded `l.logId` identifies its current last
log. If `l.chatId` is present it must match `c`; empty chats are retained in the
inventory with a zero ceiling but omitted from catch-up targets.

## Remaining validation

- Complete one owned-account experiment covering an offline message, resumed
  `LOGINLIST`, and `SYNCMSG`, without retaining message contents or live IDs.
- Confirm whether `cur` is inclusive in every server branch and classify
  retention/permission/status failures.
- Map post-sync thread/member follow-ups and initial-history bootstrap policy.
- Map server-directed reconnect, `CHANGESVR`, and `KICKOUT` into the checkpointed
  lifecycle without introducing an automatic mutation retry.

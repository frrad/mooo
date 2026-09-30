# Message continuity and recovery

Status: durable resume and recovery implemented and live-validated through the
bridge: an offline message is recovered on restart before live events.

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

### `cnt` declares held messages (2026-09-30)

`cnt` is not a page size. The official per-room sync sends `cur` as the room's
last-synced log and computes `max` and `cnt` from its local database: `cnt` is the
number of messages it already stores after `cur` (capped at 300), and `max` is the
highest stored log ID. A controlled live test confirmed the server returns only the
messages the client does not declare holding: for a range containing one missing
message, `cnt=300` and `cnt=1` returned nothing, while `cnt=0` returned the message
(SL-BIN-035, SL-LIVE-013). Catch-up therefore declares zero held messages.

The same request serves explicit mark-as-read and read-all. One run suggested
that `cnt=0` recovery left the sender's unread marker in place while requests that
declared held messages cleared it (SL-LIVE-014); treat this read-state effect as
a hypothesis until a dedicated A/B test confirms it. The client currently records
a local read watermark after every `SYNCMSG`, including `cnt=0` recovery, which
may be wrong if recovery does not mark messages read.

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

A deeper first-party pass located the official loss-mark storage transitions and
their principal boundary inputs. The Mac client creates a synthetic stored marker
at a log boundary after checking the neighboring normal messages and existing
markers. On `LOGINLIST`, a returned last chat log can produce a marker at
`prevId + 1` when `prevId` is positive, the exact previous message is absent, and
older normal history exists. If there is no returned last chat log, a positive
`lastServerLogId` with no exact stored message can produce a marker at
`lastServerLogId + 1`.

The `SYNCMSG` success callback reads the minimum and maximum returned `logId`,
raises the room's `lastSyncLogId` and `lastMChatLogId` to the maximum when needed,
and removes stored loss marks strictly between the two returned bounds. Its first-
message check can create a marker at `first.prevId + 1` when the previous message
is absent and lies beyond the response's `jsi` boundary. It also removes the
marker at `max + 1` when the next known server message links back with
`prevId == max`. The multi-chat `MCHATLOGS` path uses the same open-range and
next-message-link cleanup pattern.

At the storage layer, exact removal queries chat ID, log ID, and loss-mark type;
range removal first clamps the lower bound to the room's minimum retained log ID,
then deletes markers satisfying `lower < logId < upper`. This proves that missing
history is durable room state in the official client, not merely a transient
error return.

`doAfterSyncMsg:` is narrower than an initial decompiler function boundary
suggested: when the room's pending thread-ID set is non-empty, it passes that
chat-to-thread-ID mapping to the thread reconciler and clears the set. The
reconciler is a local persistence path: it runs in the database context, revises
stored thread state from room/message data, completes, and broadcasts an internal
update. No LOCO thread-fetch request was observed on this path. The adjacent
member-add request belongs to the separate
`doAddMemWithChatRoom:memberIds:completion:` method and is not evidence of
automatic post-`SYNCMSG` member refresh.

The remaining login bootstrap path is also narrower than its helper names first
suggested. The only observed caller of the helper that creates a marker at a
room's `lastLogId + 1` is cloud-restore new-count recomputation. It skips rooms
whose `lastLogId` is zero, skips an existing exact message, suppresses a duplicate
marker at the next ID, and otherwise creates that marker. No call edge connects
this restore repair to ordinary `LOGINLIST`/`LCHATLIST` startup. Normal login
bootstrap therefore remains governed by the chat-data and `SYNCMSG` transitions
above; the restore-only rule must not be applied globally.

OpenChat metadata is the one network follow-up in the reviewed chat-list handler.
For chat data with a positive link ID, the client looks up the stored link. A
missing link defers that chat-data record; a stored link whose token is older than
the returned token is also scheduled for refresh, but its chat data can be
handled immediately. The client batches the selected IDs into one `INFOLINK`
request using an int64-array field. A successful response persists its
`openLinks` collection in the database, then replays only the deferred chat-data
records through the normal handler. Failure does not replay those deferred
records. Chats without a positive link ID bypass this metadata dependency.

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
`SYNCMSG` pages declaring zero held messages (`cnt=0`), validates increasing log IDs within the requested
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

- Confirm whether `cur` is inclusive in every server branch and classify
  retention/permission/status failures.
- Confirm the current server's `INFOLINK` optional/empty response encodings before
  implementing OpenChat metadata persistence; direct-chat continuity does not
  depend on it.
- Map server-directed reconnect, `CHANGESVR`, and `KICKOUT` into the checkpointed
  lifecycle without introducing an automatic mutation retry.

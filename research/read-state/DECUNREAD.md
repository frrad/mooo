# DECUNREAD state transition

Status: static clean-room contract, 2026-10-02. Evidence: RS-BIN-021 through RS-BIN-023 in [`EVIDENCE.md`](EVIDENCE.md).

For an inbound `DECUNREAD(chatId, userId, watermark)`, the official callback first
looks up the existing room by `chatId`. A missing room ends the callback with no
state mutation. For an existing room it routes the member watermark helper for
`(chatId, userId, watermark)`. The helper does nothing unless the room's active
member ID set is non-empty and either `roomType != 3` or the room is not frozen.
For each supplied member ID, membership in the bot-ID set suppresses the entire
iteration: no active-member addition, watermark write, or follow-up occurs. For a
non-bot ID, an absent active-member entry is added and refreshes the active-member
count; an existing entry is not added again. The watermark map is updated only
when no prior value exists or the incoming value is strictly greater. Equal and
stale values do not overwrite storage. A changed watermark schedules the helper's main-thread follow-up, which first
cancels an earlier delayed invocation of the same maintenance selector. The
active-member refresh rebuilds member projections from active IDs, bot IDs,
unknown-member IDs, and current-user membership, then sorts and stores active,
main, and display member projections plus member-list IDs. This is a local
projection refresh, not another wire operation.

The callback compares `userId` with the current account ID. A non-current member has no room-level unread, mention/reply, joined, or archive
transition in this callback; only the guarded member-watermark helper can change
member/active-member state. For the current account, it examines the room's current unread count,
last log ID, and last-seen log ID:

- when the room's unread count is positive and `watermark < lastLogId`, it derives
  unread count from the stored-log query. The query receives one lower-bound value,
  `max(watermark, lastSeenLogId)`; no separate upper-bound argument is passed by
  this callback. Its observed predicate is equivalent to: `chatId == ? AND
  logId > 0 AND logId > ? AND type != ? AND status != ? AND scope IN (?, ?)`;
  the callback supplies the chat ID and clamped lower bound as signed 64-bit
  numbers. The remaining NSNumber arguments are fixed integer constants: excluded
  message type `10001`, excluded status `5`, and allowed scopes `1` and `3`.
- when the room's unread count is positive but `watermark >= lastLogId`, it sets
  unread count to zero and invokes mention/reply reset;
- when the room's unread count is already zero, neither unread assignment nor
  mention/reply reset occurs; joined/archive refresh still follows.

After either current-account branch, it sets `checkJoinedChatRoom` true and
invokes archive-folder refresh. The member helper's per-member order is
active-member addition (when needed), strictly-new watermark assignment (when
needed), and then, if any member was added, the post-loop active-member
count/projection refresh. A changed watermark finally schedules the delayed
maintenance follow-up. The traced
storage helper submits one watermark/member pair through the bulk update API;
this contract does not claim transaction atomicity.

## Boundaries

Observed: room existence gate; current-account predicate; positive-unread gate;
strict `watermark < lastLogId` predicate; positive-unread nesting; lower-bound
clamp `max(watermark,lastSeenLogId)`; zero-and-reset branch; joined-check true;
archive refresh; member watermark guard/write; active-member additions; non-current member path.

Implementation decisions for the clean-room reducer: represent eligible-log count
as an injected pure input (so persistence queries stay outside the reducer), emit
ordered effects, and make `Applied` mean the room existed and the callback routed
its effects. These decisions do not assert official transaction boundaries.

Untraced: completion/error reporting from the database context, and the internal
archive-folder list mutation. The projection refresh's exact display filtering
beyond the traced inputs is not reproduced here.

# Bridge read receipts

Status: implemented and covered by synthetic connector tests (2026-10-05).
Live owned-account validation is tracked in [`PLAN.md`](PLAN.md).

## Inbound: `DECUNREAD`

`DECUNREAD` carries `(chatId, userId, watermark)`. The connector uses only
those three values:

- The receipt targets the bridged message whose log ID equals the watermark.
  If that log was never bridged, it targets the bridged message with the
  highest log ID below the watermark, scanning the chat's 100 most recent
  bridged messages. Kakao log IDs increase within a chat, so the comparison is
  by log ID, not timestamp.
- A notice from another member becomes a Matrix read receipt from that
  member's ghost.
- A notice for the logged-in user becomes a receipt from the Matrix user,
  which bridgev2 sends through double puppeting. Without double puppeting,
  bridgev2 falls back to the bridge bot.
- A notice for an unknown chat, or one whose watermark precedes every bridged
  message, is dropped without queuing anything. Receipts carry no message
  position, so a failed delivery is logged and dropped. It is never replayed
  and never stops the event loop.

The official client also recomputes local unread counts, active-member lists
and archive state from this notice ([dossier](../read-state/DECUNREAD.md)).
Matrix keeps its own unread state, so the bridge has no consumer for that
bookkeeping. The pure reducer that modelled it (`internal/protocol/readstate`)
was deleted with its model-only tests. The dossier remains the specification
if a future store needs it.

## Outbound: Matrix receipts

A Matrix read receipt from the bridge user schedules `Client.MarkRead` with
the receipted message's Kakao log ID. If the receipt targets a non-message event,
bridgev2's read-up-to time selects the latest bridged message at or before it.
A receipt for a message in another chat is ignored.

### Coalescing (bridge policy)

An active Matrix client sends a receipt for nearly every incoming message.
Sending each one would put a mark-read `SYNCMSG` on the wire at message rate,
which the official client never does
([skew audit](NATIVE-SKEW-AUDIT.md) #8). The first receipt for a chat arms a
3-second window; later receipts in the window only raise the chat's pending
position. When the window ends, the bridge sends one acknowledgement for the
highest position. A chat therefore sees at most one acknowledgement per
window. `Disconnect` sends any pending acknowledgement before closing the
session. If the session is already gone, the acknowledgement is dropped, and
the next receipt sends it.

The 3 seconds is not a traced native interval. The closest native timer is
the 100 ms watermark release after main-window activation
([HISTORY-STATIC](HISTORY-STATIC.md) §D), which runs once per window
activation, not once per message. Because the request is deferred, its
failure is logged with the categories below instead of being returned to the
framework.

Owned acceptance (2026-10-15, B bridge, A/B/C group): A sent two messages and
the Matrix tester sent read receipts for both. Their EDUs reached the bridge
within 0.1 s, and one `MarkRead` went out 3 s later. On A, both messages
dropped to one unread member (only C), so the single acknowledgement of the
later position covered both messages.

### Request

`MarkRead` sends one `SYNCMSG` `[watermark-1, watermark]` with `cnt=1`, and
after a successful response it persists the watermark as the chat's read
acknowledgement. A later receipt at or below that position is not re-sent,
including after a restart. A failure is reported once, without a retry: the
shared request path never retries mutations. The logged error keeps only a
stable category (`status N`, cancelled, deadline, session closed, or outcome
unknown) and never wraps backend detail.

### Native mark-read shape (not adopted)

The official client acknowledges reads through its single-room sync
coordinator ([HISTORY-STATIC](HISTORY-STATIC.md) §C, SL-BIN-035):

- `cur` is the room's persisted `lastSyncLogId`;
- `max` and `cnt` come from the locally held messages after `cur`, capped
  at 300, with the room's last log as the fallback `max`;
- nothing is sent when `lastSyncLogId` equals both `lastSeenLogId` and
  `lastLogId`;
- an `isOK=true` response sets `lastSeenLogId` and `lastSyncLogId` to the
  request's `max`.

The bridge still sends `[watermark-1, watermark]` with `cnt=1`, a window the
official client never builds. Switching needs two things this checkpoint
does not have yet:

1. **An analogue of `lastSyncLogId`.** Native raises it after every
   single-room sync, including the mark-read itself. It does not raise it
   for a live `MSG`. The bridge's `ChatCursor` advances on live messages too,
   and `ReadWatermark` is zero for a chat the bridge never acknowledged. The
   candidate is the higher of `ReadWatermark` and the catch-up target of the
   last connect, which would need a new checkpoint field.
2. **A held count.** This is the number of bridged messages after `cur`, up
   to the receipt position, capped at 300. The server returns the messages
   that `cnt` says the client does not hold (SL-LIVE-013). An undercount
   only costs bytes, because `MarkRead` ignores the returned logs. Whether
   the server's read effect depends on `cnt` is unproven: SL-LIVE-014 is
   only a hint.

The switch waits for an owned differential in one group, with the same
unread setup as the [coalescing acceptance](#coalescing-bridge-policy). It
compares the current shape with the native shape on: the read markers on A,
B's phone notification, and the response's `chatLogs` count. It must cover
a chat the bridge never acknowledged and a backlog above 300.

### Decoupling from catch-up

Checkpoint versions up to 4 also recorded the read watermark after every
`SYNCMSG`, including catch-up pages with `cnt=0`. Whether `cnt=0` marks
messages read on the server is unproven, and one run suggested it does not
([procedure](syncmsg-read-side-effect-procedure.md)). If it does not, the
shared watermark made `MarkRead` silently skip positions the server still
showed as unread.

Checkpoint version 5 records the read watermark only after a successful
`MarkRead`. Migrating from version 4 discards the stored values. Outbound
receipts therefore no longer depend on the server effect of `cnt=0`. If
`cnt=0` does mark messages read, the cost is at most one repeated `cnt=1`
acknowledgement. The `cnt=0` differential still matters for catch-up and
backfill policy, but it no longer blocks receipts.

## `NOTIREAD`: not sent

The official Mac client sends `NOTIREAD` only for a live message from another
member that arrives while that chat's window is open and focused. In that
case the message is read on arrival, and `NOTIREAD` is its read
acknowledgement. With the window closed or in the background, it sends
nothing and counts the message as unread
([dossier](../read-state-notiread.md)).

The bridge has no equivalent of an open, focused window. Matrix tells it that
the user has read something only through read receipts, which already go to
`MarkRead`. The bridge therefore does not send `NOTIREAD`. That matches the
Mac client with no chat window open, which is its usual state.

An owned A/B/C group differential on 2026-10-10 confirmed that this omission
leaves nothing stale, as long as a Matrix read receipt eventually arrives:

- While the bridge was online without sending `NOTIREAD`, B's phone kept its
  notification for the group, and A still counted B as unread.
- A Matrix read receipt sent `MarkRead` (`SYNCMSG cnt=1`). B's phone
  notification disappeared within 5 seconds, and A's unread count dropped by
  one.

A Matrix client that never sends read receipts leaves the phone notification
in place. The official Mac client does the same when its window stays closed.

Sending `NOTIREAD` would only make sense if Matrix gained a signal for "the
user is looking at this room right now". The request would then follow the
dossier: five keys, fire and forget, and only for another member's live `MSG`
in a room that exists locally. Its `notiRead` value would come from the
`CHATONROOM` room object, defaulting to true.

### Viewing episodes (considered 2026-10-15, not adopted)

The bridge now receives Matrix viewing heartbeats (`HandleMatrixViewingChat`,
used for reaction resync). They are the closest Matrix signal to an open
window, but they do not establish the native preconditions:

- Native needs a window that is focused and not holding its watermark.
  A heartbeat says only that a client shows the room. Its focus and
  visibility semantics are untraced, and standard Matrix clients send no
  heartbeats.
- Native `NOTIREAD` marks a live message read on arrival. If a heartbeat
  were mistaken for focus, Kakao would mark read messages the Matrix user
  never saw, and the receipt path already acknowledges the ones they did see.

No owned experiment has compared a heartbeat-driven `NOTIREAD` with the
receipt path. Until one shows a behavior the receipt path cannot produce,
the bridge does not send it.

## Regular groups (2026-10-10)

Forward-only per-member receipts, offline read recovery through `CHATONROOM`
and the owned catch-up differential are recorded in
[group read receipts](GROUP-READ-RECEIPTS.md).

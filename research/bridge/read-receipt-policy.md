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

`MarkRead` sends the official client's single-room sync
([native mark-read shape](#native-mark-read-shape)). After the server confirms
a request with `isOK=true`, the bridge persists its `max` as the chat's read
acknowledgement. A later receipt at or below that position is not re-sent,
including after a restart. A failure is reported once, without a retry: the
shared request path never retries mutations. The logged error keeps only a
stable category (`status N`, cancelled, deadline, session closed, or outcome
unknown) and never wraps backend detail.

### Native mark-read shape

Adopted 2026-10-15 after the owned differential below.

**Official client (macOS 26.8.0, static).** Explicit mark-as-read, read-all
and summary-and-read enter the single-room sync coordinator with the room's
`lastLogId` as the upper bound ([HISTORY-STATIC](HISTORY-STATIC.md) §C,
SL-BIN-035, SL-BIN-042):

- `cur` is the room's `lastSyncLogId`. Only this coordinator's response
  callback writes it, so a room that never ran a single-room sync starts at 0.
  LOGINLIST/LCHATLIST `s`, live `MSG`, bulk `MCHATLOGS`, `CHATONROOM`,
  `DECUNREAD` and a successful `WRITE` leave it unchanged.
- `max` and `cnt` come from one query over the local messages: the oldest 300
  with `cur < logId <= upper bound`, no type or status filter. `max` is the
  highest of them; with no rows it is the room's `lastLogId`.
- Nothing is sent when `lastSyncLogId`, `lastSeenLogId` and `lastLogId` are all
  equal. Otherwise the request is sent even when `cur` equals `max`, with
  `cnt=0`.
- `isOK=true` sets `lastSyncLogId` and `lastSeenLogId` to the request's `max`,
  and the coordinator runs again only when `cnt` was 300. `isOK=false` raises
  `lastSyncLogId` to the highest returned log (and `lastSeenLogId` only to the
  request's `cur`), and the coordinator runs again when any log was returned.
  Each continuation recomputes `cur`, `max` and `cnt` with the original upper
  bound, without a delay.

**Server rule (owned, observed).** One A/B/C regular group, the bridge as B,
A's official Android client sending synthetic texts, A's per-message unread
counts read before and after each request ([fixture](../fixtures/bridge/markread-native-observed.json),
SL-LIVE-015):

| Request | Server held in `(cur, max]` | Response | A's unread counts |
|---|---|---|---|
| `cnt` equal to the held count | n | `isOK=true`, no logs | read through `max` |
| `cnt` above it (overcount) | 2, `cnt=3` | `isOK=true`, no logs | read through `max` |
| `cnt` below it (undercount) | 3, `cnt=2`; 2, `cnt=1` | `isOK=false`, the logs | unchanged after 30 s |
| `cur` equal to `max`, `cnt=0` | 0 | `isOK=true`, no logs | read through `max` |

The server confirms a request, and marks the chat read through `max`, exactly
when `cnt` covers every log it holds in `(cur, max]`. The bound is not the
account's server read position: `cur` above it still reads everything through
`max`. This also explains earlier observations. Catch-up's `cnt=0` over unread
logs is an undercount and reads nothing, and the old `[watermark-1, watermark]`
`cnt=1` window always holds exactly one log, so the server always confirmed it.
Whether the logs returned with `isOK=false` are always the whole range is open:
one undercount returned only an older log below the declared ones, two others
the whole range.

**Bridge analogues.**

- *`lastSyncLogId`:* the persisted read acknowledgement, zero for a chat the
  bridge never acknowledged, as for a native room that never synced. It
  advances only on `isOK=true`, so it is also the analogue of `lastSeenLogId`
  after a mark-read.
- *Held messages:* the chat's bridged Kakao logs in `(cur, receipt]`, every
  message row of the chat with or without a Matrix event, oldest 300 first.
  Logs the bridge never stores (feeds such as invitations, skipped types, logs
  before the bridge joined) make this an undercount.
- *Upper bound:* the Matrix receipt position, not the chat's last log
  (deviation below).

**Request sequence.** `MarkRead` sends `SYNCMSG(cur, max, cnt)` from those
analogues. On `isOK=true` it persists `max` and continues from it only when
`cnt` was 300 and `max` is below the receipt. On `isOK=false` with returned
logs it continues from the highest returned log; when that is the receipt
position, the next request is `cur=max`, `cnt=0`, which the server confirms. On
`isOK=false` without logs it stops without persisting anything.

**Owned acceptance (production path).** With a build of this branch:

- A chat the bridge never acknowledged, receipt on the second of three new
  texts: `(0, t2, 2)` returned the group's older invitation feed with
  `isOK=false`; `(feed, t2, 2)` was confirmed. A showed the first two texts
  read and the third unread.
- The next receipt, three bridged texts later: one confirmed `(t2, t5, 3)`.
- An undercount forced by the lab overlay: `(t8, t15, 6)` for seven held texts
  returned all seven with `isOK=false`; `(t15, t15, 0)` was confirmed and A
  showed all seven read.
- A second chat the bridge never acknowledged, with 302 bridged texts and a
  receipt on the last: `(0, t300, 300)` returned only the older invitation
  feed with `isOK=false`, `(feed, t300, 300)` was confirmed, and the capped
  continuation `(t300, t302, 2)` was confirmed. A showed the last texts read,
  and `CHATONROOM` reported the account's watermark at t302.
- Control, a build without this change, in a third group: `[t1-1, t1]`
  `cnt=1` was confirmed and read the first text only.

The returned logs had no side effect on the bridge: no message was bridged
again, and the chat cursor, server inventory and history gaps in the
continuity checkpoint changed only with live delivery.

**Deliberate deviations.**

- *Upper bound.* Native acknowledges the whole room (`lastLogId`). A Matrix
  receipt may name an earlier message, and the bridge acknowledges only up to
  it.
- *Unconfirmed position.* Native persists the `isOK=false` position as
  `lastSyncLogId`. The bridge keeps it only for the continuation in progress.
  A position the server never confirmed is not a read, and persisting it made a
  first build of this change skip later receipts while A still showed the
  messages unread. An interrupted continuation therefore repeats one
  unconfirmed round instead.
- *Skip predicate.* Native skips only when its three positions are equal and
  can send `cur=max`, `cnt=0` to confirm a position it already synced. The
  bridge skips any receipt at or below its acknowledgement.
- *Extra round.* Native stores feeds and every message type, so its count is
  usually exact and one request suffices. The bridge's count misses logs it
  never stores, which costs one `isOK=false` round that returns them before the
  confirming request.

### Decoupling from catch-up

Checkpoint versions up to 4 also recorded the read watermark after every
`SYNCMSG`, including catch-up pages with `cnt=0`. Whether `cnt=0` marks
messages read on the server is unproven, and one run suggested it does not
([procedure](syncmsg-read-side-effect-procedure.md)). If it does not, the
shared watermark made `MarkRead` silently skip positions the server still
showed as unread.

Checkpoint version 5 records the read watermark only after a successful
`MarkRead`. Migrating from version 4 discards the stored values. Outbound
receipts therefore no longer depend on the server effect of `cnt=0`. The
2026-10-15 differential settled that effect: a catch-up page declares none of
the logs it fetches, so the server answers without `isOK` and marks nothing
read ([native mark-read shape](#native-mark-read-shape)).

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

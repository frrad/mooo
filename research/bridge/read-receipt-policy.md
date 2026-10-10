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

A Matrix read receipt from the bridge user calls `Client.MarkRead` with the
receipted message's Kakao log ID. If the receipt targets a non-message event,
bridgev2's read-up-to time selects the latest bridged message at or before it.
A receipt for a message in another chat is ignored.

`MarkRead` sends one `SYNCMSG` `[watermark-1, watermark]` with `cnt=1`, and
after a successful response it persists the watermark as the chat's read
acknowledgement. A later receipt at or below that position is not re-sent,
including after a restart. A failure is returned once, without a retry: the
shared request path never retries mutations. The returned error keeps only a
stable category (`status N`, cancelled, deadline, session closed, or outcome
unknown) and never wraps backend detail.

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

## Regular groups (2026-10-10)

Forward-only per-member receipts, offline read recovery through `CHATONROOM`
and the owned catch-up differential are recorded in
[group read receipts](GROUP-READ-RECEIPTS.md).

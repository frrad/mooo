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

After accepting an inbound `MSG`, the official client sends `NOTIREAD` with the
room's notification-read flag ([dossier](../read-state-notiread.md)). Its
server meaning is untraced: the response is not status-checked, and the meaning
of its notification-read value is unknown. What it does to the primary
device's notifications is also untraced. The bridge does not send it, and the
unused request model (`internal/protocol/notiread`) was deleted rather than
wired in speculatively. An owned A/B run on 2026-09-30 suggested that
receiving a live `MSG` already cleared the sender's unread marker without
`NOTIREAD` ([parity notes](../protocol-parity.md)). Whether omitting it leaves
stale notifications on the primary device is an open question in
[`PLAN.md`](PLAN.md).

## Regular groups (2026-10-10)

Forward-only per-member receipts, offline read recovery through `CHATONROOM`
and the owned catch-up differential are recorded in
[group read receipts](GROUP-READ-RECEIPTS.md).

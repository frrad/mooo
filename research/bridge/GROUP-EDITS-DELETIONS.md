# Regular-group edits and deletions

Evidence date: 2026-10-10. Scope: editing and deleting messages for everyone
in an encrypted three-member regular group, both directions, including
changes made while the bridge is offline.

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project; no public prior art. High confidence
unless marked.

- **Requests.** `DELETEMSG {chatId, logId}` deletes for everyone;
  `MODIFYMSG {chatId, logId, type, msg, extra}` edits, with `type` the
  original type, `msg` non-empty and `extra` replacing the attachment.
  Responses have the shape of the pushes below. Delete-for-me is local only.
- **Delete rules.** Own messages only (no member or admin delete in regular
  groups; open-chat hosts use a separate hide feature); any message type;
  not already deleted; within `messageDeleteTimeV2` minutes of `sendAt`
  (account setting, default 1440). Statuses: −210 type not deletable, −211
  already deleted, −212 time limit expired. No retry.
- **Edit rules.** Own messages only; text, replies that are not
  attachment-only, and emoticons with a caption; media is not editable;
  within a hard-coded 24 hours; unchanged text is not sent; no client-side
  edit count. No retry.
- **Pushes.** `SYNCDLMSG` carries a type-0 feed chat log with `feedType` 14,
  `logId` (the target), `hidden` and `byHost`. `SYNCMODMSG` carries a
  feed with `feedType` 25, `logId` and `targetRevision`, plus
  `modifiedChatLog`, the edited message with a higher `revision`. Both feeds
  occupy their own position in the chat's log sequence.
- **Applying them.** A deletion marks the stored target with the
  `0x4000` deleted flag and shows "The message has been deleted."; the text
  stays in the local database; replies and reactions to it are refused; an
  unknown target is ignored. An edit is applied only if its revision is
  higher; the label "Edited" shows when `revision` > 0; an unknown target is
  ignored.
- **Offline recovery.** Deletions are recovered from feed-14 logs in the
  `SYNCMSG` range. Feed-25 logs whose target revision is older are collected
  and the targets are read with `GETMSGS {chatIds, logIds}` (batches of
  200, response `chatLogs`).

Recorded gaps: Swift completion closures and UI for edit errors (error
type 77), the server's own limits, the open-community delete limit source,
the edit length limits, and the unread-count effects.

## Observed fixtures

`research/fixtures/edits/observed-group-edit-delete.json`: owned A edited one
text and deleted another for everyone. A read-only B push probe recorded
`SYNCMODMSG` and `SYNCDLMSG` exactly as modelled above. A read-only
`SYNCMSG` from the earlier cursor returned the edited message at its
original position with the new text and `revision` 1, the feed-25 log,
**the deleted message as type 16385 (text plus the deleted flag) still
containing its original text**, and the feed-14 log.

A later read-only comparison after an offline edit showed that LOGINLIST's
`l` (last chat log) stays at the last displayable message while `ll` and
`CHATONROOM`'s `l` (last log ID) cover the edit feed.

## Production changes and regressions

Each change was preceded by a failing production-path test.

- **Inbound.** `SYNCMODMSG`/`SYNCDLMSG` pushes and the feed-25/feed-14 logs in
  catch-up decode to edit and deletion events at their own positions. An edit
  becomes a Matrix edit of the bridged target when it comes from the
  message's author and its revision is newer than the stored one; a
  deletion redacts the target as the deleting member. Feeds commit even when
  the target was never bridged. A deleted-flagged log becomes a "This
  KakaoTalk message was deleted." placeholder and never reveals its content;
  a later delete feed for a placeholder commits without redacting it.
- **Offline edits (regression).** An edit made while the bridge was offline
  arrived without its text. Reading it with a `SYNCMSG` range returned
  nothing and the edit was lost; it is now read by position with `GETMSGS`,
  like the Mac client, before the edit is queued, and a failed read leaves
  the feed uncommitted.
- **Sticker text edits (regression, 2026-10-16).** Edits of a sticker sent
  with text were ignored because the edited message was not a text. The text
  part is now edited and the sticker part keeps its event; see
  [stickers](../stickers.md#mac-client-2680-parity-2026-10-16).
- **Matrix edits of sticker text and replies (2026-10-16).** The bridge
  had refused every Matrix edit except a plain text. The Mac lets its user
  edit the text of their own sticker with text (types 12 and 20) and of
  their own reply unless it is attachment-only (executed:
  [`fixtures/stickers/executed-mac-sticker-text.json`](../fixtures/stickers/executed-mac-sticker-text.json)).
  Its `MODIFYMSG` `extra` starts from the composer's extra and adds each of
  these keys the original attachment has (static:
  [`fixtures/edits/static-mac-modify-extra.json`](../fixtures/edits/static-mac-modify-extra.json)).
  For an emoticon the keys are `url`, `name`, `sound`, `path`, `path2`,
  `scon`, `alt`, `xconVersion`, `width` and `height`. For a reply they are
  `src_logId`, `src_userId`, `src_linkId`, `src_type`, `attach_only`,
  `attach_type`, `attach_content`, `src_message` and `src_mentions`. Other
  keys are dropped. A plain text keeps the composer extra alone.
  - A Matrix edit of a sticker's text part (`text`) or a reply's text part
    (empty part ID) now reads the target with one `GETMSGS`, because mooo
    keeps no raw copy of the attachment. It checks that the target's author
    and type still match, builds `extra` with that recipe and sends one
    `MODIFYMSG` with the original type. An edit of a sticker part, of a
    sticker without text or of an attachment-only reply is refused before
    anything is sent. So is an edit whose target the server does not
    return. A failed read is a certain failure because nothing was sent.
    The message's other parts take the new revision and reply preview.
  - Deliberate deviation: the extra `GETMSGS` read per sticker or reply edit
    (the Mac uses its stored copy). The bridge sends no mentions, spoilers
    or links in `extra`, as for text edits.
  - Observed live (owned A/B, Android 26.8.2 as the official clients:
    [`fixtures/edits/observed-direct-outbound-sticker-reply-edits.json`](../fixtures/edits/observed-direct-outbound-sticker-reply-edits.json)).
    A Matrix edit of B's own sticker with text and one of B's own sticker
    reply with text each produced one feed-25 log and the edited message.
    Its sticker keys, or its `src_*` and `attach_*` keys, were kept. The
    server also kept `emoticonItemPath`, which the request did not carry.
    A's official client showed both labelled Edited, with their stickers.
- **Catch-up ceiling (regression).** With no message after an offline edit,
  catch-up never ran because the ceiling came from LOGINLIST `l`; the next
  live message then committed past the edit feed. Resume targets now use the
  larger of `l` and `ll`.
- **Outbound.** A Matrix edit of the user's own text message sends one
  `MODIFYMSG`; a Matrix redaction of the user's own message sends one
  `DELETEMSG`. Kakao's rules are checked first (own messages, text-only
  edits, 24-hour windows, deleted targets); room capabilities advertise
  edits and deletions with 24-hour limits. Each Matrix event is reserved
  before its one request. Refusals (−210, −212, other statuses) are
  certain; −211 counts as deleted; transport failures are ambiguous; nothing
  is retried. An outbound edit records the returned revision so a later
  catch-up does not edit again.
- **Deleted from Matrix (regression).** A message sent and then deleted from
  Matrix lost its mapping with the redaction; outbound sends do not move the
  delivery cursor, so catch-up delivered its deleted-flagged log and the
  bridge posted a placeholder. A successful outbound delete now records a
  durable tombstone, and that log commits without a Matrix event.

### Deliberate deviations

- Never-bridged deleted messages become a placeholder (the Mac stores and
  shows the same placeholder text).
- Outbound edits are limited to plain text; replies and captioned emoticons
  are rejected. mooo has no account setting and uses the 1440-minute delete
  default.
- Edits apply only when the editor is the stored author.

## Owned encrypted acceptance

Method: fresh builds of this branch with the original B secondary profile in
the existing owned encrypted A/B/C regular group. Every native action,
Matrix event, probe and bridge start/stop wrote a private EXCL 0600 receipt
first; no mutation was repeated. Matrix results were read with the tester
device; native results on A.

| Case | Result |
|---|---|
| Offline: A edited one text and deleted another; reconnect | edited text bridged once with its final text; deleted message bridged as placeholder and redacted (before the placeholder refinement); its text never reached Matrix |
| A sends and edits a text online | Matrix edit from A's ghost on the original |
| C sends and deletes a text online | redaction from C's ghost |
| Offline: A deletes a bridged text | redaction from A's ghost after reconnect |
| Offline: A edits a bridged text again | **lost** with the `SYNCMSG` read (regression fixed); then **not caught up** because of the `l` ceiling, and skipped by the next live message (fixed) |
| Offline edit on the fixed build | Matrix edit after reconnect via `GETMSGS` |
| Matrix edit, then redaction, of the user's text | native A shows "Edited" with the new text, then "The message has been deleted." |
| Matrix edit and redaction of A's message | two notices (own messages only); A's message unchanged |
| Restart after the Matrix deletion | **a placeholder appeared** (regression fixed); on the fixed build a further send-and-delete followed by restart produced no event and the cursor passed the log |
| Restarts | no repeated MODIFYMSG/DELETEMSG; no duplicate edits or redactions |

### Acceptance gaps

- The −210/−211/−212 statuses and the 24-hour limits were not provoked live.
- Edits of replies and captioned emoticons, and edit or delete of media,
  were not exercised.
- Two earlier edits remain unapplied in Matrix from the runs that exposed the
  regressions, and one stray placeholder remains; all are committed.
- Hidden feeds after the last displayable log (if the server omits them from
  `SYNCMSG`) would make catch-up report a gap; not observed.

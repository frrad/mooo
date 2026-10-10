# Regular-group text replies

Evidence date: 2026-10-10. Scope: type-26 text replies in an encrypted
three-member regular group, Kakao → Matrix (live, offline catch-up and history
import) and Matrix → Kakao, including targets that cannot be mapped. Replies
whose source or body is an image are the next slice.

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project. No public prior art was used. These are
static observations, not executed fixtures.

### Inbound rendering

- The quote card is built only from the reply's embedded `src_*` fields. The
  author line is "Reply to <name>", "Reply to Me" or "Reply to Unknown user".
  The quoted text depends on `src_type`: "Photo", "Photos" (or the album
  comment), "Video", "Voice Memo", "KakaoTalk Profile"; otherwise `src_message`
  with mentions and spoilers applied, cut at 100 UTF-16 units plus "…".
- A source that is missing locally only loses its thumbnail and rich emoji;
  nothing is fetched for display. Tapping the quote looks the source up
  locally by log ID and shows "Unable to move to original message." when it is
  missing, deleted or invisible. Nothing checks that `src_logId` points to a
  message of `src_type`.
- A reply's chat-list summary is its own text, never the quote.

### Outbound construction

- `src_logId` and `src_type` come from the source message; `src_userId` and
  `src_linkId` from its local author object (0 when the author is unknown
  locally). `src_linkId` is always written, as 0 outside open chats.
- `src_message` is the source's export summary ("Photo", "Video", or the text;
  for a reply-of-reply, that reply's own text). Over 100 units the cut keeps
  whole composed characters.
- `src_spoilers` copies the source's spoilers and is omitted when there are
  none; `src_mentions` and `src_emojis` are added when present.
- The UI refuses deleted, blinded, thread and not-yet-sent (pending, failed,
  invisible) sources and plus/partner rooms. The send method itself only
  refuses a missing source or empty text.
- WRITE with type 26 and the `src_*` extra. A failure marks the message failed
  (`-2`); there is no automatic retry, and a manual resend reuses the stored
  extra.
- In rooms where chat threads are enabled (direct, regular group and memo rooms
  unless a server flag disables them) the Mac's Reply action opens a thread
  instead of sending type 26. The owned Android 26.8.2 client sends type-26
  replies in this group, and incoming type-26 replies render fully on Mac.

### Recorded gaps

The local-only keys stripped before WRITE (that `src_*` reach the wire is
inferred), the send-gate checks and maximum reply length, the thread-window
reply path, reply notification behavior in muted rooms, and the server flag
controlling threads were not traced.

## Production changes and regressions

Each change was preceded by a failing production-path test.

- **Inbound reply to an unbridged source.** The bridge passed the source as a
  reply target, and the framework silently dropped the relation when that
  source had no Matrix event, losing the reply's context. When the source is
  confirmed absent from the portal, the reply now carries the source preview
  Kakao embedded in the reply as a quote (`> preview` in the body, a
  `<blockquote>` in the HTML body) and no relation. This mirrors the native
  card, which renders from the same embedded fields. A database lookup error
  leaves the relation to the framework. The real-framework tests cover a
  bridged source (relation, no fallback), a missing source (quote, no
  relation), and history import (a source inside the imported interval
  relates; an older one is quoted).
- **Outbound reply to an unmappable target.** Replies to a Matrix event with no
  KakaoTalk mapping, or to a row without source metadata, were already refused
  before any source mutation, but with a generic error. They now carry a
  certain, unsupported status explaining that the target is not a bridged
  KakaoTalk message and that the message can be resent without the reply.

### Deliberate deviations

- The inbound fallback quote has no author line; the native card shows
  "Reply to <name>".
- mooo omits `src_linkId` outside open chats and sends an empty `src_spoilers`
  array, matching the earlier owned Android observation; the Mac writes
  `src_linkId: 0` and omits empty spoilers. Both shapes rendered correctly on
  the official Android client.
- mooo truncates `src_message` at 100 UTF-16 units without an ellipsis, without
  splitting a surrogate pair (observed Android behavior); the Mac keeps whole
  composed characters.

## Owned encrypted acceptance

Method: a fresh build of this branch with the original B secondary profile in
the existing owned encrypted A/B/C regular group. Every native send, Matrix
send and bridge start/stop wrote a private EXCL 0600 receipt first; no mutation
was repeated. Matrix results were read with a tester device (decryption, sender,
body, reply target); native results with official A's reply cards (quoted
author and text).

| Case | Result |
|---|---|
| C text, then A's native reply to it, both while the bridge was offline | catch-up delivered both once; A's reply relates to C's event |
| C's native reply to A's reply, online | relates to A's reply event |
| Matrix reply to C's text | one Kakao reply; native A shows "Reply to" C with C's text |
| Matrix reply to an earlier Matrix-originated text | native A quotes the bridge account and that text |
| After a restart, Matrix reply to an unbridged Matrix event | refused with the new status notice; no mapping and no Kakao send |
| After a restart, Matrix reply to A's Kakao reply | native A quotes A's reply |
| A's native reply to a Matrix-originated reply | relates to that Matrix event |
| Final restart | no new Matrix events; durable cursor equal to the latest mapping |

The tester's original crypto device became unusable mid-run when the operating
system's temporary-file cleanup removed its pickle key. A replacement tester
device was created on the owned local homeserver with durable private storage;
results above before that point were read with the original device, later ones
with the replacement.

### Acceptance gaps

- A live inbound reply to an unbridged source was not produced: every message
  in this group is already bridged, so the fallback is covered by the
  real-framework tests only.
- History import of replies was exercised by tests, not by an owned import.

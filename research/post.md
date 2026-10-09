# Post (24): inbound text posts

Research date: 2026-10-09. Reference: authorized Android KakaoTalk 26.8.2 APK
and owned A/B emulators. Methods: controlled Boards → Create New experiment,
original secondary-session MSG capture, native receiver inspection and scoped
JADX 1.5.6 inspection. No public prior art was used. Raw traffic, screenshots
and proprietary output remain outside the repository.

## Observed text-post creation

The sender opened the owned chat's More → Boards → Create New flow and entered
`Mooo-Synthetic-Post-Capture`, leaving Announcement deselected and adding no
attachments. A private receipt preceded the one DONE action. The receiver got
type **24**, with attachment keys `os` and `version`.

The first object had discriminator `t: 1`, plain content `ct`, and `jct` holding
a **JSON string** encoding an array with one `type: "text"` element and its
`text`. Both representations contained the synthetic source text. The second
object was a navigation button (`t: 2`, `st: 1`) with an account-specific
`kakaomoim` URL. Version values were Android/iOS `11.1.0`, Mac `3.7.5` and
Windows `4.2.0`. The native receiver showed the synthetic text and View Post.
Confidence is high for this observed text-only creation shape. Navigation
identifiers are replaced in the shared fixture; the source text is synthetic.

## Static chain

`PostEditActivity` (classes37.dex) passes the selected chat and PostPosting
model to PostPostingService. `MoimApi.kt` (`ph90`, classes11.dex) builds a
PostContentRequest, serializes content elements and supplies the chat to the
create API. The posting service selects create versus edit by presence of an
existing post ID. Successful status produces a Post response model and
listener/event notifications; nonzero status uses a server error or generic
failure. Exceptions notify failure and clear posting state.

`PostChatLog.kt` (`com.kakao.talk.db.model.chatlog.u`, classes10.dex) parses
`os` and version metadata, then asks the shared post-object formatter for its
preview. The first header can select announcement/share prefixes. The formatter
dispatches text, media, schedule, poll, quiz and other object forms separately.
For text, a nonempty structured content list takes precedence over plain `ct`.

`PostObject.kt` (classes10.dex) defines text discriminator 1 and the `ct`/`jct`
fields. Its custom structured-content serializer (`d901`) decodes a JSON string
into content elements and also has an array-input branch. The outer object
factory catches parse failures and can drop failed objects; unknown
discriminators become unknown objects. These static branches are leads, not
live evidence for mentions, mixed media or alternate carriers.

The full endpoint interface, content-element request serialization, response
model, persistent database writes and every callback/downstream consumer are
still untraced. Editing, deletion, announcement/share headers, native board
interaction and multimedia post parity remain separate gaps.

## Implementation decision and acceptance plan

Render the observed text-only post as a readable Matrix `m.text` snapshot with
its source text, preserving message identity and ordering. Parse the embedded
`jct` string as structured data and preserve ordered text elements; do not
silently display `ct` when structured content is malformed or unsupported.
Navigation buttons must not trigger requests or expose their private URL.

Bound both JSON layers, reject duplicate fields and unknown content/object
forms, and retain a delivery gap for unsupported rich or multimedia content.
The bridge snapshot is separate from native board editing, comments and likes.
The observed string carrier is supported; legacy `ct`-only forms, the statically
identified array carrier, mentions and non-text elements still need controlled
fixtures. Structured text takes precedence over `ct`; malformed structured data
must not hide behind a readable fallback.

Local limits are 64 KiB per JSON layer, 16 post objects, 128 text elements,
16 KiB per element and 32 KiB for the combined source text. The shared JSON
validator rejects duplicate keys, excessive depth and excessive complexity.
These are implementation limits, not recovered official service maxima.

The initial catch-up message and a distinct live post passed SDK decryption as
encrypted Matrix text with exact source content. Native View Post displayed
each matching source text. The shared sender created the live post and returned
to the owned chat without another submit. Normal restart retained exactly two
post messages with no conversion gaps, and delivered a later text once. The
same SDK crypto store decrypted the earlier post and the later text after
restart. This proves the observed text-only snapshot path, not complete board
interaction or mixed-content parity.

## Repeatable owned sender

`research/emu.sh send-post a|b` consumes private stdin `peer`, `text` and
`receipt`. It starts in an already-open owned chat with an empty composer,
navigates More → Boards → Create New, requires an empty non-announcement
editor and enters a synthetic ASCII `Mooo-Synthetic-` token. It rejects an
already-visible matching post, rechecks the text and announcement setting,
waits for a stable DONE control, and reserves a private receipt before one
submit. A confirmed new post returns through the guarded board/More path to
the same owned chat. Any uncertain outcome preserves the receipt and blocks
automatic repeats. The helper does not grant permissions, change announcement
settings, attach media, edit old posts or retry restricted actions.

# Regular-group image replies and reply attachments

Evidence date: 2026-10-10. Scope: replies whose source is a photo, replies that
carry a sticker, and Matrix images sent as replies, in an encrypted
three-member regular group.

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project; no public prior art. Static observations,
not executed fixtures.

- **Reply to a photo** is allowed; the reply check never looks at the source
  type. `src_type` is the source type (2 photo, 27 album) and `src_message` the
  localized summary: "Photo" (without the caption), "%d photos" for an album,
  "Video". No thumbnail, token or URL keys are added for media sources.
- **A reply cannot carry a photo or file.** While replying, the file button is
  disabled, drops are refused and pasted images or files are skipped. The photo
  send path never reads the reply source, so no type-26 photo and no type-2 or
  27 message with `src_*` exists. No warning is shown.
- **A reply can carry an emoticon** as one type-26 message: `attach_type`,
  `attach_content` (name, path and further resource fields) and `attach_only`
  (true without text; the message is then the localized emoticon placeholder).
- **Inbound rendering** recognizes emoticon attach types 6, 12, 20, 22 and 25;
  with `attach_only` the text body is hidden and the quote plus sticker show.
  Other attach types are ignored and the message renders as a plain text reply.
  Photo sources quote as "Photo" (type 2), the album comment or "Photos" (27),
  or "Video" (3, 28); a thumbnail appears only from the local cache.
- In rooms with chat threads enabled, the Mac's reply action posts a thread
  comment (scope 3) instead of type 26 unless the source is already in a thread.

Recorded gaps: the chat-capture path while replying, two unidentified input
controls, alternate file-picker entry points, the meaning of attach types 22
and 25, non-English summaries, and the thread selection flag.

## Observed fixture

`research/fixtures/replies/observed-group-reply-shapes.json`: owned A
(Android 26.8.2) sent a text reply to a photo, a sticker-only reply to a reply
and a sticker-only reply to a photo while a scoped read-only B push probe
recorded key sets, value kinds, `src_type`, `src_message`, `attach_type`,
`attach_only` and the `attach_content` key set. A reply to a photo carries
`src_type` 2 and `src_message` "Photo". A sticker reply carries the message
"(Emoticons)", `attach_only` true, `attach_type` 12, `emoticonItemPath` and
`attach_content` with `name`, `path` and `type`. Android's reply composer also
hides the media button, matching the Mac.

## Production changes and regressions

Each change was preceded by a failing production-path test.

- **Sticker replies.** The reply decoder ignored `attach_*`, so a sticker reply
  reached Matrix as the literal text "(Emoticons)". Sticker attach types 12 and
  20 now decode through the existing sticker decoder and become a Matrix
  `m.sticker` that relates to the reply source (or carries the quote fallback
  when the source is not bridged). The reply's stored type stays 26.
- **Unsupported reply attachments** become an explicit notice that names the
  attach type and still relates to the source; text alongside such an
  attachment stays visible with a marker. A sticker with its own text (not
  observed) keeps the text and marks the sticker. Nothing is silently dropped.
- **Reply to a photo.** A Matrix reply to a bridged photo sent `src_message`
  "[image]"; it now sends "Photo", including for rows stored with the older
  preview.
- **Matrix image sent as a reply** was already refused before any source
  mutation; it now carries a certain, unsupported status explaining that
  KakaoTalk cannot send a photo as a reply.

### Deliberate deviations

- Unknown attach types become a notice; the Mac renders them as a plain text
  reply, which would show only a placeholder.
- Sound stickers (6) and attach types 22 and 25 are not rendered as stickers.
- Album sources are not yet quoted as "%d photos"; album reply targets keep the
  stored preview.

## Owned encrypted acceptance

Method: a fresh build of this branch with the original B secondary profile in
the existing owned encrypted A/B/C regular group. Every native send, Matrix
send and bridge start/stop wrote a private EXCL 0600 receipt first; no mutation
was repeated. Matrix results were read with the tester device; reply targets
were confirmed against the bridge's message mapping (sender and source type);
native results with official A's reply cards.

| Case | Result |
|---|---|
| A's text reply to a photo, sticker reply to C's reply and sticker reply to a photo, all while the bridge was offline | catch-up delivered one text reply and two `m.sticker` replies from A's ghost, each relating to the expected source (photo by B, reply by C) |
| Matrix text reply to C's photo | one Kakao reply; native A shows "Reply to" C with "Photo" |
| Matrix image sent as a reply | refused with the new status notice; no mapping and no Kakao send |
| C's animated sticker reply to a Matrix message, online | one `image/webp` `m.sticker` from C's ghost relating to that message |
| Restart | no new Matrix events; durable cursor equal to the latest mapping |

### Acceptance gaps

- Album, video and sound-sticker sources and replies with attach types other
  than 12 and 20 were not produced live.
- The sticker media bytes in Matrix were not hashed by the transcript tool.

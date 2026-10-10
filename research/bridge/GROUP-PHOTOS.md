# Regular-group photos

Evidence date: 2026-10-10. Scope: single JPEG/PNG photos (chat-log type 2) in
an encrypted three-member regular group, both directions, with captions,
download failures, offline catch-up, restart and media faults. Albums and other
formats remain separate slices.

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project (Objective-C metadata, selector stubs,
constant dictionaries, disassembly). No public prior art was used. These are
static observations, not executed fixtures; confidence is high where read
directly from code or constant data and medium where one dispatch layer was
inferred.

### Inbound model

- `LocoChatLogPhoto` (`nameMappingDictionary` at `1017a68b8`) maps `k`→token,
  `w`, `h`, `s`→size, `mt`→mimeType, `cmt`→comment, `rsc`/`rt`/`hfac` resource
  fields, plus `url`, `thumbnailUrl`, `thumbnailWidth`, `thumbnailHeight`,
  `expire` and `tpath`. There is no checksum property: `cs` is not modelled.
- `-[NTChatPhoto update]` (`1017846f8`) stores the comment; `hasComment` and
  search consume it. No path reads the chat-log `message` as a caption.
- `expireAtSecs` divides the stored `expire` by 1000: `expire` is epoch
  milliseconds.
- The chat-list summary (`lastMessageOfChatMessage:forExport:` at `1013b46f4`)
  renders a photo as the localized "Photo" without the caption.

### Download

- The traced LOCO trailer path (`LPTrailerController` `101654d84`,
  `LPTrailerDownloadOperation` `101761e60`) checks the local cache, then sends
  GETTRAILER with the attachment token and DOWN on the trailer host, resuming
  from the temporary file's length.
- Failures: trailer connect `-3393`, nonzero server status passed through, nil
  response `-3404`, a completed download whose length differs from the expected
  size `-1`. No checksum is verified on this path. No automatic retry exists.
- Expiry: a message is marked expired only when it is past `expire` and no local
  cache exists, or when the server reports an expiry-class error. The bubble then
  shows an expired icon and an "original file has expired" alert.

### Outbound

- The send UI adds a non-empty caption to the pending message's extra as `cmt`
  (`extraDictionary:withComment:` at `101805460`).
  `LPTrailerUploadOperation start` (`1015785dc`) copies `cmt` into the POST `ex`
  JSON for types 2 and 3 only. SHIP carries no caption.
- Accepted extensions are jpg, jpeg, png, gif, bmp and webp; size is bounded by
  the server's upload maximum; an oversized image is sent as a file without its
  caption.
- SHIP → POST (server offset drives the streamed range) → COMPLETE. COMPLETE
  replaces the pending message with the server chat log. Any failure or cancel
  stores the error code and marks the message failed (`-2`). Retry is the manual
  resend button only; a stored trailer task lets a resend resume at the server
  offset.

### Recorded gaps

- Production choice among the trailer, URL, Tenth and Talk Cloud download
  strategies, and between trailer and direct HTTP upload, inside the Swift media
  jobs (GAP-D1, GAP-U1). mooo uses the attachment URL for download and the
  trailer chain for upload; both are observed to work live, but the official
  selection logic is untraced.
- The HTTP-path failure branches (403/404/410), the full expiry-error
  classifier, the on-disk cache recipe, the default upload size limit, the
  SHIP-redirect follow-up, status meanings beyond in-progress/failed, the
  notification text and the caption display surface.

## Observed fixture

`research/fixtures/photo/observed-group-caption.json`: owned A (Android
26.8.2) sent one synthetic JPEG with a typed caption into the group while a
scoped read-only B push probe recorded only key sets, value kinds, the
synthetic caption and later the MIME value and expiry magnitude. Findings: the
caption is attachment `cmt`; the chat-log `message` is the literal `photo`;
JPEG photos carry `mt` `image/jpg`; `expire` has 13 digits and lay 14 days
after the send, so it is epoch milliseconds. A PNG observation carried
`image/png` and no `cmt`.

## Production changes and regressions

Each change below was preceded by a failing production-path test.

- **Disconnect during an inbound transfer.** Conversion ran on the SDK portal's
  background context, so a 30-second photo download or Matrix upload outlived
  Disconnect's five-second bound and the source owner was retained. A
  connection lifecycle context now spans Connect to Disconnect; every message
  conversion is linked to it, and Disconnect cancels it before waiting for the
  pump. The regression covers a stalled download and a stalled Matrix upload,
  asserts release within the bound with no commit, and replays the photo once on
  the next connection.
- **Disconnect during an outbound upload.** The dedicated media connection
  ignored context cancellation and waited for COMPLETE until its 60-second I/O
  deadline. Cancellation now closes it. Outbound image sends are linked to the
  connection lifecycle, so Disconnect interrupts them; the result is reported
  as unconfirmed and never retried.
- **Unavailable downloads.** A CDN 403, 404 or 410 was treated as transient,
  pausing delivery and replaying until bounded recovery stopped. These now
  become a committed "unavailable" notice; 429 and 5xx remain retriable.
- **Expiry unit.** Photo `expire` was compared as seconds, so the expiry check
  never fired. It is now compared as milliseconds. Albums share this path; their
  expiry unit is inferred from the shared model, not separately observed. Video
  still compares seconds and needs its own observation.
- **Framework error notices on transient failure.** Every transient media
  conversion failure posted the SDK's generic "An error occurred while
  processing an incoming message" notice, once per replay attempt. Transient
  media errors now wrap the SDK's ignore sentinel; the event still has no
  mapping, so live delivery and history keep source progress.
- **Captions.** Inbound `cmt` becomes the Matrix caption (body) while the
  filename stays `photo.jpg`/`photo.png`. An outbound Matrix image whose body
  differs from its filename sends that body as `cmt` in the POST extra. Captions
  are bounded to 16 KiB of valid UTF-8 without NUL; an oversized outbound
  caption is rejected before any source mutation.
- **MIME type.** Kakao's `image/jpg` reached Matrix verbatim; photos and album
  parts now use `image/jpeg`.
- **Outbound order.** The Matrix image was fetched after the durable
  one-send reservation, so a failed fetch consumed it without a send, and an
  image Kakao cannot accept was reported as "may have been delivered". The image
  is now fetched and validated first. A failed fetch is a certain, retriable
  failure; a non-JPEG/PNG or oversized image is a certain, unsupported failure;
  neither reserves the event or reaches KakaoTalk.

### Deliberate deviations

- mooo verifies the attachment SHA-1 before releasing bytes; the Mac trailer
  path checks only length.
- mooo accepts only JPEG and PNG up to 16 MiB outbound and does not fall back to
  sending a file; the official client also accepts gif/bmp/webp and resends
  oversized images as files.
- mooo neither resumes an interrupted upload nor retries it; it keeps no
  trailer task record. The official client resumes only on a manual resend.

## Owned encrypted acceptance

Method: a fresh build of this branch used the original B secondary profile and
the existing owned encrypted A/B/C regular group, behind a private localhost
fault proxy (Matrix media upload/download faults) and a private HTTPS CONNECT
proxy for the bridge (CDN download cut or stall). Every native send, Matrix
send, fault switch and bridge start/stop wrote a private EXCL 0600 receipt
first; no mutation was repeated. Matrix results were read with the retained
tester device (decryption, sender, caption, MIME, decrypted media SHA-256);
native results with official A (photo viewer: sender, time, caption, order;
saved-file hash for outbound photos) and C.

| Case | Result |
|---|---|
| A captioned JPEG while the bridge was offline, then catch-up | one encrypted image from A's ghost, caption exact, bytes equal to the original |
| C PNG while offline, then catch-up | one image from C's ghost, bytes exact |
| A captioned JPEG while online | caption exact, `image/jpeg`, bytes exact |
| CDN download cut twice, then restored | delivery paused, no notice, cursor and mappings unchanged; bounded recovery delivered one exact image |
| Matrix media upload cut three times, then restored | same: progress held, one exact image after recovery |
| SIGINT while the CDN download was stalled | shutdown in 0.24 s, no commit; next start delivered one exact image |
| Matrix → Kakao captioned JPEG | one mapping; native A shows the caption and the saved file hash equals the source |
| Matrix → Kakao PNG | one mapping; native A saved file hash equals the source, no caption |
| Matrix media download cut for an outbound image | certain "not sent" status, no reservation row, no Kakao send |
| SIGINT while the outbound Matrix fetch was stalled | shutdown in 0.24 s, no Kakao send; the cancelled fetch posted the certain "not bridged … not sent to KakaoTalk" status before exit |
| Restarts between cases | no duplicate Matrix events; durable cursor equal to the latest mapping |

Native A's viewer listed all eight bridged photos once, in source order, with
the expected senders and captions; Matrix showed the same order with no
duplicates and no framework error notices. A received the outbound caption,
so the server keeps `cmt` from the POST extra (the COMPLETE chat log itself was
not inspected).

### Acceptance gaps

- Expired and unavailable downloads are covered by production-path tests only;
  a real expired photo or CDN refusal was not produced live (the CONNECT proxy
  cannot forge HTTP statuses without interception).
- The Disconnect-during-upload case was exercised live on the Matrix fetch
  stage; interrupting the Kakao media upload itself is covered by client and
  connector tests only.

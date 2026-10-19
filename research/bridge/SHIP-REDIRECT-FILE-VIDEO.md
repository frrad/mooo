# SHIP redirect for files and video

Evidence date: 2026-10-19. Follows [SHIP redirect photo upload](SHIP-REDIRECT.md),
which covers the shared prepare, Kage and WRITEMEDIA steps for photos. Related:
[media upload stream](../media-upload-stream.md) (section 9 found the redirect for
files and video) and frrad/mooo#428.

Sources, in order of weight:

1. **KakaoTalk for Mac 26.8.0 arm64**, static Ghidra analysis: the direct-upload
   coordinator, the prepare/append/ready builders and parsers, the Kage helper,
   media-key selection, the shared WRITEMEDIA write-data consumer with its file and
   video classes, and the shared caption-extra consumer. Function names come from
   the binary's own Swift `#function` strings.
2. **KakaoTalk for Android 26.8.2**, static JADX reading, as a supplement: the
   chat uploader and upload policy, the HTTP upload implementation, the
   WriteMediaJob and MediaForm serializers.
3. **One owned-account observation** on the disposable B account (section 6),
   with a private probe that sent SHIP, prepare, every Kage chunk and the ready
   poll, but never WRITEMEDIA.

No public prior art was consulted. Addresses and decompiled code stay in the
private lab notes.

## 1. Routing

- On the Mac, files (type 18) and videos (type 3) enter the same direct
  coordinator as photos when SHIP answers `rd: true`. Large files and videos
  (types 29 and 28) skip SHIP and always go direct. Android routes the same way.
  It also sends file and video straight to HTTP without SHIP when its size policy
  rates them large (Drawer only). (High.)
- After prepare, the strategy depends only on the response's `resourceType`, not
  on the media type:
  - `vk` is a chunked Kage upload;
  - Android names `vt` and `vtl` (Tenth): one raw PUT from the offset to the end;
    the Mac sends anything other than `vk` to its Tenth uploader.
  (High.)
- The ready poll (section 4) runs whenever prepare returns `needReady: true`, for
  any media type and strategy. (High.)

## 2. Prepare

- Same endpoint and headers as for photos: authorized `POST
  https://talk-pilsner.kakao.com/messaging/files/v1/prepare`, JSON `chatId`,
  `mediaType`, `size`, `extension`. Neither client adds a filename, checksum,
  duration or dimensions. (High, both clients.)
- `mediaType` is `"file"` for type 18 and `"video"` for type 3; photos send
  `"image"`. (High, both clients; observed in section 6.)
- `extension` is the local name's extension, Unicode-normalized and lowercased.
  On the Mac an empty extension fails before any request, so a file without an
  extension cannot go direct. (High.)
- The response has the same six fields as for photos: `url`, `resource`,
  `resourceType`, `uploadKey`, `chunkSize`, `needReady`. (High; observed.)
- With a stored resource, both clients resume through `/messaging/files/v1/append`
  instead. mooo has no resend, so resume is out of scope.

## 3. Kage upload

- Same as for photos: multipart `offset` (decimal start of the chunk) and
  `file_1`, whose filename is the last path component of `resource`; the signed
  URL gets no account Authorization or cookies. (High; observed with the Mac's
  starting offset.)
- The part's `Content-Type`:
  - Mac: the system's preferred MIME type for the extension, else
    `application/octet-stream`;
  - Android: a coarse type from the chat type (`video/*`, `application/*`);
  - observed: `video/mp4` and `application/octet-stream` were accepted.
  (High.)
- Responses observed in section 6:
  - an intermediate chunk answers `{"offset": "<end of range>", "msg": "ok"}`, as
    for photos;
  - a file's final chunk answers `{"msg": "ok", "info": [{filename, length,
    content_type}]}`, with the resource filename, the full length and
    `content_type` `"etc/dat"` for a `.dat` file (photos return `"image"`);
  - a video's final chunk answers only `{"msg": "ok"}`, with no `info`.
- Neither official client reads the final acknowledgement's `info` or
  `content_type`. The Mac checks the HTTP result. Android treats any 2xx as
  success and ignores the body. (Medium-high Mac, high Android.)
- No separate thumbnail is uploaded for video. (Medium-high.)

## 4. Ready poll

- Authorized `POST https://talk-pilsner.kakao.com/messaging/files/v1/ready`,
  JSON `resource`, `resourceType`, `chatId`, `mediaType`, `uploadKey`,
  `prevStatus` and an optional `jobId`. (High, both clients.)
  - The Mac sends `prevStatus` 0 first and then the last status returned; Android
    omits it on the first poll.
  - `jobId` starts from a Tenth upload's transcode job and is then replaced by any
    `jobId` in a ready reply. A Kage upload has none.
- The reply carries `status`, `interval` (milliseconds), `maxWaitSeconds` and an
  optional `jobId`. (High; observed.)
  - Android's status names: -1 unknown, 0 start, 1 done, 2 encode trigger,
    3 Argos trigger, 4 Argos poll, 5 encode poll.
  - `status` 1 means ready.
  - Any other status sleeps `interval` and polls again, until `maxWaitSeconds`
    has passed since the first poll; then the upload fails. The Mac's defaults
    are a 5000 ms interval and 600 s.
- An error reply with code `"90025"` is the content filter (illegal content).
  Both clients fail the send without a write. (High.)
- Observed for video: two and four polls. Each returned `interval` 1000 and
  `maxWaitSeconds` 600, then `status` 5 until `status` 1 after about 1.6 s and
  3.8 s. Files returned `needReady: false`, so they had no poll.

## 5. WRITEMEDIA

Same request as for photos (`chatId`, `msgId`, `scope`, `type`, the `noSeen`,
`noLight` and `silence` flags, then one nested media object and an optional
`extra` string), with the message's own type and these nested objects. (High,
both clients agree.)

| Type | Media key | Nested fields |
|---|---|---|
| 2 photo | `photo` | `resource`, `resourceType`, `hfac`, `width`, `height` |
| 3 video | `video` | `resource`, `resourceType`, `hfac` |
| 18 file | `file` | `resource`, `resourceType`, `hfac`, `name`, `checksum` |

- `hfac` is the prepare `uploadKey`.
- A file's `checksum` is the SHA-1 of the whole file as lowercase hex. On the Mac
  the coordinator computes it before the upload. The LOCO trailer SHIP carries
  the same digest as uppercase hex.
- A file's `name` is the message's file name. Its origin in the Mac file sheet
  was not traced (gap G2).
- A video carries no width, height, duration, size or thumbnail. Android measures
  them but does not send them.
- A caption (video only, as on the trailer path) is the JSON `extra` string with
  `cmt`, as for photos. Files carry no caption.
- Android omits `noLight`; the Mac sends it. mooo follows the Mac.

The response and its persistence are the same as for photos: no media-type
branch was found.

## 6. Live observation (2026-10-19)

A private probe on the owned disposable B profile drove mooo's real SHIP request
and session, and then sent prepare, Kage chunks and ready directly. It sent no
WRITEMEDIA, so no message was created. The uploads were:

- two random `.dat` files of 64 KiB and 9 MiB;
- two ffmpeg test-pattern `.mp4` videos of 17,281 and 25,367,829 bytes.

The run recorded only key names, value types, protocol constants and
comparisons. Results
([fixture](../fixtures/media-upload/observed-kage-file-video.json)):

| Upload | SHIP | Prepare | Chunks | Final ack | Ready |
|---|---|---|---|---|---|
| file 64 KiB | `rd: true` | `vk`, 4 MiB, `needReady: false` | 1 | `info` with `etc/dat` | none |
| file 9 MiB | `rd: true` | `vk`, 4 MiB, `needReady: false` | 3 | `info` with `etc/dat` | none |
| video 17 KB | `rd: true` | `vk`, 4 MiB, `needReady: true` | 1 | `msg` only | 5 then 1 |
| video 25 MB | `rd: true` | `vk`, 4 MiB, `needReady: true` | 7 | `msg` only | 5, 5, 5, 1 |

- Every chunk answered HTTP 200.
- Every intermediate `offset` equalled the end of its range.
- Each file's `info` named the resource filename and the full length.
- No Tenth strategy was offered.

### Production acceptance (2026-10-19)

After the implementation landed in mooo, a private harness sent through the
production `Client.SendUpload` on the same account, into the owned A–B direct
chat:

- **A 6 MiB file (two chunks).** WRITEMEDIA type 18 returned a position. The
  stored attachment carries `name`, `cs`, `s`, `k`, `url`, `rsc`, `rt` and
  `hfac`. mooo's history read decoded the message as a file, and the download
  was byte-identical.
- **Four videos with captions (25 MB/7 chunks, 12 MB/3 chunks, 17 KB twice).**
  Each polled ready and then received a WRITEMEDIA type 3 position. The stored
  attachment carries `cmt` (the exact caption), `tk`, `url`, `s`, `w`, `h`, `d`,
  `rsc`, `rt`, `hfac` and `expire`.
  - The server re-muxes the video. The stored size differs slightly from the
    upload, and `cs` is an **empty string**.
  - `w`, `h` and `d` match the source.
  - A downloaded 17 KB copy had the same stream as the upload (h264, 320×240,
    3.0 s, 75 frames).
- **One transient failure.** The first attempt of the 25 MB video failed with a
  transport error before any bytes were sent. It was reported as certainly not
  sent and not retried; the next attempt succeeded.
- **Not covered:** this run did not check how the official recipient displays
  the messages.

The empty `cs` matters for receiving: mooo's video decoder required a 40-digit
checksum, so it read these stored videos as undecodable gaps. That is separate
from sending; see gap G7.

## 7. Failure and certainty

The server creates the message only on WRITEMEDIA. Both clients fail before
WRITEMEDIA, and so certainly create no message, when any of these fail:

- local checks: bad local file, empty extension;
- prepare: refused or malformed;
- upload: any refused or malformed chunk;
- ready: a ready error, including the 90025 content filter, or the ready
  deadline.

A lost or failed WRITEMEDIA reply is ambiguous. Neither client retries
automatically. The Mac's only loop is the bounded ready poll. Android's resend
is a manual or scheduled resume of the stored resource.

## 8. Requirements for mooo

1. On `rd: true` for a file or video, use the same prepare, Kage and WRITEMEDIA
   path as for photos, with `mediaType` `"file"` or `"video"`.
2. Send the part `Content-Type` for the extension, falling back to
   `application/octet-stream`.
3. Check every intermediate `offset`, as for photos. For the final chunk:
   - require `msg` `"ok"`;
   - when `info` is present, require exactly one entry with the resource filename
     and the full length;
   - do not require a `content_type` value, and accept a final reply without
     `info`. This is stricter than both official clients, which do not read the
     body, and it accepts every observed reply.
4. When `needReady` is true, poll ready as in section 4 before WRITEMEDIA. Bound
   the wait by `maxWaitSeconds` (at most the 600 s default) and by the caller's
   context. Treat 90025 and every other ready failure as certainly not sent.
5. WRITEMEDIA uses the media key and nested fields from section 5. Files add
   `name` and the lowercase SHA-1 `checksum`; video adds nothing beyond the
   resource triple; a video caption goes in `extra` `cmt`.
6. Keep failing closed on a Tenth `resourceType` until a Tenth PUT has been
   observed. It is certainly not sent.
7. Any failure before WRITEMEDIA is certainly not sent. A failure after it is
   ambiguous. Nothing is retried.

## Gaps

- **G1.** The server offered only `vk`. The Tenth PUT, its headers and its
  transcode job are untraced live.
- **G2.** The Mac's source for the file `name` and for `size` (set when the file
  message is created) was not traced. mooo uses the Matrix file name and the byte
  length.
- **G3.** WRITEMEDIA for file and video passed live through mooo (section 6).
  The official recipient's view was not checked.
- **G4.** The meaning of ready statuses 0 and 2-4 beyond Android's names, and
  whether the server ever returns `jobId` for a Kage upload.
- **G5.** The [photo doc](SHIP-REDIRECT.md) says Android sends the ending offset.
  This trace read the multipart offset as the chunk's starting offset (the cursor
  advances only after a 2xx). The Mac's starting offset is the observed one.
- **G6.** Large types 28/29, the Drawer route and the secret-chat variant were
  not followed.
- **G7 (resolved).** A video stored through the HTTP upload has an empty `cs`
  and a server-remuxed size. mooo's inbound video decoder used to reject the
  empty checksum, so such videos reached Matrix as gaps.
  - It now accepts an empty `cs`, as the official clients do: neither one reads
    a received video's or file's `cs` (see [video](../video.md)).
  - A live resend decoded as a video with its caption, and its download passed.
  - mooo still verifies a non-empty `cs`.

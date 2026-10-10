# Regular-group outbound files, video, audio and albums

Evidence date: 2026-10-10. Scope: Matrix `m.file`, `m.video`, `m.audio` and
`com.beeper.gallery` events sent into a bridged regular group. Single photos
are covered by [group photos](GROUP-PHOTOS.md); the upload transport by
[media transfer](../media-transfer.md).

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project; no public prior art. High confidence
unless marked.

- **Files, video and audio** use the single-photo chain: `SHIP` on the main
  session, `POST` on a dedicated media connection, the byte stream from the
  server's offset, then a server-pushed `COMPLETE` carrying the chat log. Only
  `t` changes (18 file, 3 video). `SHIP` carries `c`, `s`, `t`, `cs` (SHA-1 of
  the whole file), `e` (lowercased extension) and `ex`. `POST` adds `f`, the
  NFC-normalized file name, which is the only way the server learns the name;
  `w`/`h` are zero outside photos. No thumbnail, duration or dimensions are
  sent: the server derives them.
- **Classification is by lowercased extension.** Video: ts, ogv, flv, mov,
  mp4, mpeg, mpg, mkv, wmv, asf, avi, m4v. Everything else that is not a
  photo is sent as a file (type 18), including audio files: the Mac client has
  no voice recorder and only re-sends existing type-5 audio. Only video carries
  a caption (`cmt` in `POST ex`); a file's caption is dropped.
- **Deny list.** A server-updated list with a built-in fallback of 167
  executable, script and macro extensions (exe, bat, js, sh, jar, apk, docm,
  xlsm, …) is checked before sending.
- **Albums** use a different protocol: `MSHIP` (`c`, `t`=27, `sl`, `csl`,
  `el`, `ex`) returns aligned per-photo lists (`kl` tokens, `mtl` MIME types,
  `vhl` hosts, `pl` ports); each photo is uploaded in order with `MPOST`
  (`u`, `k`, `t`, `s`, `scp`, `mm`, `nt`=0, `os`, `av`, `dt`=2; no chat ID,
  extra or name) and its `COMPLETE` carries no message; finally an ordinary
  `WRITE` with `type`=27, empty `msg` and an `extra` of `kl`, `wl`, `hl`,
  `mtl`, `sl`, `csl`, optional per-photo captions `cmtl`, and the sender's
  local paths as `imageUrls`. Albums hold 2 to 30 photos (jpg, jpeg, png, gif,
  bmp, webp); a lone leftover photo is sent singly.
- **Limits** come from server configuration: images 20 MiB (`upMaxSize`),
  files and video 300 MiB (`videoUpMaxSize`), with separate large-media paths
  above a threshold.
- **Failures.** No server status is special-cased and nothing is retried;
  the message is marked failed with a manual resend. Albums resume per photo
  on a manual resend.

Recorded gaps: how the Swift upload job chooses this path over a direct HTTP
upload, the large-file and large-video branches, the defaults of the
`useCollageImage` and `useOriginalVideoSending` settings, per-room allowed
types, file-name length rules, the failure UI text, and the deny-list refresh
endpoint.

## Production behavior

Each change was preceded by a failing production-path test.

- `m.file` and `m.audio` are sent as KakaoTalk files and `m.video` as video,
  classified by extension like the Mac client (a `.mp4` sent as `m.file`
  becomes video). The Matrix filename (or body) is NFC-normalized and sent as
  `f`; a video caption is sent as `cmt`.
- Before the Matrix download: declared size, name, the built-in deny list and
  captions on files are checked. After the download, the bytes are validated
  before the durable one-send reservation, then the upload runs once. Matrix
  download failures are certain and retriable; rejections are certain
  failures with a notice; transport failures after the reservation are
  ambiguous and never retried.
- A `com.beeper.gallery` of 2 to 30 JPEG or PNG photos becomes one album
  (`MSHIP`, `MPOST` per photo, one type-27 `WRITE`); the gallery caption is
  attached to the first photo, and `imageUrls` are omitted. A one-photo
  gallery is sent as a photo. A failure before the `WRITE` creates no message
  and is reported as certainly not sent; a failure on the `WRITE` is
  ambiguous.
- Files, video and albums cannot be sent as replies; the bridge rejects them
  before any request.
- Room capabilities advertise files, audio and video (captions only on video)
  and galleries.
- **Regression:** an album sent from Matrix is recorded as one whole-message
  row. When catch-up later delivered the same album from KakaoTalk, the
  album's part-resume logic treated it as missing every part and posted both
  photos again. A recorded whole-message row now marks the album complete.

### Deliberate deviations

- mooo buffers uploads in memory and limits files and video to 64 MiB, photos
  to 16 MiB and albums to 64 MiB in total (Mac: 300 MiB and 20 MiB). Albums
  accept JPEG and PNG only.
- mooo has no server deny list and always applies the built-in one.
- A captioned Matrix file is rejected rather than sent without its caption.
- More than 30 gallery photos are rejected rather than split into several
  albums, keeping one source message per Matrix event.

## Owned encrypted acceptance

Method: a fresh build of this branch with the original B secondary profile in
the existing owned encrypted A/B/C regular group. Every Matrix send and bridge
start/stop wrote a private EXCL 0600 receipt first; no mutation was repeated.
Matrix events came from the tester device; native results were read from
official A, and downloaded bytes were compared by SHA-256 against A's app
cache.

| Matrix event | Result on native A |
|---|---|
| `m.file` text file | file card from B with the exact name and size; downloaded bytes identical |
| `m.video` MP4 with caption | video bubble with server-derived duration; player opened; caption shown exactly; the server re-encoded the video (51.9 KB delivered from a 42.9 KB source) |
| `m.audio` M4A | file card with the exact name; downloaded bytes identical |
| `com.beeper.gallery` PNG + JPEG with caption | one two-photo album; caption on the first photo; both photos byte-identical |
| `m.file` with `.exe` | Matrix notice "does not allow this file type"; nothing sent |
| `m.file` with a caption | rejected by the bridge framework's capability check ("captions are not supported here"); nothing sent |
| Restart after the first album | **failure:** catch-up delivered the album again as two images from B's ghost (fixed above; the two events remain) |
| Second gallery on the fixed build, then restart | one album on A; catch-up delivered no new Matrix events and the cursor reached the album |

### Acceptance gaps

- Video bytes cannot be compared because the server re-encodes; playback and
  caption were checked instead.
- Upload faults (interrupted transfer, Disconnect during an upload) were
  covered for photos in the photo slice and share the same transport code;
  they were not repeated live for files or albums.
- Large media, more than two album photos, GIF/WebP album photos and voice
  messages (MSC3245) were not exercised.

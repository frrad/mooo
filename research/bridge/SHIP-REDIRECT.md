# SHIP redirect photo upload

Issue [#342](https://github.com/frrad/mooo/issues/342). Evidence date: 2026-10-13.
Scope: single JPEG/PNG photos, including chunked Kage uploads. Rhino was used
only for the earlier read-only log inspection; reproduction and implementation
ran locally against owned disposable accounts.

## Failure and correction

Production `Client.SendImage` reproduced `SHIP status: 0`, `rd: true`, `k: ""`,
`vh: ""`, `p: 0` on a fresh synthetic image in a roster-verified owned direct
chat. This is an alternate upload instruction, not a malformed trailer endpoint.
The [observed reply](../fixtures/photo/observed-ship-redirect.json) contains no
account identifiers or authorization. The interim handling change recognizes
`rd` and stops the trailer path before POST or a message write.

The alternate uploader's initially observed HTTP 404 was a research-harness
error: **`file_1` must use the last path component of the prepared resource key
as its multipart filename, rather than the local image filename.** Mac's full
upload caller derives that filename before invoking its multipart request layer.
Fresh requests using the local filename returned `Not found upload ready key.`;
a fresh request using the derived filename returned HTTP 200. See the
[observed filename fixture](../fixtures/photo/observed-kage-filename.json).

An earlier isolated official-request control also returned 404 because it was
fed the incorrect filename manually. It bypassed the coordinator that derives
the filename, and was not a valid official end-to-end send. It provides no
evidence of an external outage or account policy. Cookie preservation, complete
Brew headers, same-session SHIP/prepare/upload, a larger JPEG, delayed upload and
Android's ending-offset/form ordering did not resolve that harness error.

## Implementation-neutral contract

1. Validate the photo and caption before sending SHIP. On success, inspect `rd`
   before trailer fields. Omitted `rd` defaults false; a wrong BSON type fails.
   A true redirect supplies no trailer endpoint. Keep the same LOCO session.
2. Send one authenticated POST to
   `https://talk-pilsner.kakao.com/messaging/files/v1/prepare`, JSON
   `chatId`, `mediaType: "image"`, `size` in bytes and lowercased `extension`.
   The Mac builder executed offline confirms this request:
   [executed prepare fixture](../fixtures/photo/executed-prepare-request.json).
   Brew sends Authorization = access token + `-` + hashed device UUID; A,
   talk-agent (`macos/` + application version), talk-user-id and talk-language.
   It also sets the current IANA timezone as TZ. mooo omits that optional header;
   successful controlled production sends validate that omission in this path.
3. Read `url`, `resource`, `resourceType`, `uploadKey`, `chunkSize`, `needReady`.
   The observed strategy is `vk`, signed HTTPS `kage.talk.kakao.com/offset-up/`,
   4 MiB chunks and `needReady: false`. These are private ephemeral values.
4. Derive the multipart filename from `resource`'s last path component. POST
   `offset` as decimal text followed by `file_1` with that filename, the image
   MIME type and exactly the next chunk. The Mac uploader sends the starting
   offset; Android 26.8.2 sends file first and the ending offset (a later
   trace read it as the starting offset; see gap G5 of the
   [file/video doc](SHIP-REDIRECT-FILE-VIDEO.md)). mooo follows
   the independently traced Mac ordering/offset, which passed live acceptance.
   Send no account Authorization or account cookies to the signed upload URL.
5. Intermediate chunk responses contain an `offset` string. Require progress to
   equal the end of the submitted range. A final response contains `msg: "ok"`
   and one `info` entry identifying the filename, complete byte length and
   `content_type: "image"`. Require all three to match before creating a message.
   The [observed two-chunk fixture](../fixtures/photo/observed-kage-chunks.json)
   covers the 5,884,063-byte photo and its acknowledgements.
6. Send exactly one `WRITEMEDIA` on the existing LOCO session, not trailer
   COMPLETE or a generic WRITE. The request carries `chatId`, `msgId`, `scope: 1`,
   `type: 2`, `noSeen: false`, `noLight: false`, `silence: false`, and a nested
   `photo` object with `resource`, `resourceType`, `hfac` (the upload key), `width`
   and `height`. The optional caption remains a JSON `extra` string containing
   `cmt`. No `msg` or `f` is added for this fresh-photo branch.
   [Executed write model](../fixtures/photo/executed-write-photo.json) and
   [executed photo-data consumer](../fixtures/photo/executed-photo-write-data.json)
   establish the two serialization layers independently.
7. Check the server status, selected chat identity and positive log ID/send time.
   Retain a returned `chatLog` when present; a response without it may supply the
   ordering fields directly. The bridge records the server position through its
   existing image-send mapping path. Upload failure before WRITEMEDIA is certain
   not-sent, even if storage received bytes. A lost or malformed write reply is
   ambiguous; do not retry any upload or write automatically.

The current implementation supports this evidenced Kage photo strategy. Tenth,
processing-readiness polling and legacy unchunked tickets fail closed before a
message write. Files and video follow
[SHIP redirect for files and video](SHIP-REDIRECT-FILE-VIDEO.md), which also adds
the ready poll. Albums remain separate work, and this does not claim parity for
the official manual-resume UI.

## Official Mac chain and persistence

Read-only Ghidra inspection of authorized macOS 26.8.0 build 2000, supplemented
by the isolated synthetic executions above:

- The successful SHIP trailer callback checks redirect, clears completion and
  progress handlers, skips the trailer task/endpoint, and completes with
  success=false, redirect=true, error=nil. Controller completion removes the
  upload-map entry. Swift converts redirect into its dedicated error case; the
  upload coordinator catches only that case and enters direct upload.
- Prepare's callback reads all six ticket fields. The coordinator stores the
  resource, type and upload hash in pending-message extra. The Kage helper derives
  MIME from the local image and filename from the remote resource; it chunks the
  local file and consumes the offset/final upload completion.
- `NTChatPhoto`'s write-data consumer reads pending extra `rsc`, `rt`,
  `__ext__uploadHash`, `width` and `height` and produces the canonical nested
  photo object above. A separate caption-extra consumer and the WRITEMEDIA model
  assemble the final request. The request/response path is separate from COMPLETE.
- The WRITEMEDIA response callback checks success, advances the last-token ID
  and forwards the response. The higher completion schedules a database block
  before forwarding completion: it converts the returned chat log into a stored
  message, updates that message, applies mention handling and room summary state,
  updates the sender watermark except at scope 2, and handles scope-3 sync.
  It looks up the pending message by chat ID/msg ID, transfers/caches its local media for the accepted message
  and deletes the pending entry. Thread updates remain conditional on positive
  room/thread IDs. The existing photo storage/view consumers retain comment,
  resource metadata and image properties; see [group photos](GROUP-PHOTOS.md).
- A missing/non-successful write response bypasses that success persistence
  block and is forwarded as failure. The direct coordinator's continuation
  throws on nil/non-success, cleans up its resources and propagates the error.
  There is no successful completion inferred from uploading bytes alone.

These static persistence observations are not executed database/UI parity
fixtures. The production implementation follows its existing bridge mapping and
send-reservation policy instead of reproducing the official pending-message DB
or manual resume UI. No proprietary source or account-specific artifacts are
published.

## Validation

A production-path regression failed with the interim `ErrUploadRedirect` before
the fallback was implemented, then passed through SHIP, prepare, upload and one
WRITEMEDIA. Synthetic protocol regressions consume the executed/observed fixtures,
check signed-URL and filename validation, exact chunk ranges and acknowledgement
progress, and redact transport failures. Failure tests distinguish a refused
upload from a lost WRITEMEDIA reply and verify no automatic resend.

Owned-account production sends passed for a fresh small PNG, a fresh JPEG and a
5.9 MB PNG spanning two chunks. Each returned a valid source position; a bounded
read of that stored message downloaded byte-for-byte identical content with the
exact caption and owned sender. The recipient Android 26.8.2 client displayed
the small/large PNGs once, with their exact captions in the image viewer. No new
secondary enrollment or profile cloning was used. A separate official Android
original-quality JPEG control also delivered exact bytes. This is local client
and recipient validation, not a live Beeper deployment test.

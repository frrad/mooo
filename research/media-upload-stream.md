# Media upload byte stream, limits and failure handling

Research date: 2026-10-19. Issue: frrad/mooo#428.

Sources, in order of weight:

1. **KakaoTalk for Mac 26.8.0 arm64**, the authorized official binary, read
   only by static Ghidra analysis (resolved selector calls, disassembly,
   constant reads and private parity reports).
2. **KakaoTalk for Android 26.8.2** base APK, decompiled with JADX, as a
   supplement. Obfuscated identifiers are not quoted; recovered Kotlin file
   names are given where they help.
3. **mooo** on main at `2b7bd15a`, driven through its real upload function
   with the scripted peer.
4. Earlier **owned-account observations** already recorded in
   [group photos](bridge/GROUP-PHOTOS.md) and
   [group outbound media](bridge/GROUP-OUTBOUND-MEDIA.md).

No public prior art was consulted, and no new live experiment ran for this
note (see gap G1). Addresses, pseudocode and decompiled code stay in the
private lab notes.

This note covers what happens after `SHIP`: the dedicated media connection,
`POST`/`MPOST`, the byte stream, `COMPLETE`, and the size limits and failure
handling around them. Request and response fields are in
[media transfer](media-transfer.md) and
[group outbound media](bridge/GROUP-OUTBOUND-MEDIA.md).

## 1. The mooo failure (reproduced)

`uploadToMediaServer` encrypts everything from the server's offset to the end
of the file as **one** V3 secure frame. One frame's plaintext is capped at
`DefaultMaxCiphertext - 12 - 16` = 16 MiB + 22 bytes (16,777,238). A
throwaway test drove the real function against the scripted media peer:

| File size | POST offset | Result |
|---|---|---|
| 16,777,238 | 0 | streamed in one frame; COMPLETE read |
| 16,777,239 | 0 | `client: media encrypt: loco: ciphertext too large` |
| 16,777,239 | 1 | streamed (the remaining 16,777,238 bytes fit) |
| 22,276,883 (the size in the issue) | 0 | `ciphertext too large` |

- The threshold applies to the remaining bytes, not the file size.
- In every failing case `SHIP` had already run on the main session, and the
  media peer had read and accepted exactly one `POST`.
- The bridge connector passes the error through `outboundSendError`. It is
  not a `StatusError`, `media.ErrUploadTooLarge` or `ErrPhotoNotCreated`, so
  the user gets the `uncertainFailure` notice ("may have been delivered").
  That is wrong: nothing was streamed. `uploadRejectedStatus` and
  `certainRefusal` are never reached.
- The scripted media peer used to read only one secure frame, with the same
  per-frame cap. This PR adds `(*Peer).ReadStream`, which reassembles a
  multi-frame stream and reports each frame's size.

## 2. Chunking the post-POST stream

### Mac (high confidence)

- The single-upload operation and the album operation both stream through
  the same `LocoTrailerAgent`, a `LocoAgent` subclass. The connection is
  opened to the `SHIP` host/port with **secure-layer type 2**, hard-coded at
  the trailer connect call. That is the same 4-byte-length-plus-AES-GCM
  framing as the main session.
- The agent opens the local file, seeks to the offset, and sets the write
  position to the offset and the total to the file size.
- Each step reads **4096 bytes** (or what remains) and encrypts that chunk
  alone as **one secure frame**. It submits the frame with socket write
  timeout -1 (none) under its own tag; tags start at 2^62 - 1 so they never
  collide with packet IDs.
- **One chunk in flight.** The complete-write callback clears the tag. While
  position < size, it reports progress and submits the next chunk. Otherwise
  it reports "data written", closes the file and starts reading the next
  packet header (COMPLETE).
- Chunks count from the offset, not from 4096-byte boundaries of the file.
  The size does not vary by media type or file size; nothing in the path
  reads a configured block size.

### Android (medium-high confidence)

- The POST job reads `o` and streams the file from that offset through an
  okio buffered sink. The connection sink is buffered, then V2SL-encrypting,
  then buffered again over the socket. The V2SL sink encrypts **each write
  call as one frame**.
- The buffered-sink loop reads the file one 8192-byte segment at a time and
  emits each complete segment immediately. So Android frames are
  **8192 bytes**, plus a final remainder on flush.
- Android's V2SL **receive** side refuses any frame whose length prefix is
  over **131,068 bytes** (nonce + ciphertext + tag): `MAX_BLOCK_SIZE` in
  `SecureLayer.kt`.
- Android can also run LOCO over TLS 1.3 instead of V2SL. That transport was
  not followed.

### What the server accepts (observed only in part)

- Both official clients send many small frames.
- The live media server has already accepted at least one larger frame from
  mooo: the 180,340-byte Matrix-to-KakaoTalk PNG in the
  [group photos](bridge/GROUP-PHOTOS.md) acceptance, which arrived with a
  matching hash. That frame is above Android's receive bound.
- The server's own upper bound is unknown (G1). A multi-frame upload from
  mooo has not been observed live (G1).

## 3. Deadlines and timeouts

### Mac (high confidence for the logic, medium for the values)

- The trailer connection takes four timeouts from the main LOCO session's
  `LocoConfig`: connect, receive-header, in-segment, out-segment. The only
  `LocoConfig` construction site passes the constants **15 / 20 / 10 / 10
  seconds**, and the LocoConfig setters for them are called only by the
  LocoAgent initializer.
- GETCONF's `wifi`/`wwan` objects (`LocoConnInfo`) do carry `connTimeout`,
  `recvHeaderTimeout`, `inSegTimeout` and `outSegTimeout`. The client
  archives them, but no traced code applies them to `LocoConfig` or an agent.
  So the 10 s is effectively a client constant (G2).
- **No overall deadline, and none that scales with size.** Socket writes have
  no timeout.
- **The progress watchdog is the out-segment timer.** Every partial-write
  callback restarts it (disable, then enable). Every complete-write callback
  disables it.
- When the watchdog fires, the agent disconnects. The disconnect fails the
  operation unless the user cancelled it.
- `POST` is sent like any request, so its reply is bounded by the 20 s
  receive-header timer.
- After the last chunk, the agent reads the next header **without** arming
  the receive-header timer, and that read has socket timeout -1. The
  in-segment watchdog only bounds a packet body once its header has arrived.
- Neither `LPTrailerUploadOperation` nor `LPTrailerOperation` has a timer of
  its own.
- So the COMPLETE wait ends only when:
  - the socket closes (server close or TCP error);
  - the user cancels the upload;
  - a manager-wide cancel-all stops the upload, download and mini queues
    (its trigger is untraced, G3).

### Android (low-medium confidence)

- The connect timeout comes from the connection setup.
- LOCO jobs run under a timeout chosen by the caller, but the media POST and
  COMPLETE timeout value was not traced (A2).
- `COMPLETE` is declared a push method, so Android waits for it as a push,
  not as a reply.

## 4. Resume offset

- **Mac:** a stored ticket lets a manual resend skip `SHIP`. A sent media
  message keeps its trailer task (ticket, host, port). On resend, the upload
  entry point looks up the task by its stored key. If the ticket has not
  expired, it is reused; otherwise a fresh `SHIP` runs.
  - The `POST` reply's offset then drives streaming: less than the size
    streams the rest, equal to the size sends nothing and waits for
    COMPLETE, greater than the size does nothing (the operation stalls
    silently).
  - The local file size is read from the file system before the comparison.
  - The expiry test itself (`tokenExpireTime` from the GETCONF trailer
    object) was not traced (G4).
- **Android:**
  - The offset is read only when the server sends `o` as a 32-bit integer
    (otherwise it is treated as 0).
  - The file must exist, be readable and match the declared size ("Invalid
    file").
  - An offset outside 0..size fails with "Offset out of bounds".

## 5. COMPLETE and failure handling

### Mac, single upload

| Situation | Result |
|---|---|
| no response object | failure, client status -200012 |
| `POST` status ≠ 0 | failure with that status |
| `COMPLETE` status ≠ 0 | failure with that status and the server's `errMsg` when present |
| `COMPLETE` status 0 | the pending message is replaced with the returned `chatLog` and persisted |
| disconnect, including the watchdog | failure, or cancellation if the user cancelled |

Nothing is retried automatically. The message is marked failed and offers a
manual resend (section 4).

### Android

- One classifier decides whether a failed send is abandoned or kept for
  another attempt.
- Abandoned outright: oversize, Drawer-needed, upload/argument errors,
  certain cipher and secret-chat errors, and any nonzero LOCO status.
- Other failures, such as transport errors, may keep the sending log for a
  later resend (its retry policy was not traced, A3).

### Certain versus ambiguous

The official clients do not tell the user which case applies; they show
"failed" and offer a resend that resumes. A bridge has no resend button and
must say whether the message may exist. The server creates the message only
when it sends `COMPLETE` with the chat log, after the last byte. So:

- **Certain, not sent:** any failure before the media connection carries
  bytes. That covers a refusal before `SHIP`, `SHIP` failure, media connect
  failure, nonzero `POST` status, and a local error before the first frame
  (such as the #428 encrypt error).
- **Certain, refused:** a nonzero `COMPLETE` status.
- **Ambiguous:** the connection drops or times out after bytes were written
  but before `COMPLETE` arrives. The server may have finished the upload and
  created the message.
- **Inferred, not observed:** a connection closed after an accepted `POST`
  but before any byte creates no message (G6).

## 6. Maximum upload size and where it is checked

All three limits come from the `trailer` object in the GETCONF response.
GETCONF renames only one key (`3g` → `wwan`), so `trailer` maps to
`LocoTrailerInfo` by property name. The client archives the object in its
preferences and reads it back from there. Each value is used only when it is
above zero; otherwise a built-in default applies (high confidence):

| Use | Server field | Type | Built-in default |
|---|---|---|---|
| Image sent as a photo | `trailer.upMaxSize` | int32 | 20 MiB (20,971,520) |
| File or video, and the large-media threshold | `trailer.videoUpMaxSize` | int32 | 300 MiB (314,572,800) |
| Large media | `trailer.largeUpMaxSize` | int64 | 1 GiB (1,073,741,824) |

- An image above the image limit is sent as a file.
- The 1 GiB limit applies only when all three hold: the caller allows large
  media, the client reports large media as available, and the account has an
  activated Drawer subscription. The send-path size check passes "allowed"
  unconditionally; the file sheet passes its own flag.
- **The check runs before `SHIP`.** The Mac send sheet sorts chosen files
  into valid, oversized, zero-size, bad-extension, folder and missing
  groups, and only valid files go on. The size check rejects size 0 too.
- Android runs its size policy before the upload (medium confidence) and
  raises "size exceeded" or "Drawer needed". Both are abandoned without
  retry.

## 7. What the user sees

- **Refusal (nothing sent).** The Mac send sheet lists rejected files by
  reason, with a "file too large" section for oversized ones. A file over
  the large-media limit gets a "file too large" dialog that suggests a
  subscription for files up to 1 GB. A generic transfer-failure message
  tells the user to keep files within 300 MB and use a valid file type.
  (Paraphrased from the bundled English strings; not every key's caller was
  traced.)
- **Failure (after sending started).** The message stays in the chat marked
  failed, with a manual resend. Resend resumes as in section 4. There is no
  separate "may have been delivered" state.

## 8. Albums (MPOST)

- Each photo has its own `MPOST` on its own media connection, opened in
  turn, and uses the same agent, the same 4096-byte chunks and the same
  timeouts.
- The Mac album handler differs from the single path in two ways:
  - It never reads the `status` of `MPOST` or of the photo's `COMPLETE`.
    Any reply counts as progress.
  - A missing album item fails with client status -203.
  The offset branches match the single path. After the last photo, the album
  is posted with an ordinary `WRITE` (see
  [group outbound media](bridge/GROUP-OUTBOUND-MEDIA.md)).
- Android checks each item's upload status and fails the album on a nonzero
  status.
- mooo already fails the album on a nonzero photo `COMPLETE`
  (`ErrAlbumNotCreated`). That is a deliberate, safer deviation from the
  Mac.

## 9. Requirements for mooo

Implementation-neutral checks for the #428 fix. "Should" items are
recommendations; the rest are needed for correctness.

1. **Stream in many frames.** After a successful `POST`/`MPOST`, send the
   bytes from the server's offset to the end as consecutive secure frames,
   in order. The bridge's 64 MiB limit must not be bounded by the single-
   frame cap.
2. **Frame size.** Each frame's plaintext should be small. 4096 bytes
   matches the Mac exactly; 8192 matches Android. Frames must stay at or
   below 131,040 plaintext bytes (131,068 framed), the largest frame the
   official Android receive side accepts. The server's real bound is
   unknown, so do not depend on anything larger.
3. **Offset handling.** Offset == size sends nothing and still waits for
   `COMPLETE`. An offset outside 0..size fails before any bytes are sent;
   keep mooo's current rejection, which matches Android.
4. **Progress-based timeout.** Do not tie the transfer to one fixed overall
   deadline that a large upload on a slow link can exceed. Bound stalls
   instead: fail when a write makes no progress for a bounded interval (the
   Mac uses about 10 s). Honor the caller's context deadline and
   cancellation as today.
5. **Bounded COMPLETE wait (deliberate deviation).** The Mac waits for
   `COMPLETE` with no timer at all. A bridge should bound this wait, because
   the result is otherwise unknown forever, and report a timeout as
   ambiguous.
6. **Certainty classification.**
   - Certain "not sent": a failure before the first byte is written,
     including any local encryption error.
   - Certain refusal: a nonzero `POST` or `COMPLETE` status.
   - Ambiguous: a failure after bytes were written but before `COMPLETE`.
   Map each to the matching bridge status, never "may have been delivered"
   for the certain cases.
7. **Check limits before SHIP.** A file mooo will not send must be refused
   before `SHIP`, with the existing size notice. mooo's fixed limits (64 MiB
   files and video, 16 MiB per photo) stay a deliberate deviation from the
   server-configured 20 MiB / 300 MiB / 1 GiB until mooo reads GETCONF.
8. **No automatic retry** of an ambiguous upload, as today. Manual resend
   and ticket reuse are out of scope; mooo has no resend button.
9. **Albums.** Keep failing on a nonzero per-photo `COMPLETE`, and stream
   each photo in frames the same way.
10. **Tests.** A regression test should upload more than one frame's worth of
    bytes through the real upload path. It should check the reassembled
    bytes and that every frame respects requirement 2 (`ReadStream` returns
    each frame's size), and that `COMPLETE` is handled. The static fixture
    lists frame plans and offset cases.

## Fixture

[`fixtures/media-upload/stream-chunks.json`](fixtures/media-upload/stream-chunks.json)
(provenance `static`). It holds the expected 4096-byte frame plans for
synthetic sizes and offsets, the offset edge cases (with the deliberate
deviation for an offset past the end), and the size-limit fallbacks. mooo
has no code that consumes it yet; the #428 fix should.

## Evidence and confidence

| Finding | Source and method | Confidence |
|---|---|---|
| Mac: 4096-byte chunk, one frame per chunk, write timeout -1, one chunk in flight | Mac disassembly of the trailer agent's write step, encrypt helper and complete-write callback | high |
| Mac: secure-layer type 2 for the media connection | immediate at the trailer connect call | high |
| Mac: out-segment watchdog restarted on partial write, cleared on complete write, firing disconnects | Mac write callbacks; [write callbacks](reconnect/WRITE-CALLBACKS.md) | high |
| Mac: timeouts 15/20/10/10 from the single LocoConfig construction; GETCONF timeouts archived, no consumer found | Mac constructor caller list, setter callers, `LocoConnInfo` coder-only reads | medium-high |
| Mac: no operation-level timer; COMPLETE read unarmed | Mac operation class inventory; agent read path | medium-high |
| Mac: POST/COMPLETE branches, offset cases, ignored open result, -200012 | Mac response-callback disassembly with resolved method strings | high |
| Mac: resend reuses an unexpired stored ticket | Mac upload entry point call sequence | medium |
| Mac: album handler skips status checks; -203 for a missing item | Mac album response-callback disassembly | medium-high |
| Mac: limit defaults, selection predicate, check before SHIP | Mac Swift limit accessors, constants and send-sheet filter | high |
| GETCONF `trailer` field names | response name mapping, `LocoTrailerInfo` properties | medium |
| Android: 8192-byte frames; one frame per sink write | Android POST job, sink chain and buffered-sink loop | medium-high |
| Android: receive bound 131,068 framed bytes | Android V2SL source | high |
| Android: offset bounds and file checks; int32 offset read | Android POST job and its file-stream helper | high |
| Android: failure classifier; size/Drawer checks before upload | Android send request | medium |
| Server accepted a ~180 KB single frame from mooo | earlier owned acceptance (group photos) | medium (frame size inferred from the file) |
| mooo threshold 16,777,238 remaining bytes; failure after POST; wrong bridge status | throwaway test through the real upload function | high |

## Gaps

- **G1.** No live multi-frame upload from mooo, no resume observation, and
  no measurement of the server's frame bound or idle timeout. A prepared
  experiment on the disposable accounts was not run (it needs the
  maintainer's go-ahead in this session).
- **G2.** No consumer found for GETCONF `inSegTimeout`/`outSegTimeout`, and
  Swift code that might read them was not searched exhaustively.
- **G3.** The trigger for the upload manager's cancel-all, and whether
  CocoaAsyncSocket enables TCP keepalive, are untraced.
- **G4.** Ticket expiry (`isExpiredToken`, `tokenExpireTime`) and what the
  server returns for a `POST` on an already completed ticket are untraced
  and unobserved.
- **G5.** The large-media path (types 28/29, Drawer and the
  `availableLargeMedia` gate) was not followed past the limit choice; see
  [multi/large media](multi-large-media.md).
- **G6.** What the server does with an abandoned `POST` (accepted, then the
  connection closed with no bytes) is unobserved.
- **G7.** The Mac's receive-side frame bound, if any, is unknown.
- **A2.** Android's media POST/COMPLETE timeout value.
- **A3.** Android's resend policy for failures it keeps.
- **A4.** Android's TLS 1.3 LOCO transport and the relay (HTTP) photo path
  were not followed.

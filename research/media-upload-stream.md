# Media upload byte stream and size limits

Research date: 2026-10-19. Issue: frrad/mooo#428. Client: KakaoTalk for Mac
26.8.0 arm64, the authorized official binary, read only through static Ghidra
analysis (selector resolution, disassembly and the private parity reports).
No Android build and no public prior art were consulted, and there was no live
experiment. Addresses and pseudocode stay in the private lab notes.

This note covers what happens on the dedicated media ("trailer") connection
after `POST`/`MPOST` is answered, and how the client chooses its upload size
limits. The `SHIP`/`POST`/`COMPLETE` request and response fields are covered
in [media transfer](media-transfer.md) and
[group outbound media](bridge/GROUP-OUTBOUND-MEDIA.md).

## The mooo failure (reproduced)

On main at `2b7bd15a`, `uploadToMediaServer` encrypts everything from the
server's offset to the end of the file as one V3 secure frame. A frame's
plaintext is capped at `DefaultMaxCiphertext - 12 - 16` = 16 MiB + 22 bytes
(16,777,238). A throwaway test drove the real function against the scripted
media peer:

| File size | POST offset | Result |
|---|---|---|
| 16,777,238 | 0 | streamed in one frame; COMPLETE read |
| 16,777,239 | 0 | `client: media encrypt: loco: ciphertext too large` |
| 16,777,239 | 1 | streamed (the remaining 16,777,238 bytes fit) |
| 22,276,883 (the size in the issue) | 0 | `ciphertext too large` |

So the threshold applies to the remaining bytes, not the file size. In every
failing case the media peer had already read and answered exactly one `POST`,
and `SHIP` had already run on the main session. The bridge connector returns
the error through `outboundSendError`. It is not a `StatusError`, a
`media.ErrUploadTooLarge` or an `ErrPhotoNotCreated`, so it gets the
`uncertainFailure` notice ("may have been delivered"). That is wrong here:
nothing was streamed, so KakaoTalk certainly created no message.
`uploadRejectedStatus` and `certainRefusal` are never reached. The scripted
media peer reads one secure frame through the same per-frame cap, so before
this change it could not accept a multi-frame stream at all.

## Official client: streaming after POST

The upload operation (single media) and the album operation (`MPOST`) both
open the media connection with a `LocoTrailerAgent`, a `LocoAgent` subclass.

1. **Connection.** The agent connects to the host and port from the `SHIP`
   response with secure-layer type `2`, which is hard-coded at the trailer
   connect call. It is the same encrypted framing the main session uses: a
   4-byte length prefix followed by the AES-GCM envelope. Timeouts are the
   operation's `locoConfig` values, read in this order: connect,
   receive-header, in-segment, out-segment. A reviewed configuration
   initializer supplies 15, 20, 10 and 10 seconds (see
   [carriage timeout provenance](reconnect/CARRIAGE-TIMEOUT-PROVENANCE.md)).
   Whether every trailer operation gets that same instance is not proven.
2. **POST reply.** If no response object arrives, the operation fails with
   client status -200012. If `POST` returns a nonzero `status`, it fails with
   that status. Otherwise it reads the local file's current size from the
   file system and stores the reply's offset. Then:
   - offset < size: start streaming from that offset (below);
   - offset == size: send no bytes and go straight to reading the next packet
     (`COMPLETE`);
   - offset > size: nothing happens. No error is raised and no read is
     started, so the operation is left waiting.
   The streaming call returns a boolean for "file opened". The operation
   ignores it, so a file that cannot be opened also leaves it waiting.
3. **Chunking.** Streaming opens a file handle, seeks to the offset, and sets
   the write position to the offset and the total to the file size. Each step
   reads **4096 bytes** (or what is left), encrypts that chunk alone as **one
   secure frame**, and submits it to the socket with write timeout -1 (none).
   Chunks are counted from the offset, not aligned to 4096-byte boundaries of
   the file. A zero-length read writes nothing.
4. **One chunk in flight.** Each frame is written under its own tag, starting
   at 2^62 - 1 so that it cannot collide with packet IDs, and the agent records
   tag → length. The complete-write callback removes the tag. While the write
   position is below the file size, it reports progress and submits the next
   chunk. Otherwise it reports "data written", closes the file and starts
   reading the next packet header. The next chunk is never queued before the
   previous one has been fully written.
5. **Timeouts while streaming.** There is no deadline for the whole transfer
   and no deadline that grows with file size. The only watchdog is the
   out-segment timer (10 s with the values above). Every partial-write callback
   restarts it (disable, then enable), and every complete-write callback
   disables it. When it fires, the agent disconnects, and the disconnect fails
   the operation unless it was cancelled. Socket writes themselves have no
   timeout.
6. **COMPLETE.** After the last chunk the agent reads the next header without
   arming the receive-header timer; LocoAgent's `readHeader` has socket timeout
   -1 (none). For `COMPLETE`, a nonzero `status` fails the operation with that
   status and the server's `errMsg` when present. Status 0 replaces the
   pending message with the returned `chatLog`, persists it and finishes the
   operation. For albums, each photo's `COMPLETE` records the token and starts
   the next `MPOST`.
7. **Failure.** A disconnect (including the watchdog) completes the operation
   as cancelled or failed. Nothing is retried automatically. A manual resend
   sends a new `POST` and resumes from the offset the server returns.

**Consequence for the server.** Every upload larger than 4096 bytes from the
official Mac client reaches the media server as many consecutive secure frames,
so the server must reassemble a multi-frame stream. This is inferred from the
client code and the fact that its uploads work; mooo has not sent a
multi-frame stream to the live server yet (gap G1).

## Official client: size limits

All three limits come from the `trailer` object in the `GETCONF` response.
Only one key is renamed (`3g` → `wwan`), so `trailer` maps to
`LocoTrailerInfo` by property name. The client archives it in its preferences
and reads it back from there. Each value is used only when it is greater than
zero; otherwise a built-in default applies:

| Use | Server field | Type | Built-in default |
|---|---|---|---|
| Image sent as a photo | `trailer.upMaxSize` | int32 | 20 MiB (20,971,520) |
| File or video, and the large-media threshold | `trailer.videoUpMaxSize` | int32 | 300 MiB (314,572,800) |
| Large media | `trailer.largeUpMaxSize` | int64 | 1 GiB (1,073,741,824) |

- An image above the image limit is classified as a file, not a photo.
- The file/video limit becomes the large-media limit only when all three
  hold: the caller allows large media, the client reports large media as
  available, and the account has an activated Drawer subscription. The
  file-size check behind the send path passes "allowed" unconditionally. The
  file-sheet loader passes its own `largeMediaAllowed` flag.
- The send sheet runs these limits **before** any request. It sorts the
  chosen files into valid, oversized, zero-size, bad-extension, folder and
  missing groups, so an oversized file never reaches `SHIP`. A file of size 0
  is not uploadable.

mooo does not send `GETCONF` and keeps its own fixed limits (64 MiB for files
and video, 16 MiB per photo). See the deliberate deviations in
[group outbound media](bridge/GROUP-OUTBOUND-MEDIA.md).

## Implementation-neutral contract

- After a successful `POST`, send the bytes from the server offset to the end
  of the file as consecutive secure frames of at most 4096 plaintext bytes
  each, in order, then read packets until `COMPLETE`.
- Treat offset == size as "nothing to send" and still wait for `COMPLETE`.
  An offset beyond the size is invalid. mooo already rejects it before
  sending anything, a deliberate deviation from the official client's silent
  stall.
- Do not tie the transfer to one fixed overall deadline. The official client
  watches progress (about 10 s without write progress) instead.
- Check size limits before `SHIP` so that a refusal is certain and nothing
  is allocated on the server.

Fixture: [`fixtures/media-upload/stream-chunks.json`](fixtures/media-upload/stream-chunks.json)
(static). It lists the expected frame plan for synthetic sizes and offsets,
and the limit fallback cases. mooo has no code path for it yet: the fix for
#428 adds the chunked writer, and that PR should consume the fixture.

## Evidence and confidence

| Finding | Method | Confidence |
|---|---|---|
| 4096-byte chunk, one secure frame per chunk, write timeout -1 | disassembly of the trailer agent's write step (immediate `0x1000` passed to the file read) and of its encrypt helper | high |
| One chunk in flight; loop ends at the file size; then the next header read | disassembly of the complete-write callback | high |
| Out-segment watchdog restarted on partial write, disabled on complete write | disassembly of both write callbacks; firing → disconnect from [write callbacks](reconnect/WRITE-CALLBACKS.md) | high for the order; medium for 10 s applying to every trailer connection |
| POST/COMPLETE branches, offset ≥ size behavior, ignored open result | disassembly of the upload operation's response callback, with method strings resolved | high |
| Secure-layer type 2 on the media connection | immediate at the trailer connect call | high |
| Limit defaults 20 MiB / 300 MiB / 1 GiB and selection predicate | disassembly of the Swift limit accessors and the constants they load | high |
| GETCONF `trailer` field names | response name mapping and `LocoTrailerInfo` property list | medium (the generic model mapper itself was not traced) |
| Server reassembles multi-frame streams | inferred from client behavior | medium until observed (G1) |

## Gaps

- **G1.** No live experiment: a multi-frame upload from mooo to the live
  media server, and whether the server accepts frames larger than 4096
  bytes, are unobserved. The official client never sends a larger frame.
- **G2.** The generic `LocoModel` BSON-to-property mapper and the `GETCONF`
  request trigger were not traced. Field names come from property names.
- **G3.** Higher-level operation timers outside the LOCO agent, such as an
  upload job timeout in the Swift media layer, were not searched for. The
  agent itself puts no bound on the wait for `COMPLETE`.
- **G4.** The large-media path (types 28/29, the Drawer gate and
  `availableLargeMedia`) was not followed past the limit choice. See
  [multi/large media](multi-large-media.md).
- **G5.** Android was not consulted. Its chunk size may differ, and the
  server's behavior does not depend on the Mac's choice.
- **G6.** What the media server does with an abandoned `POST` (a connection
  closed after `POST` without bytes) is unobserved.

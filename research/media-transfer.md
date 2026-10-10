# Chat media transfer

Status: implementation specification, 2026-09-29.

This note records account-independent behavior only. Private identifiers,
upload tickets, URLs, packet captures, and message contents are excluded.

## Single-photo send

1. Validate the local JPEG or PNG, derive width and height, and compute the
   uppercase hexadecimal SHA-1 checksum.
2. On the authenticated main LOCO session, send `SHIP` once with chat ID,
   message type `2`, byte length, checksum, extension, and an empty extra object.
3. A successful response supplies an opaque upload key and a dedicated secure
   LOCO host and port. Treat all three as sensitive ephemeral state.
4. Open a new LOCO v3 encrypted connection to that endpoint and send `POST`
   once with the key, account/chat IDs, media properties, and client metadata.
5. Honor the server's optional byte offset. Encrypt and stream the remaining
   bytes as one LOCO secure envelope, then wait for the server-pushed `COMPLETE`
   packet. `COMPLETE` contains the created `chatLog`; no separate `WRITE` is
   needed for this flow.

No stage is retried automatically. A disconnect or timeout after `SHIP`, `POST`,
or byte transmission is ambiguous and must be surfaced to the caller.

## Single-photo receive

An inbound photo arrives as an ordinary `MSG` push with chat-log type `2`. Its
JSON attachment includes an opaque media key, size, checksum, MIME type,
dimensions, download/thumbnail URLs, and expiry. Live testing between two owned
disposable accounts confirmed this shape with a synthetic image. Download URLs
and keys must be treated as secrets and must never be logged.

The common `chatLog` envelope may also carry optional integer `authorId` and
`sendAt` fields. The clean-room decoder propagates them to the typed photo
event when present; absent fields remain zero, and an unknown or missing author
is left to bridge sender fallback behavior. These fields are separate from the
attachment and are not required to validate or download the photo.

The production session has one background reader that dispatches correlated
responses by packet ID and delivers unsolicited packets through a bounded push
channel even while no request is active. The photo decoder accepts type `2`, and
the downloader permits only evidenced Kakao HTTPS media hosts, bounds the body,
and verifies the advertised size and SHA-1 checksum before releasing bytes.

## Captions, expiry and failures (2026-10-10)

An optional photo caption travels as attachment `cmt` inbound and as `cmt` in
the POST `ex` JSON outbound; SHIP carries no caption. Photo `expire` is epoch
milliseconds. JPEG photos are labelled `image/jpg`. A CDN 403, 404 or 410 is a
final "unavailable" outcome; 429 and 5xx remain transient. Cancelling the
sender's context closes the dedicated media connection. Evidence, Mac trace and
gaps: [group photos](bridge/GROUP-PHOTOS.md).

## Files, video and albums (2026-10-10)

Files (type 18) and video (type 3) reuse the single-photo chain with their
own `t`, the file name in `POST f` and no dimensions. Albums use `MSHIP`, one
`MPOST` per photo, and a type-27 `WRITE`. Details, limits and acceptance:
[group outbound media](bridge/GROUP-OUTBOUND-MEDIA.md).

## Evidence and confidence

- Black-box owned-account experiment, Android 26.8.2 to clean-room Go client,
  2026-09-29: inbound type and attachment field set observed; high confidence.
- macOS 26.8.0 binary analysis, 2026-09-29: image-upload host/route and JPEG
  preparation behavior observed; medium confidence for the secondary-device
  LOCO path because the HTTP uploader is a distinct implementation.
- Public interoperable clients reviewed at pinned revisions, 2026-09-29:
  `SHIP` -> dedicated secure `POST` -> raw encrypted bytes -> `COMPLETE`; medium
  confidence before live validation.
- Black-box owned-account experiments, clean-room Go client to Android 26.8.2
  and back, 2026-09-29: production send completed and rendered without failure;
  production idle receive downloaded an exact byte-for-byte match with verified
  size/checksum; high confidence.
- Common-envelope `authorId`/`sendAt` propagation is parser-fixture evidence,
  not a claim that every server response supplies either optional field.

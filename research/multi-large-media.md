# MultiPhoto, LargeVideo and LargeFile

Research date: 2026-10-08. Authorized sources: Android KakaoTalk 26.8.2 APK and
owned A/B emulators; logged-out instrumentable Mac KakaoTalk 26.8.0 arm64.
No public Kakao protocol implementation was consulted.

## Type 27: observed album contract

Owned A selected two generated 64×64 PNG swatches, Original quality and Collage
Photos, then submitted once using the guarded shared launcher. B's raw MSG had
`type=27`. The attachment contained parallel `kl`, `wl`, `hl`, `sl`, `csl`, `mtl`,
`cmtl`, `imageUrls`, `thumbnailUrls`, `thumbnailWidths`, `thumbnailHeights` lists
and a shared `expire`. Both downloaded images matched the original generated
bytes, with sizes 139 and 141 bytes. Full images used the same allowlisted HTTPS
CDN as type-2 photos, with no credential headers needed for this observation.

Mac `LocoChatLog.multiPhoto` admits type 27 and parses `LocoChatLogMultiPhoto`.
Executed synthetic model inspection confirms its field mapping: `kl`→tokenList,
`wl`→widths, `hl`→heights, `sl`→fileSizeList, `csl`→checksumList,
`mtl`→mimeTypeList, `cmtl`→comments. URL and expiry properties retain their names.
Android `MultiPhotoAttachment` retains indexed arrays, while `MultiPhotoChatLog`
uses indexed sizes/dimensions, chooses resource keys before legacy token fallback,
and tracks per-image downloaded/expired state and cleanup. Resource-only albums
(`rscl`/`rtl`/`hfacl` without full URLs) require a separate download path.

The implementation accepts 2–30 photos, aligned required arrays, optional aligned
bounded comments, JPEG/PNG, allowlisted full/thumbnail URLs and 40-digit SHA-1
checksums. Limits are deliberate mooo policy: 128 KiB attachment JSON, 16 MiB per
image, 64 MiB total, 4096 per advertised and actual canvas dimension and 16 KiB per caption. Duplicate
JSON fields, malformed arrays and resource-only albums become explicit delivery
gaps rather than partially parsed albums. These bounds are not official parity
claims. No artwork, live URLs, media keys or identifying messages are published.
The observed-shape fixture contains generated swatches and synthetic replacements
for keys, URLs and expiry, preserving the independently observed list ordering.

## Matrix conversion and recovery

Each photo becomes an ordered `m.image` part with a stable zero-based part ID;
all parts retain the same Kakao chat/log/author identity and type 27. A nonempty
per-image comment becomes its body, with the generated filename retained.
Encrypted media and events use the existing Matrix library. Deterministic expired,
unsafe or checksum failures become per-photo notices; transient download/upload
failures leave the message cursor uncommitted.

Pinned bridgev2 normally considers any stored part a complete duplicate. Albums
use its upsert interface: completed part IDs are skipped, missing parts resume,
and the cursor commits only after successful handling. Tests exercise production
upsert/conversion and cursor paths. Files may be uploaded again after an incomplete
conversion, but already persisted Matrix message parts are not repeated. Matrix
send/database ambiguity and attachment-upload orphan cleanup remain broader
library/deployment concerns; this does not claim atomic multi-event delivery.

Owned encrypted acceptance: the initial offline album caught up as two parts with
one Kakao identity. Both wire events were `m.room.encrypted`; the retained SDK
crypto device decrypted each as `m.image` and verified exact original PNG hashes.
A second deliberate album send while the bridge was running also arrived as two
ordered encrypted parts and passed both exact-byte checks. A normal restart kept
exactly two album identities with two parts each; a later synthetic text arrived
once and decrypted. The first album remained decryptable with retained keys.
B's official Android chat displayed both albums and the follow-up. The original
album attempt was not replayed; each new experiment used a separate durable receipt.

## Types 28–29: distinct paths and live-test limits

Executed Mac synthetic inspection returned distinct `LocoChatLogLargeVideo` and
`LocoChatLogLargeFile` models for types 28 and 29. They have resource key/type/hash
properties rather than ordinary direct full-resource URL properties. Their names
are not permission to reuse the type-3/18 URL downloader.

Android's `ChatMessageType` converter promotes Video/File only when their media
upload policy selects LARGE. The current File policy returns DEFAULT or INVALID,
not LARGE. The Video policy selects LARGE above the normal configured limit only
when its room/device/account gates pass and within the configured large limit;
its account gate consults Drawer user information. The sending request separately
reports size-exceeded and needs-Drawer outcomes. This is static control-flow
research; exact account entitlement behavior is not proven by enum inspection.

A controlled emulator video-picker attempt emitted ordinary Video (3), not
LargeVideo (28). It cannot count as type-28 E2E acceptance. Producing a genuinely
large output and confirming the owned account's gate remain necessary. A
read-only service-landing check offered subscription plans; it did not establish
an existing large-upload entitlement. No purchase,
backup/restore operation, forced retagging or entitlement bypass was performed.
The maintainer permits skipping LargeFile when it cannot be live-tested. Types
28/29 remain unsupported; their resource-resolution/authentication, download,
persistent transitions, downstream playback and failure paths are untraced gaps.

## Evidence limits

Static names, mappings and policy branches are research leads. Owned type-27
shape/bytes and encrypted delivery are observations; synthetic Mac accessor/model
executions are scoped executions. Full official cache/persistence/error callback
parity, mixed-format/maximum-size albums, actual Matrix application gallery UI,
caption interoperability and native outbound album creation remain unvalidated.

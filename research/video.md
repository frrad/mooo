# Ordinary video (3)

Research date 2026-10-08. Sources: authorized Android 26.8.2 official client and
owned A/B emulators; logged-out Mac 26.8.0 arm64. No public implementation consulted.

## Independently observed contract

A selected one generated three-second 320×240 H.264 MP4 using the normal Photo
picker's video item. B received MSG type 3 with attachment fields `tk`, `url`,
`cs`, `s`, `w`, `h`, `d`, `expire`. The downloaded output was 146274 bytes,
320×240 and three seconds; its SHA-1 matched `cs`. Kakao transcoded the source,
so the delivered output differed from the input. Download used the existing
allowlisted HTTPS CDN with no credential header in this observation.

Executed synthetic Mac inspection selected type 3’s LocoChatLogVideo and
confirmed mappings tk→token, s→size, w→width, h→height, d→duration, cmt→comment,
plus separate high-quality and resource-key properties. Android VideoChatLog
reads duration from d, derives missing duration from a local media retriever
and stores seconds (milliseconds divided by 1000). It tracks separate expiry
flags, downloading relay state and local-file length, and writes updated state
through chat-log persistence and notification paths. Android ChatLog selects
urlh before url, then path, then relay fallback; its
thumbnail getter uses thumbnailUrl or appends .thumbnail for ordinary video.
mooo currently chooses the independently observed ordinary url deliberately;
high-quality selection is an explicit deviation pending a fixture.
Direct URL, relay-resource, high-quality and local-cache selection are distinct paths; full relay callbacks,
player behavior and persistence parity remain untraced unless stated below.

## Implementation decision and validation scope

Bridge the observed ordinary direct-URL MP4 as native m.video, retaining full
received bytes and checksum, dimensions and duration converted to Matrix
milliseconds. Use existing Matrix encryption. Nonempty cmt becomes the body,
with the generated video.mp4 filename retained. Resource-only attachments and
alternate high-quality selection remain gaps; types 28/29 are unchanged.

mooo policy bounds: 64 KiB attachment, 64 MiB media, 8192 per metadata dimension,
24-hour duration and 16 KiB caption. Require a bounded token, SHA-1 and allowlisted
URL; reject duplicate JSON fields. Check expiry before fetching, redirects before
following, exact bytes/checksum and bounded ISO BMFF top-level framing with ftyp,
moov and mdat. Framing validation is not codec decoding or playback parity.
Deterministic invalid/expired/checksum resources become notices; transient fetch
or Matrix upload failures preserve the uncommitted delivery position.

The observed-shape fixture preserves field names, size/duration/dimensions;
token, URL, checksum and expiry are synthetic replacements. Separate synthetic
container tests validate mooo bounds and failure handling, not official codec
parity. Owned encrypted acceptance passed: one offline/probed MSG caught up and a
second deliberate live send each became one encrypted m.video. The SDK decrypted
both attachments and verified exact independent received hashes, sizes, 320×240
and 3000 ms. Identical source sends produced 146274 and 150615 bytes respectively;
a stale first-send size expectation rejected the second. A scoped one-message
SYNCMSG read and independent download established the second output, after which
its own expectation passed without resending. This is a harness expectation
correction, not a bridge conversion bug. The shared maintenance notes now require
per-attempt output verification. A normal restart retained exactly two video
identities and one fresh synthetic
text follow-up; earlier media and the follow-up decrypted with retained keys.
B's official receiver rendered an advancing generated frame (0.733 seconds).
A keyboard-resize race in shared send-text hit Enter with stale Send bounds;
a failing-first regression now requires stable controls before the send attempt.
The old receipt was retained and its confirmed owned unsent draft cleared;
a distinct follow-up fixture succeeded using the repaired helper.
Actual Matrix application playback, high-quality/caption interoperability,
resource-only forms and official codec/cache/callback parity remain gaps.

## Full-chain evidence limits

The ordinary MSG model and direct-resource response are observed; Mac mapping
was executed with synthetic input. Android URL selection, duration mutation,
expiry setters and persistence/notification calls were read statically. The
complete uploader request/response callbacks, relay downloader callbacks, cache
cleanup transitions and all player consumers are not fully traced. Controlled
receiver playback and Matrix SDK acceptance cover the tested path only, and do
not establish full official-client parity. Outbound video from Matrix: [group outbound media](bridge/GROUP-OUTBOUND-MEDIA.md).

Final readiness exposed an unrecognized official video-player screen. A
failing-first classifier/navigation regression now recognizes the combined
player controls and exits with Back, preserving unknown-dialog stops.

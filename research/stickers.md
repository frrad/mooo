# Inbound Kakao stickers

## Evidence and first implementation contract

Research date: 2026-10-08. Authorized official references: KakaoTalk Mac 26.8.0
(arm64, logged-out instrumentable copy) and Android 26.8.2 (owned A/B emulators).
No public Kakao protocol implementation was used as a starting point.

The Mac message accessor admits types 12 and 20 into a sticker model and type 6
into a separate sound-sticker model. Executed synthetic type-12 attachments
preserve `name`, `path`, `sound`, `width`, and `height`. The owned Android free-pack
experiments emitted types 12 (static PNG) and 20 (animated WebP), with
`path`, `name`, `type`, and `emoticonItemPath`. Dimensions must come from media headers,
since the observed attachment has no width/height fields.

The official resource request builder, executed with a synthetic item path,
constructs `https://item.kakaocdn.net/dw/<path>`. Full-size requests have no query;
small/tiny variants add those bare query names. The owned asset was available
without credential headers. Do not generalize this to every pack or entitlement.

For `.gif` and `.webp`, the official download/cache consumer invokes its resource
decoder. It applies a custom stream XOR to exactly the first 128 bytes, seeded from the UTF-8 bytes of
`a271730728cbe141e47fd9d677e9006d` as its key (ASCII, not hex-decoded), retaining
all remaining bytes. The constant is part of the binary, not account material.
Executed 128/256-byte synthetic vectors are in `fixtures/stickers/codec.json`.
Executing the same official decoder on the owned download produced an animated
WebP with its tail unchanged. Files shorter than 128 bytes will be rejected by
mooo; official short-file behavior is not claimed.

### Resource stream recipe (implementation-neutral)

Initialize three unsigned 32-bit registers A/B/C from big-endian words of the
first twelve key bytes. Zero words use defaults A=`0x12000032`, B=`0x2527ac91`,
C=`0x888c1214`. Only the fixed public ASCII key is needed by this implementation.
For each of the first 128 bytes, reset retained B-bit=1 and C-bit=0. Produce eight
bits, most significant first. Each bit advances A; its old least-significant bit
chooses which other register advances:

- A bit 1: A becomes `(A >> 1) ^ 0xc0000031`; retain B's old low bit,
  then B becomes `(B >> 1) & 0x3fffffff`, XOR `0xe0000010` if that bit was 1.
- A bit 0: A becomes `A >> 1`; retain C's old low bit,
  then C becomes `(C >> 1) & 0x0fffffff`, XOR `0xf8000001` if that bit was 1.
- The output bit is retained B-bit XOR retained C-bit. XOR the resulting byte
  into the resource. Register state continues across bytes; retained bits reset
  at each byte. The tail after offset 128 is unchanged.

The initial standard-RC4 hypothesis failed the executed synthetic vectors and
was discarded. This recipe is derived from the authorized binary routine and
must match those vectors before use.

The protocol implementation accepts bounded relative asset paths only,
construct the fixed HTTPS CDN URL, forbid redirects, and never send Kakao
credentials to the CDN. Types 12/20 become Matrix `m.sticker`, with original
PNG/GIF/WebP bytes, actual dimensions and MIME type. Encrypted room/media upload
uses the existing Matrix library. No sticker artwork is bundled or committed.
Sound, composition/XCon, unsupported formats, and native outbound stickers are
outside this slice. A sound field must not silently lose audio: unsupported sound
stickers receive an explicit notice.

HTTP 404/410, invalid/oversized media, and unsupported formats produce typed
conversion-gap notices, retaining the message identity. Transient network/server
or Matrix upload failures leave the cursor uncommitted for normal catch-up.
No ambiguous outgoing message is repeated.

## Full-chain scope

Traced the Mac attachment accessor, resource URL builder, download/cache consumer,
resource transform and synthetic executions. The download method handles cache
hits and HTTP success/failure callbacks; cache eviction is unrelated to Matrix
storage. Detailed callback branches, Mac rendering-model construction and Android
persistence remain explicit parity gaps; this is an inbound implementation
contract supported by scoped executions and owned observations, not full client
parity. Header/container validation does not validate every compressed pixel bitstream.

WebP header/container validation follows Google's
[WebP container specification](https://developers.google.com/speed/webp/docs/riff_container).
The bridge preserves animation bytes; playback support depends on the Matrix
client. No sound playback is advertised.

## Owned A/B acceptance (2026-10-08)

Each prepared sticker was sent once from owned A to B using the shared launcher.
The bridge used the original B secondary profile and database. Type 20 produced
an animated 360×360 WebP (188,782 bytes); the production resource transform matched
the executed official decoder byte for byte. Type 12 produced an unchanged PNG
(35,110 bytes). Both became native `m.sticker` events inside `m.room.encrypted`.
The retained Matrix SDK device decrypted both events and verified their encrypted
attachments against the independently obtained resource hashes.

Normal bridge restarts retained exactly one database message for each sticker.
A later synthetic text arrived once and decrypted successfully; the earlier
animated sticker remained decryptable with retained keys. B's official Android
chat displayed both received stickers and the follow-up. Both owned primary
phones remained authenticated; no secondary enrollment was retried.

Synthetic production-path tests cover the executed codec vectors, animated GIF
and WebP byte preservation, bounded paths/resources, malformed attachments,
missing-resource notices, and transient transfer failures that do not commit the
cursor. This does not establish Element/Beeper animation playback, sound stickers,
paid-pack entitlement behavior, outbound stickers, or full official-client parity.


## Additional free-pack availability audit (2026-10-09)

A controlled owned-account Android 26.8.2 experiment selected the first item in
Kakao Friends Classic. One tap opened a preview; a single explicit Send produced
`Sticker=12`, with `path`, `emoticonItemPath`, `name`, and `type` attachment keys.
The receiving official client displayed the ordinary sticker. This adds emitter
coverage but is not a fixture for AnimatedEmoticon, Spritecon, or AnimatedStickerEx.

The first non-animated item in the free Kakao Friends Basic Mini Face 1 pack was
selected in the Mini tab. Its editor's Enter inserted content into the chat
composer; one explicit Send produced `Text=1`, with an `emojis` attachment.
The receiving official client displayed a small graphic inside a text bubble.
The observed structure contains `total_len`, `total_item`, and `items`; each
observed item has an `id`, `len`, and an `at` array. Item IDs, resource resolution,
position units, bounds, repeated placements, rendering, and failure behavior
remain untraced. This is a rich-text gap, not a Spritecon fixture or evidence that
all Mini items use this format. No Matrix graphic-parity claim is made.

Scoped static inspection of `EmoticonChatLog.kt` (`chatlog.h`, classes10.dex)
shows shared attachment getters for `name`, `alt`, `sound`, `width`, `height`,
`xconVersion`, `emoticonDemoChatLog`, and `welcome`. Its resource helper treats
Spritecon differently when deriving a thumbnail name. `ChatSender.kt`
(`xua.o`, classes8.dex) passes the selected type and attachment through the send
request and invokes an additional input cleanup for Spritecon. Neither trace
establishes complete subtype contracts or an available owned pack for 6/22/25.
The DEX fingerprints are recorded in [the Nudge audit](nudge.md).

Private captures and UI observations remain outside the repository. No packs
were purchased and no resource entitlement was bypassed. Types 6/22/25 remain
unsupported pending controlled fixtures and a complete resource/rendering trace.

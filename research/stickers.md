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


## Sticker sent with text (2026-10-16)

Controlled owned-account experiment, Android 26.8.2 on owned A and B. A
selected the first Kakao Friends Classic item in the A/B direct chat. With
the sticker preview open, A typed a synthetic caption into the composer and
tapped Send once. B's scoped read-only SYNCMSG probe saw exactly one log:
type 12, `message` set to the caption, and the same attachment keys as a
sticker sent alone (`path`, `emoticonItemPath`, `name` "(Emoticons)", `type`).
A sticker sent alone has an empty `message`, as in the observed group fixture.
Both official clients render that one log as the sticker with the text in a
bubble below it.

When B replied to that log from Android, the reply carried `src_type` 12 and
the caption as `src_message`. B's reply contained a second sticker and its own
text; that is recorded in [image and sticker replies](bridge/GROUP-IMAGE-REPLIES.md).

Fixture: [`fixtures/stickers/observed-direct-sticker-with-text.json`](fixtures/stickers/observed-direct-sticker-with-text.json)
(provenance `observed`; paths are synthetic).

The decoder had ignored `message` on types 12 and 20, so the bridge sent only
the sticker (regression). It now reads `message` as an optional string. A
present non-string value, invalid UTF-8 or a NUL turns the log into a
malformed-payload gap. A sticker with text becomes two Matrix events: the
`m.sticker` (empty part ID, as for a sticker alone) and then an `m.text` (part
`text`). The event is an upsert. A redelivered message that has only its
sticker row, such as one bridged before this fix or a send that stopped after
the sticker, gets the missing text without repeating the sticker. A sticker
resource failure still produces its notice, and the text is still sent. Both
parts store the caption as their reply preview, so a Matrix reply to either
part sends `src_type` 12 and the caption as `src_message`, matching Android.

Live acceptance used B's bridge profile, catching up the two logs above while
offline. The tester's encrypted Matrix transcript showed A's ghost sending a
360×360 PNG `m.sticker` and then the caption, followed by B's text and its
sticker, both replying to the first sticker. A Matrix reply to the bridged
caption reached Kakao as type 26 with `src_type` 12 and the caption as
`src_message`, with the correct source log. A's official client showed the
quote correctly.

### Mac client (26.8.0) parity, 2026-10-16

Method: static reading of the authorized macOS 26.8.0 arm64 binary (private
Ghidra project and annotated disassembly), plus an executed harness: the
isolated, logged-out probe copy of the official binary ran synthetic inputs
through the client's own sticker model, reply presenter and edit predicate
under Frida. Fixture:
[`fixtures/stickers/executed-mac-sticker-text.json`](fixtures/stickers/executed-mac-sticker-text.json)
(provenance `executed`). The client maps types 6, 12, 20, 22 and 25 to one
emoticon model family; 12 is the static sticker and 20 the animated sticker.

1. **Model and rendering.** The emoticon model's text is the chat log's
   `message` (executed: empty for a sticker alone, the text otherwise, for
   types 12 and 20). The chat-log view (static) adds a text bubble and the
   sticker view when that text is non-empty and the sticker view alone when
   it is empty, so the Mac shows both, as Android does. The vertical order
   comes from layout code that was not traced; the bridge keeps the observed
   order (sticker, then text).
2. **Reply summary.** The reply composer's `src_message` is the presenter's
   summary of the source, cut to 100 UTF-16 units (static). Executed: a type-12
   or type-20 source with text is summarized as its text, the same as Android
   sends. A sticker without text is summarized with the localized emoticon
   label ("Emoticon" in English), and a reply carrying a sticker and text by
   its reply text. The executed fixture is read by a connector test that
   checks the bridge stores these summaries as reply previews. For a sticker
   without text mooo keeps its own `[sticker]` preview (recorded deviation).
3. **Composer.** The Mac can send a sticker with text (static). The emoticon
   send puts the typed text in `message` and the emoticon fields (`name`,
   `path`, and for some kinds `path2`, `scon`, `width`, `height`,
   `xconVersion`, `sound`, `kid`) in the attachment. This is the shape Android
   sends. A reply carrying an emoticon sets `attach_type`, `attach_content`
   and `attach_only` to whether the text is empty. With text, the Mac
   therefore sends `attach_only: false`, where Android leaves the key out; the
   decoder treats both the same.
4. **Editing.** Executed: the Mac allows editing a sticker of type 12 or 20
   when it has text, is the user's own and is less than 24 hours old. It also
   allows editing a reply carrying a sticker and text. It refuses a sticker
   without text and an attachment-only sticker reply. Static: an emoticon
   edit sends `MODIFYMSG` with the new text. Its `extra` is the original
   attachment's sticker keys (`url`, `name`, `sound`, `path`, `path2`, `scon`,
   `alt`, `xconVersion`, `width`, `height`), so the sticker is kept.

Observed on Android 26.8.2 the same day: A long-pressed its own sticker with
text and the menu offered Edit. The edit composer holds only the text and
shows no send control while it is empty. After one edit, B read a type-0
feed log with `feedType` 25, the sticker's `logId`, `targetRevision` 1 and
`hidden` true. `GETMSGS` returned the sticker as type 12 with the new text
and the same sticker path. A `SYNCMSG` read seconds after the edit still
returned the old text; a read a few minutes later returned the new text.
Fixture: [`fixtures/stickers/observed-direct-sticker-text-edit.json`](fixtures/stickers/observed-direct-sticker-text-edit.json)
(provenance `observed`).

The bridge ignored edits whose message was not a text or reply, so Matrix
kept a sticker's old text (regression). An edit of a sticker now edits only
the text part (`text`); the sticker event is not edited. The sticker part
gets the new revision and reply preview without a Matrix event, so a later
reply quotes the new text, as the Mac summary does. For a reply carrying a
sticker, the edit targets the text part (empty part ID) by ID, and the
sticker part is updated the same way. A sticker edit without text is ignored.

Live acceptance used B's bridge profile, catching up A's edit while offline.
The tester's encrypted transcript showed one Matrix edit (`m.replace`, new
text) of the sticker's text event; the sticker event was not edited. A tester
Matrix reply to the sticker part then reached Kakao as type 26 with
`src_type` 12, the edited text as `src_message` and the correct source log.

Remaining gaps:

- Matrix edits of a sticker's text, and of replies, are still refused by the
  bridge. Supporting them needs the original attachment for `extra`.
- The Mac's on-screen position of the text bubble relative to the sticker
  (layout code untraced; rendering the view off-screen in the harness
  crashed).
- No live Mac send or edit of a sticker with text was observed. The Mac
  findings are static and executed only.
- Type 20 with text was executed on the Mac model but not sent live.

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

Nonanimated Mini rendering is implemented through the type-1 text path; see
[Mini emoticons](mini-emoticons.md) for source placement, encryption acceptance
and the explicit layout and animated-resource gaps.

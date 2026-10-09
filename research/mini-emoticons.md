# Mini emoticons inside text messages

Research date: 2026-10-09. Authorized KakaoTalk Android 26.8.2, controlled
owned-account sends and scoped receiver MSG captures; scoped JADX 1.5.6 inspection
of the same APK. No public protocol implementation consulted. Proprietary assets,
captures and decompiled source remain outside Git.

## Observed contract

Four controlled sends used the first non-animated item of the free Basic Mini
Face 1 pack. All produced type 1 and one `emojis` item, `total_item: 1`,
`total_len: 6`, `len: 6`. Pure messages use anchor `[1]`; a literal opening
parenthesis before the Mini makes its anchor `[2]`. The receiver displayed the
same graphic inline in pure and mixed text.

The initial manual sends selected the item and additionally pressed Enter.
Their wire sources were `(item)\n` and
`Mooo-Synthetic (literal) before (item)\n after`. The UI dump omitted that
trailing newline; initial fixture transcription was corrected against the wire.
The shared helper's two fresh sends waited for direct insertion and did not
press Enter. Their sources were `(item)` and
`Mooo-Synthetic-Mini-Verified (literal) before (item) after`, without that newline.
Both pure and nonempty-composer traces confirmed direct insertion after selection.

[The observed fixture](fixtures/mini-emoticons/observed-text.json) replaces each
resource ID with a synthetic category-12 ID, preserving source, counts, lengths
and anchor ordinals. Its `mooo_deviation` explains the different Matrix layout;
`mooo_expected` specifies ordered parts. It does not establish availability of
the synthetic resource or authorize requesting it.

## Static chain

- `ChatLog.kt` (`chatlog.d.W1`, classes9.dex) stores the attachment string,
  treats blank/`null` as absent, parses `emojis` through `eh9.s`, and stores an
  empty emoji model when parsing fails. `eh9.s` deserializes and applies `eh9.j`.
- `EmojiAttachment.kt` (`com.kakao.talk.chat.emoji.EmojiAttachment`, classes9.dex)
  serializes occurrence count as `total_item`, combined fallback length as
  `total_len`, and item records as `items`. `EmojiInfo.kt` has required `id`,
  `len`, and `at` fields.
- `EmojiUtils.kt` (`v1o.a/c`, classes9.dex) computes count from the sizes of the
  anchor lists and length from each item's length times its occurrence count.
- `ChatMessages.kt` (`eh9.i`, classes9.dex) flattens and sorts anchor/length
  pairs, scans the source for opening parentheses with a one-based counter,
  and installs graphic spans when the matching fallback fits inside the source.
  Java string indices and span lengths use UTF-16 code units. Anchors are not
  byte offsets or character offsets.
- `eh9.j` checks aggregate consistency, a maximum count of 150, item lengths
  from 1 through 12, unique anchors and numeric resource-ID syntax. JADX's output
  for the nonpositive-anchor branch is incomplete; its exact failure behavior
  has not been established by executing the official method.
- `EmojiConst.kt` (`tzn`, classes9.dex) uses whole-string numeric underscore
  syntax. `ItemCategory.kt` (`zc00`, classes11.dex; anonymous-class inlining
  disabled for constructor inspection) maps category 12 to non-animated Mini
  text and category 14 to animated Mini text. Categories 11/66/90 map to types
  6/22/25 and are separate formats.
- `EmojiHelperImpl.kt` (`o0o.O/u/w`, classes21.dex) derives
  `<pack>.emoji_<index>.png` for non-animated Mini resources and `.webp` for
  animated resources. Thumbnail names use `thum` and PNG. `HostConfig.kt`
  (`m9y.c`, classes11.dex) selects `item.kakaocdn.net` in the production branch;
  resource URLs use HTTPS and `/dw/`. Resource-size suffixes are separate inputs.
- An owned non-animated resource resolved at that full-size PNG path without
  credential headers; the PNG graphic visually matched the native receiver.
  This does not establish availability or entitlement behavior of other packs.
- `ChatOnlyFewEmojiViewHolder.kt` (`c0`, classes8.dex) uses the shared text
  renderer and a separate Mini size and information button. Keyboard ownership
  checks exist in `o0o.g/K`; they are not a license to bypass sender restrictions.

The shared text renderer, loader coroutine, request callbacks, cache expiration,
database writes after chat-model updates, all malformed-span cases, and animated resources
remain incompletely traced. JADX could not fully decompile several loader and
chat-model methods. Static leads are not executed parity fixtures.

## Implementation and limits

Preserve the original source and turn validated non-animated spans into ordered
native Matrix text/image parts. Each image must use the normal encrypted-media
upload path in encrypted rooms. This deliberately changes Kakao's single-bubble
inline layout, while retaining surrounding text, fallback text, graphic bytes
and source order. Do not publish an unencrypted inline-image URL to approximate
that layout. Use deterministic part IDs and resume missing parts after partial
delivery, as for albums.

Reject ambiguous, overlapping, out-of-range or unsupported attachments with a
retained message identity. Do not silently discard an `emojis` attachment and
claim successful text conversion. Animated Mini resources, outbound keyboard
behavior and pixel-identical text layout remain separate gaps.

Local limits are a 64 KiB attachment/source, 32 item records, 128 occurrences
(the static official limit is 150), 12 UTF-16 units per fallback, 64 ASCII bytes
per resource ID, 4 MiB per image and an 8 MiB aggregate resource budget per
conversion. Unique fields and anchors, aggregate consistency, UTF-16 boundaries
and non-overlap are required. Unknown inner attributes fail closed. Invalid
attachments retain identity through the normal gap path. Deterministic resource
failures produce notices retaining the fallback and surrounding text; transient
failures leave the cursor uncommitted. Resources resolve before upload, so an
aggregate-budget failure becomes a notice rather than endless orphan uploads.
Resources/uploads are reused only within the conversion; no persistent CDN cache
or sender entitlement bypass is implemented. Other type-1 rich metadata,
including combined mention formatting, remains untraced.

The original graphic-loss regression failed before implementation. Separate
regressions exposed and protected the aggregate-budget retry problem, ordinary
`null` attachment compatibility, unrelated metadata array limits, and guarded
sender behavior.


## Repeatable owned sender

`research/emu.sh send-mini a|b` accepts private stdin JSON with `peer`, `receipt`,
`fallback: "(item)"` and optional synthetic ASCII `prefix`/`suffix`. It requires
an owned open chat, empty composer, closed keyboard, the free Basic Mini Face 1
pack and an unambiguous first non-animated item. It reserves the receipt before
selection, waits for exact direct insertion, verifies the whole source and stable
Send control, submits once and checks composer clearing. It does not press Enter.

Stopped attempts revealed that selection already inserts the Mini into the
composer. The initial guard incorrectly expected an unchanged composer and
stopped before Send. Receipts were retained, absence of delivery was checked and
only verified unsent synthetic drafts were discarded before distinct attempts.
Regression tests cover direct insertion, delayed insertion, wrong fallback,
changed source and an existing receipt. Fresh pure/mixed helper runs completed.

An exact-log history read returned two logs for the older live target despite
the single-target request. The private capture harness stopped, then used an
explicit unique requested-log match within the owned chat response. It never
selected a different log as a substitute. Both fresh attachments and source
strings were checked against those scoped responses.

## Owned acceptance

Two initial messages arrived through encrypted catch-up; two fresh helper sends
arrived live. Together they produced nine Matrix parts from four source IDs,
with no conversion gaps. The existing SDK credentials and crypto database
successfully decrypted all parts, including exact PNG bytes and dimensions,
original fallback strings, surrounding text and newlines. The native receiver
showed the same graphic and intended placement.

A normal restart retained exactly those nine parts and event IDs. A later
ordinary text message was delivered once; ten encrypted events remained
decryptable with retained keys. The committed-build restart is a required final
acceptance gate before merging. Animated Mini resources, inline pixel layout,
combined mentions, malformed official fallback parity and loader/cache lifecycle
remain explicit gaps.

Additional source fingerprints (SHA-256):

- classes9.dex: `4438f27283cd26c1b19578f46a04d91a17695fa4085731d1b4b0920ffb616402`.
- classes11.dex: `374eadd2787bcbfcbaa04468cf9c750bc4b872764a57e8edb58af24cb1a06aa2`.
- classes21.dex: `6b79ef4ff81f75dc71958cb6dcf569c0575e5e0ac01025c7e4789e992b66abba`.

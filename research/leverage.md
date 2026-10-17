# Shared cards (type 71)

Research date: 2026-10-16. Message type 71 is named `Leverage` by the official
message-type enum ([message-types.md](message-types.md)). It is the chat log
for a shared card: a KakaoTalk channel profile or post shared into a chat, and
more generally a share template rendered as a rich card (feed, list,
commerce, music and other layouts). No public prior art was consulted. An
outside pull request (frrad/mooo#412) guessed that type 71 is a feed or link
card and read `P.ME` and `P.SNM`. It was not used as a starting point; the
comparison is at the end.

Fixtures:

- [`fixtures/leverage/observed-cards.json`](fixtures/leverage/observed-cards.json):
  `observed`, three cards and one official reply produced between owned
  accounts.
- [`fixtures/leverage/executed-mac-model.json`](fixtures/leverage/executed-mac-model.json):
  `executed`, what the official Mac client's own model returned for 90
  synthetic cards.

## Owned-account observation

Method: owned Android lab emulators A and B, KakaoTalk 26.8.2, in their direct
chat. A had followed a verified business channel earlier. A opened the
channel's profile and chose "Send to KakaoTalk Friends" in its menu, then
pressed the Share button under one of the channel's posts, sending each to B.
On B, the profile card was long-pressed and replied to with a synthetic text,
and the post card was forwarded back with the Share icon beside the bubble. A
private probe on B's mooo secondary profile read each new chat log with
SYNCMSG. Raw records were reduced to their key structure and then deleted. B's
chat list and chat screen were read for what Android displays.

Observations (confidence high for these two card kinds):

- Both shares wrote one type-71 chat log authored by the sender. The forward
  wrote a new type-71 log authored by the forwarder, with the same `message`
  and `C` object; only `P.RF` changed (`pf` or `pv` to `chat_ln`).
- The attachment is a JSON object with two parts. `P` holds the card's
  envelope: `TP` (layout: `Profile`, `Feed`), `ME`, `SID` (`plusfriend`),
  `DID`, `L` (link object), the minimum versions `VA`, `VI`, `VW`, `VM`,
  booleans `FW`, `KV`, `BC`, `AD`, `ADULT`, `PC`, a signed block `A`
  (`version`, `code`, `createdAt`) and `RF`. `C` holds the layout's content:
  `PR` (profile header), `TI` (title/description), `THL`/`THC` (thumbnails),
  `SO` (social counts), `BUL` (buttons with their own link objects).
- The chat log's `message` equals `P.ME`. For these cards it is a Korean
  sentence naming the channel ("shared the profile/post of channel X"), even
  though both phones use English. Link objects use `LPC` and `LMO` (PC and
  mobile web URLs) and, for posts, `LCA`/`LCI` (custom-scheme app links).
- B's chat list previewed the card as exactly the `message` text. The bubble
  was a rich card: channel image and name, description, a subscriber count and
  a "채널 홈" (channel home) button for the profile; title, description, two
  thumbnails and two buttons for the post. The `message` text does not appear
  in the bubble. Long-press offered Reply, Share and Delete.
- B's reply preview showed the `message` text. The reply was a type-26 log
  whose extra had exactly `src_logId`, `src_userId`, `src_type` (71),
  `src_message` and `src_spoilers` (`[]`). `src_message` was the card's
  `message`, unchanged.

Not observed: cards from other apps' share templates (list, commerce, music,
carousel), calendar cards, locked or adult cards, open chats, type 73, and
anything the Mac client displays. The Android emulators could not produce a
logged-in Mac client.

## Official Mac client (26.8.0)

Static method: read-only disassembly and decompilation of the authorized arm64
binary in the private Ghidra project, with Objective-C selectors and constant
strings resolved. Executed method: the isolated, logged-out probe copy of that
binary was spawned under Frida. For each synthetic case the harness created the
type-71 model, set its author, message, attachment and a sent status, read its
texts and predicates, built the card data object from it, and asked the cell
class which bubble view it would use. The base-class reply, reaction and thread
predicates need a chat room, so the harness replaced them with YES; only the
type-71 overrides decided those answers. A case that killed the process was
recorded and the run restarted after it. No account, network, or preference
state was used. The original preferences were hash-checked unchanged.

### Model

`+[NTChatMessage classWithType:message:chatId:]` returns `NTChatLeverage` for
type 71 and also for 73. `NTChatLeverage` reads the attachment as a dictionary
and hands its `P`, `C` and `CAL` parts to `KAOLeverageMessageData`, which the
bubble, link and predicate code use. `TP` is lower-cased and mapped to a
layout: `feed`, `profile`, `music`, `talkmusic`, `melonmusic`, `melonlist`,
`list`, `noti`, `commerce`, `carousel`, `_cover`, `_brand`; anything else is
layout 0. Version gating uses `VM` (the Mac minimum version).

### Text outside the bubble

Outside the calendar case below, the chat-list text (`-lastMessage:` with NO)
and the export text (YES) are the same: the chat log's `message`, exactly as stored (no trimming, newlines kept),
whatever the attachment holds. A missing, empty, non-JSON or non-object
attachment does not change it. `P.ME` is not used for it; a card whose `ME`
differs from `message` shows `message`.

The one exception is a `CAL` key: when present and not null, both texts come
from the calendar formatter instead (`CAL` `{}` gave an empty string). That
formatter is localized and depends on the event type, alarm and date; it was
not traced further.

The reply construction uses the export text as `src_message` (static; see
[group replies](bridge/GROUP-REPLIES.md)), which matches the observed Android
reply.

### Bubble

The bubble view comes from the card data: a mobile-only card (`PC` false,
unless it is an alimtalk card) and an alimtalk card the user has not enabled
get a "check on mobile" view; a locked card (`LOCK`) a lock view; an
adult-only card (`ADULT`, account not verified) an adult view; otherwise the
layout's view (feed, profile, list, commerce, music variants, notification,
cover, brand), and a generic view for an unknown layout, carousel without a
type, or unreadable attachment. The bubble's contents were not traced.

### Tap link

Tapping the card opens the card's public link (`P.L`), unless the card is
mobile-only, an alimtalk card, or locked, in which case there is none. From a link object the
Mac takes `LCM` (Mac URL) if non-empty, else `LPC` (PC URL), provided `LAT`
(action type) is absent, empty, or contains "web" in any case; `LAT` "app"
gives no link, and `LMO`, `LCA` and `LCI` are never used on the Mac. The tap
handler then opens it only if it parses as an http or https URL (it
percent-encodes an unparseable string first). A soft-warning card (`SW`)
shows a warning dialog first; an authorized link (`LAA`) is opened with
account authorization.

### Predicates

- Reply and thread: allowed unless `LOCK` is true, or `ADULT` is true and the
  account is not adult-verified.
- Reaction: allowed unless the card is a carousel (`TP` "carousel" in any
  case, executed) or "growable". Growable (static) means `BC` is true and the
  card is not verified (`KV`), locked, adult-gated or mobile-only, in a Plus
  (channel) chat; in any other chat a card is never growable.
- Share (forward): requires a sent status, a forwardable card (`FW`), not
  growable, and not an alimtalk card.
- `LOCK`, `ADULT`, `PC`, `SW`, `LAA` and `ALM` are read with `boolValue`:
  JSON `true` or a non-zero number, or a string whose first character after
  whitespace, a sign and leading zeros is Y, y, T, t or 1-9 (`"yes"`, `" t"`,
  `"-1"`, `"007"` are true; `"no"`, `"false"`, `"0"` are false). A missing
  `PC` counts as true.

### Failure behaviour

These killed the official process: a non-string `TP`, a non-object `P`, a
non-object `CAL` string, and a non-string `LCM`. A nil message gives a nil
summary; an empty one gives an empty summary.

## Implementation

mooo decodes type 71 into `events.LeverageMessage`: the chat log's `message`
as the summary, the Mac tap link when it is a plain http(s) URL, and whether
replies are unavailable. It never keeps the attachment, the signed `A` block or
any other field. `String()`/`GoString()` are fully redacted.

The bridge posts an `m.text` from the card's author at its send time. Its body
is the summary, then the link on its own line when there is one. This is the
text both official clients show for the card everywhere outside the bubble,
plus what tapping the card opens; the rich bubble is not reproduced. The
summary is stored as the reply preview.

A Matrix reply to a card sends `src_type` 71 and the summary as `src_message`,
with the same extra keys as the observed official reply. Replies to locked or
adult-only cards are refused before anything is sent, with a message saying
KakaoTalk does not allow them. Matrix reactions to a carousel card are refused
before anything is sent, as the official client offers none. A Matrix redaction
of the bridged account's own card is not sent as a delete for everyone
(untraced); it removes the event only from Matrix. The observed fixture's
`mooo_link` values are mooo's own application of the Mac link rule to the
observed attachments (Android cannot show the Mac link); link parity rests on
the executed fixture. An inbound KakaoTalk reply to a card that was never
bridged quotes its `src_message`, as the official client does.

Deliberate deviations (each executed case that differs carries a
`mooo_deviation` in the fixture):

- A card with a `CAL` key keeps the generic unsupported notice; the calendar
  summary is untraced.
- A missing, empty or whitespace-only `message`, or one that is not valid
  UTF-8, contains NUL or exceeds 64 KiB, keeps the generic unsupported notice.
- An attachment over 64 KiB, not valid UTF-8, or with data after its JSON
  object, is treated as unreadable: the summary is kept and there is no link.
- Inputs that killed the official process are handled: `TP` is not read; a
  non-object `P` is ignored; a non-string `LCM` falls back to `LPC`; a
  non-object `CAL` keeps the notice.
- No link is bridged for adult-only cards (opening needs adult verification),
  soft-warning cards (the Mac warns first), authorized `LAA` links (the Mac
  adds authorization), non-http(s) links, links over 2048 bytes, and links with
  spaces, control characters or non-ASCII bytes (the Mac would percent-encode
  them).
- Replies to adult-only cards are refused even for an adult-verified account;
  the bridge cannot check verification.
- A null `PC` counts as desktop-visible, like a missing one; the official
  handling of null was not executed. Flag strings are read with ASCII
  whitespace skipping; Foundation's exact whitespace set (Unicode spaces,
  newlines) and fractional numbers were not executed.
- As for every reply, `src_message` is cut at 100 UTF-16 units
  ([group replies](bridge/GROUP-REPLIES.md)). An oversized attachment that
  holds `CAL` is unreadable to mooo and shows the plain summary.

With `network.debug_dump_dir` set, every bridged card's full raw chat log is
also written there (`gap-partly_displayed-*.json`, owner-only, private
content), so real samples of other layouts can be collected before their
content is implemented.

## Gaps

- Calendar cards (`CAL`) and their summary text.
- Type 73 (`PlusLeverage`): the Mac uses the same model, but no record was
  produced or observed, so it stays an unsupported notice.
- The bubble's contents (titles, descriptions, thumbnails, buttons and their
  own links) are not bridged.
- Cards from third-party share templates, locked, adult and carousel cards
  are covered by the executed fixture only.
- Matrix reactions to a growable card in a Plus chat are not refused; the
  bridge does not track whether a chat is a Plus chat.
- Whether the official client allows deleting a card for everyone is
  untraced.
- How the Mac shows a card in a notification was not traced.

## Comparison with frrad/mooo#412

That pull request's guess that type 71 is a feed or link card is broadly
right. Its renderer read `P.ME`, which equalled `message` in every observed
card, but the official clients use `message`: a card where the two differ
shows `message`. `P.SNM` did not appear in the observed channel cards and is
not read by the Mac's text or link paths that were traced.

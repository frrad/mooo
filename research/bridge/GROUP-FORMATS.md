# Regular-group inbound formats

Evidence date: 2026-10-10. Scope: inbound albums, video, audio, files,
contacts, profiles, locations, stickers, polls and Boards posts in an encrypted
three-member regular group, plus the unsupported-format policy. Photos,
captions and replies are covered by their own slices
([photos](GROUP-PHOTOS.md), [replies](GROUP-REPLIES.md),
[image replies](GROUP-IMAGE-REPLIES.md)).

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project; no public prior art. Static observations,
not executed fixtures. Per-format traces are in the format documents listed in
[message types](../message-types.md).

- `LocoChatLog` carries no room field; each type accessor checks only the
  message type and parses the attachment string. Message-class selection is a
  fixed type-to-class map whose only room lookup concerns one feed subtype.
  Polls and posts (13, 14, 24 and the open-chat 96, 97, 98) map to one Boards
  class with no receive-side room gate. A profile card's embedded user is
  always resolved as a regular user. Bubble selection checks the room type only
  for loss-mark cells.
- Unknown or unmapped types render a generic "Please check on your mobile."
  bubble ("Unable to Display Message." in the chat list); polls/posts with an
  unknown form or a newer `version.mac` render an update prompt. None of this
  depends on the room type.
- Room-specific branches found: an unjoined regular group under the
  join-confirm setting summarises every message as an invitation (no parsing
  difference); open community chats use a different default expiry when an
  attachment has none; open groups show a review state on files and videos.

Recorded gaps: Swift bubble bodies (call-pattern scans only), the complete set
of accepted post/poll object forms, the fallback bubble's exact text, and the
inbound object-creation block.

## Observed fixtures

Each claimed format has an `observed` fixture under `research/fixtures/`:
audio, file, contact, location, video, vote, post, multiphoto, profile, photo
and replies from earlier owned observations, and a new
`stickers/observed-group-shape.json` (owned C's static sticker in the group:
type 12, empty message, attachment `name`, `path`, `type`,
`emoticonItemPath`), consumed by a production decode-and-convert test.

## Owned encrypted acceptance

Method: a fresh build of the current main branch with the original B secondary
profile in the existing owned encrypted A/B/C regular group. Every native send,
fault and bridge start/stop wrote a private EXCL 0600 receipt first; no
mutation was repeated. Matrix results were read with the tester device
(decryption, sender, type, MIME, decrypted media hash).

| Format | Sender, delivery | Matrix result |
|---|---|---|
| Location | A, bridge offline → catch-up | `m.location` with the synthetic address |
| Poll | A, bridge offline → catch-up | text summary with title and three options |
| Contact | A, online | `m.file` vCard, hash equal to the earlier observed fixture |
| Profile card | A (C's profile), online | text summary with the shared profile |
| Voice memo | A, online | `m.audio` (`audio/mp4`), decryptable media |
| File | A, online | `m.file`, bytes equal to the source |
| Sticker | C, online; C again while the bridge was offline | animated `image/webp` and static `image/png` `m.sticker` from C's ghost |
| Boards post | A, online | text summary of the post |
| Video | A, online | `m.video` (`video/mp4`); Android re-encodes before upload, so bytes differ from the local source and the bridge verifies Kakao's own checksum |
| Album (collage) | A, online | two ordered `m.image` parts, bytes equal to the sources, JPEG part labelled `image/jpeg` |
| Restarts | — | no new Matrix events; durable cursor equal to the latest mapping |

A leftover pass-through lab proxy from the previous run carried this run's
Matrix traffic; both its fault modes were set to pass throughout.

The lab tool's post and poll senders completed their sends but could not
return to the chat from the group's side drawer; each outcome was confirmed
read-only before continuing and nothing was repeated.

## Unsupported formats

Every `MSG` either decodes to a supported format, becomes an
`UnsupportedMessage` notice naming its type, or becomes a committed
malformed-payload notice; an unidentifiable message stops admission rather
than being dropped. No message kind maps to a silent drop. A live unsupported
type could not be produced in this group: creating a calendar event (Schedule,
type 13) was refused by KakaoTalk's feature restriction, and Nudge (21) and
SharpSearch (23) have no observed Android entry point. The policy remains
covered by production-path tests.

### Acceptance gaps

- No live unsupported-type message.
- Mini emoticon text, LargeVideo/LargeFile and resource-only albums were not
  exercised in the group.

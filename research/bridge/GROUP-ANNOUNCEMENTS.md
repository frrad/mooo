# Regular-group rich announcements

Evidence date: 2026-10-10. Scope: Boards announcements (Moim meta type 1) of
rich post types projected to the Matrix room topic, their replacement, clearing
and reconnect behavior, and the type-24 chat messages that announcement posts
create. Builds on the TEXT announcement work in
[chat metadata](../chat-metadata.md) and [Boards posts](../post.md).

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project; no public prior art. Static observations,
not executed fixtures.

- **Content model.** The announcement `ct` post JSON is parsed for
  `owner_id`/`owner.id`, `notice_setter_id`, `type`, `json_content` (kept only
  if longer than two characters), timestamps, `thumbnail`, `thumbnail_large`,
  `id`, `subject`, `content`, `gif`, `image_count`, `notice`, `poll_count` and
  `revision`. `type` maps TEXT, IMAGE, VIDEO, FILE, POLL, QUIZ and SCHEDULE;
  anything else is unknown. An empty, unparsable or non-object value, including
  `{}`, produces no post and hides the banner.
- **Banner text, in order:** `json_content` rendered as text (mentions as
  `@name`/`@All`), else `content`, else `subject` (a POLL post with several
  polls adds " and N more"), else for IMAGE/VIDEO/FILE "The photo/video/file
  has been posted as an announcement.", else "Please update KakaoTalk to the
  latest version." SCHEDULE adds a D-day prefix. `notice=false` is parsed but
  not checked on the banner path.
- **Replacement and clearing.** A type-1 entry whose revision differs from the
  stored one replaces it; a revision of zero or less deletes it; a response
  without type 1 never clears; `{}` with a newer revision is removal.
- **Reconnect.** CHGMOMETAS pushes are merged directly. GETMOMETA runs when a
  room is opened (driven by the CHATONROOM revision map) or a link is joined, so
  an offline removal is recovered when the room is next opened.

Recorded gaps: whether a meta change creates or removes the banner in the
window controller, D-day rules, the CHATONROOM `mr` location and encoding,
Swift-side GETMOMETA triggers and the failure UI.

## Observed fixtures

- `research/fixtures/chatmeta/observed-moim-rich-announcements.json`: owned A
  posted an IMAGE announcement (text plus one photo: `type` "IMAGE", `content`,
  `json_content` text elements, `thumbnail`, `thumbnail_large`) and a POLL
  announcement (`type` "POLL", `subject`, `poll_count`, `closed_at`, no
  `content`).
- `research/fixtures/post/observed-rich-objects.json`: the type-24 chat messages
  those posts created (message "[Announcement] …"). Their `os` objects were
  `t=3` (a header on both announcement posts), `t=5` with `th` photo
  thumbnails, `t=9` poll with `tt` title and `its` items, `t=1` text and `t=2`
  link with subtype 1 or 4.

## Production changes and regressions

Each change was preceded by a failing production-path test.

- **Rich announcement topics.** Any non-TEXT announcement was treated as
  unsupported and left the previous announcement in the topic. The topic now
  follows the Mac banner order: structured text, content, subject (with the
  poll count suffix), then the posted-media sentence. Only content with nothing
  to show is unsupported, and it keeps the current topic (where the Mac shows an
  update prompt). Replacement, clearing (`{}`) and the existing revision guard
  are unchanged.
- **Rich Boards posts in chat.** The post decoder accepted only text and the
  subtype-1 link, so announcement posts with a photo or poll became
  malformed-payload notices. It now tolerates the header object and link
  subtypes, counts photos, names an attached poll and counts any other object
  rather than rejecting the post; Matrix receives "KakaoTalk post:" with the
  text, "[N photos]", "Poll: …" and a note for objects not shown.

### Deliberate deviations

- Content with nothing to show keeps the current Matrix topic instead of an
  update prompt.
- `json_content` mentions are rendered from their `text`; mention resolution
  and SCHEDULE D-day prefixes are not implemented.
- The bridge refreshes announcements for every bridged group on each
  connection; the Mac refetches when a room is opened.

## Owned encrypted acceptance

Method: the original B secondary profile in the existing owned encrypted A/B/C
regular group. Every native mutation and bridge start/stop wrote a private EXCL
0600 receipt first; no mutation was repeated. The topic was read from the local
homeserver's room state; messages with the tester device.

| Case | Result |
|---|---|
| Offline: A posted an IMAGE announcement, then a POLL announcement; bridge reconnect | topic set to the poll title (the newest announcement) |
| Restart | topic unchanged |
| A announced an existing text (confirming replacement) | topic replaced with that text; the announcement post bridged as a text post |
| A posted an IMAGE announcement online (confirming replacement) | topic replaced with its text; post bridged as text plus "[1 photo]" |
| A removed the announcement | topic cleared |
| Restart | topic still empty; no new Matrix events |

The two offline announcement posts reached Matrix as malformed-payload notices
before the post decoder fix and stay that way (committed); later posts render.

### Acceptance gaps

- VIDEO, FILE, QUIZ and SCHEDULE announcements, multiple polls in one post and
  mentions were not produced live.
- The native banner text was not compared character by character.

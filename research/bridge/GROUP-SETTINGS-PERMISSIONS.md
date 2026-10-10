# Regular-group settings and permissions

Evidence date: 2026-10-10. Scope: personal notification settings, other
personal versus shared chat settings, and member roles and permissions in a
bridged regular group.

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project; no public prior art. High confidence
unless marked.

- **Notifications are local on the Mac.** The per-chat notifications toggle
  only flips a local room flag; no LOCO or HTTP request carries it (only
  CREATE, PCREATE and CWRITE take a `pushAlert` flag at creation). The
  server's chat-data `p` is decoded but never applied, so the Mac does not
  learn a change made on another device. There is no timed mute (`mt` in
  CHATONROOM is an open-chat sending restriction). Mentions, replies to the
  user, keywords and threads can still notify in a muted room (global
  settings, medium confidence). `CHGCHATST`/`SETCHATST` carry business-chat
  status, not notifications.
- **Personal values** use `SETMCMETA {chatId, type, content}` with string
  types — `name`, `imagePath`, `favorite`, `chat_hide` (sent only for memo
  chats), `chat_category` (no caller) — and arrive as `CHGMCMETA`. Shared
  values use `SETMETA` with integer types. Pin order, folders and the Mac
  archive are local.
- **Roles.** Plain regular groups have no owner, host or manager. Any member
  may invite (`ADDMEM {chatId, memberIds}`); there is **no kick** (the kick
  commands and push need an open-chat link); a member can only leave. The Mac
  never sends a shared rename or photo: its editors set the personal value.
  Team chats (a regular group with shared meta 15) have host/staff/member
  roles managed over HTTP and are out of scope here.

Recorded gaps: how the server sets the per-message notification flag, the
mobile command that sets the server-side `p`, server refusal codes for
`SETMETA`/`SETMCMETA`/`ADDMEM`, team-chat endpoints, and chat background and
bubble settings.

## Owned observation

Turning the group's notifications off on the account's own phone (Android)
sent no push to the secondary session; after reconnecting, the secondary saw
`p=false` both in LOGINLIST chat data and in `CHATINFO`. So Android stores
the setting on the server for the account, while the Mac ignores it.

## Production behavior

Each change was preceded by a failing production-path test.

- **Notifications → Matrix mute.** When chat data carries `p`, it maps to the
  Matrix user's own mute (indefinite when notifications are off, unmuted
  when on); an absent value changes nothing. With the framework's default
  `mute_only_on_create`, this seeds the mute only when the portal is created
  and never overrides a later Matrix mute. It never touches shared room
  state.
- **Matrix mute → nothing.** A Matrix mute stays the Matrix user's own
  setting; like the Mac, the bridge sends no request.
- **Roles.** KakaoTalk members keep the default power level. A Matrix change
  to a KakaoTalk member's level is rejected once with a notice and the
  previous levels are restored; Matrix-only users' levels are left to Matrix.
- **Membership.** A Matrix kick or ban of a KakaoTalk member is rejected
  (regular groups have no removal) and the member is restored in Matrix; a
  Matrix invite of a KakaoTalk user is rejected (the bridge does not add
  members yet) and revoked. Self changes and Matrix-only users pass silently.
  Nothing reaches KakaoTalk.
- Unchanged from earlier slices: room name and avatar changes are rejected
  (the Mac only edits personal values), and topic changes are rejected and
  restored ([outbound announcements](GROUP-OUTBOUND-ANNOUNCEMENTS.md)).

### Deliberate deviations

- mooo maps the server-side `p` (set by mobile clients) to the Matrix mute
  at portal creation; the Mac ignores it.
- Invites from Matrix are rejected although KakaoTalk allows any member to
  invite; adding members is a follow-up.

## Owned encrypted acceptance

Method: a fresh build of this branch with the original B secondary profile in
the existing owned encrypted A/B/C regular group. Every native action, Matrix
change and bridge start/stop wrote a private EXCL 0600 receipt first; no
mutation was repeated. Matrix state was read from the local homeserver; the
roster from native A.

| Case | Result |
|---|---|
| Matrix kick of A's ghost | one notice; A's ghost re-joined; native A still shows 3 members |
| Matrix power level 50 for A's ghost | one notice; level restored to the default |
| Notifications off, then on, on B's phone; restarts | no push; shared room name unchanged; no Matrix events |

### Acceptance gaps

- The lab has no double puppet and its homeserver does not deliver room
  account data to the appservice, so the Matrix mute mapping in either
  direction was verified only by production-path tests.
- A live Matrix invite and ban of a KakaoTalk member were not performed.

# Regular-group reactions

Evidence date: 2026-10-10. Scope: per-member reaction add, replace and remove
in an encrypted three-member regular group, both directions, including changes
made while the bridge is offline. Builds on
[reaction compatibility](REACTION-COMPATIBILITY.md) and
[replies and reactions](../replies-and-reactions.md).

The 2026-10-13 static follow-up traces the [room eligibility and scheduling
gates](REACTION-SYNC-SCHEDULING.md): the Mac excludes `PlusChat`, `MemoChat` and
`PartnerChat`, and its open-chat timer is distinct from mooo's reconnect sweep.

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project; no public prior art. Static observations,
not executed fixtures.

- **No reaction state on reconnect paths.** LOGINLIST, reconnect, SYNCMSG and
  GETMSGS chat logs carry no reaction aggregates, and there is no LOCO
  log-meta request. Missed changes are recovered by one HTTP loop:
  `GET /messaging/chats/{chatId}/chat-log/meta/sync-meta?cur&max&cnt` on the
  same host and headers as the members lookup (host and method inferred from
  the shared request builder). The response is
  `{content:[{chatId,logId,linkId,type,revision,content,extra}], last}`.
- **Cursors.** Each room stores `lastLogMetaRevision` (raised by applied metas,
  pushes or the revision returned by the user's own reaction) and
  `lastSyncLogMetaRevision` (the sync cursor). A zero cursor starts from the
  oldest local message's log ID. `max`/`cnt` describe the locally stored metas
  between the cursor and `lastLogMetaRevision`, capped at 300.
- **Apply rule.** A meta row keyed by (log ID, chat ID, type) is replaced only
  by a strictly newer revision. After each page the sync cursor moves to the
  page's highest revision; paging continues while `last` is false or absent,
  with a guard against repeated parameters. An HTTP failure is not retried and
  leaves the cursors unchanged.
- **Triggers.** A full sync runs on the first 10-second timer tick after a chat
  is opened; later ticks run after a successful SYNCMSG when the cursors differ.
  After the user's own reaction the client refetches rather than writing
  optimistically. Only open chats sync; a chat-entry flag can disable it.
- **Mini reactions.** Synced type-2 rows are marked stale and trigger a
  debounced `/emoticon/chat/rx/logmetas` fetch for visible messages.
- **Per-member state.** Only aggregate counts and the viewer's own pick are
  stored; any change arrives as a new aggregate that replaces the row.
  Members and mini details are fetched on demand for the detail view.

Recorded gaps: server interpretation of `cur`/`max`/`cnt` beyond the owned
observation below, the `/rx/logmetas` response model and failure handling, UI
consumers past the update delegate, push create-versus-update for unknown rows,
and the reconnect → chat-entry link for an already-open window.

## Observed fixtures

- `research/fixtures/reactions/observed-android-quick-reactions.json`: owned
  A, B and C applied Android 26.8.2's six quick reactions to one synthetic text.
  All six are mini items (type-2 aggregates) with Korean labels and fixed item
  IDs: heart `1200509_029`, thumbs-up `_021`, check `_037`, laugh `_001`,
  surprised `_002`, sad `_003`. One member can hold several at once.
- `research/fixtures/reactions/observed-sync-meta-page.json`: a read-only
  `sync-meta` GET for the owned group. `cur=0` returned an empty last page; a
  `cur` below the group's metas returned every newer meta (one legacy type-1
  meta with `extra`, one mini type-2 meta) and `last=true` regardless of
  `max`/`cnt`; a `cur` above `max` with `max=0` returned HTTP 400. Revisions are
  log-ID-scale numbers.

## Production changes and regressions

Each change was preceded by a failing production-path test.

- **Offline reaction recovery.** The original reconnect-wide sweep recovered
  missed changes but requested unsupported business-channel metadata (#355).
  The proposed replacement uses explicit room-viewing heartbeats: the first
  eligible tick after ten seconds pages `sync-meta` from the durable per-chat
  cursor (starting at the oldest bridged log). Further requests require known
  newer metadata, a successful own reaction, or unfinished/failed work.
  Only `DirectChat`, `MultiChat`, `OD`, and `OM` are eligible; the chat-entry
  `dlm` stop flag also disables sync. Lookup and delivery failures retain the
  cursor. This proposal has synthetic coverage, with owned acceptance pending;
  the live-push failure policy remains unchanged.
- **Explicit recovery.** In a standard Matrix appservice deployment, run
  `!kakao sync-reactions` in the selected Kakao room (use the configured command
  prefix if different). It checks only that room and uses the same type/entry
  gates and durable cursor. There is no all-rooms option. Explicit requests may
  run immediately but share the login's ten-second admission cooldown and
  single in-flight budget. A longer backlog requires another explicit command;
  failures retain progress and report a bounded error.
- **Quick-reaction emoji.** Android's quick reactions reached Matrix as their
  Korean labels. The six observed mini items now use the same emoji as the
  outbound legacy table (❤️ 👍 ✅ 😆 😮 😢); stored identities remain
  `kakao:mini:<item>`. Reactions bridged earlier keep their old key.
- **One-reaction limit across kinds.** The framework's one-reaction-per-sender
  limit made a Matrix reaction redact the same account's mini reaction (for
  example one added on the phone) although Kakao kept it. The limit is removed;
  after KakaoTalk accepts a new legacy selection, the bridge replaces only that
  sender's previous legacy reaction.

- **Cancel from the stored own pick.** A Matrix redaction used to read
  `/members` before cancelling, a request native never sends there
  ([skew audit](NATIVE-SKEW-AUDIT.md) #5). Removal now compares the redacted
  row with this login's stored legacy reaction on the message and sends only
  the cancel. A stored pick that a push already replaced suppresses the cancel.

### Deliberate deviations

- The proposed viewing policy consumes framework heartbeats rather than a Mac
  window timer. A 15-second heartbeat lapse expires the episode on the next
  signal; an explicit switch cancels in-flight requests. Each admitted run is
  limited to 15 seconds and ten pages, retaining continuation demand afterward.
  Standard Matrix appservices provide no viewing callback, so reconnect alone
  no longer recovers offline reactions there. mooo sends `max=cur` and `cnt=0`
  because it keeps no local meta rows.
- Mini metas are reconciled through the existing detail lookup rather than the
  Mac's `/rx/logmetas` batch fetch.

## Owned encrypted acceptance

These 2026-10-10 observations exercised the former reconnect-wide policy.
They do not validate the proposed viewing-driven scheduler.

Method: a fresh build of this branch with the original B secondary profile in
the existing owned encrypted A/B/C regular group. Every native reaction, Matrix
reaction or redaction, fault and bridge start/stop wrote a private EXCL 0600
receipt first; no mutation was repeated. Matrix results were read with the
tester device; native results with official A's reaction bubbles; stored item
IDs were read from the bridge's reaction mapping.

| Case | Result |
|---|---|
| A heart, A thumbs-up, C check, C laugh, B (bridge account's phone) surprised, C sad | one Matrix reaction each, from the reacting member's ghost, on the encrypted target |
| Matrix ❤️, then 👍 | one Kakao legacy heart, then thumbs-up replacing it; native A shows only "Thumbs up"; Matrix redacted ❤️ and kept every mini reaction |
| A removes its own heart on the phone | Matrix redaction from A's ghost; the same sync restored B's mini reaction removed by the old one-reaction limit |
| Matrix redacts 👍 | the Kakao legacy reaction is cancelled on native A |
| With the bridge stopped, A re-adds heart and C removes sad; then restart | the resync delivered A's ❤️ and redacted C's sad; Matrix's five active reactions matched native A's five bubbles |
| Restart after resync | no new reaction or redaction events |

### Acceptance gaps

- Native B's reactions appear from B's ghost because the lab has no double
  puppet for the bridge account.
- Legacy reaction changes made on another device while offline were not
  produced (only mini changes and the bridge's own legacy reactions).
- A resync backlog longer than ten pages per chat continues on the next
  connection; long backlogs were not exercised.

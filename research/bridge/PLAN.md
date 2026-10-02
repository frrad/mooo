# Matrix bridge plan

Status: active work plan, 2026-09-30. Framework decision:
[ADR 0003](../../docs/adr/0003-bridge-on-mautrix-bridgev2.md).

## Goals

- Bridge one or more operator-owned Kakao profiles into Matrix as a puppeting
  bridge built on mautrix-go `bridgev2`.
- Preserve every client invariant in
  [`client-architecture.md`](../client-architecture.md): the bridge is the sole
  per-profile session owner and sole continuity committer. It never retries an
  ambiguous mutation, and it never triggers read side effects the operator did
  not choose.
- Stay homeserver-neutral. Development runs against a disposable local
  homeserver. The deployment target (a standard appservice, Beeper self-hosting
  via bridge-manager, or both) is decided in B4.

## Layout

- `internal/bridge/connector`: `NetworkConnector`, per-login `NetworkAPI`,
  login flows, and the mapping between Kakao events and remote events.
- `cmd/mooo-bridge`: the executable, built on the framework's standard main.
- The connector talks to the Kakao client through a narrow interface. This keeps
  it testable with fakes and with the existing scripted mock backend, without a
  homeserver.

## Mapping

| Concern | Design |
|---|---|
| Login | Each bridge login owns one `client.Client`, a private auth-state path, and its flock lease. |
| Login flows | A QR "display and wait" flow drives `registration.QRRegistrationService`. An explicit import flow adopts an existing operator-created profile. |
| IDs | Portal = Kakao chat ID. Ghost = Kakao user ID. Message = `chatID:logID`. Outbound messages record the `WriteResponse` log ID. |
| Inbound | `Client.Events()` feeds remote events, which are queued in per-chat delivery order. `CommitEvent` is called only after the framework reports the event as handled. Delivery is at-least-once, and the framework's message-ID dedup absorbs replays. |
| Outbound | Text, reply, photo, and reaction go to the existing client methods. An ambiguous delivery fails the Matrix event and is never resent. |
| Reactions | Kakao pushes aggregate counts (`CHGLOGMETA`). Per-sender Matrix reactions are derived from `ReactionMembers` / `MiniReactionDetails` lookups; the exact reconciliation design is an open question. |
| Own messages | Messages written by the logged-in account on another device are sent through double puppeting. |
| Connection | The bridge decides when to reconnect. Kakao gives it no reconnect of its own. Retries use exponential backoff and then run a bounded `CatchUp`. A dropped connection is reported as a transient disconnect; `KICKOUT` is reported as logged out or bad credentials. |
| Read state | Matrix read receipts go to `MarkRead`. `DECUNREAD` becomes ghost read receipts. Catch-up and backfill use `SYNCMSG`, which can mark messages read on the server, so both are bounded and backfill is opt-in. |
| Chat metadata | No client API exists yet for chat list, names, avatars, members, or profiles. Until B2, portals use placeholder names derived from IDs. B2 uses `CHATINFO` for room metadata and `MEMBER` for profiles ([dossier](../chat-metadata.md)). |

## Phases

### B0: skeleton

- [x] Add mautrix-go (v0.31.0, pure-Go Olm via the `goolm` tag). Scaffold the
      connector and `cmd/mooo-bridge`.
- [x] Document a local dev harness in [`DEV.md`](DEV.md): a disposable
      homeserver plus a generated appservice registration, all kept outside the
      repository.
- [x] Import-profile login for an existing lab profile, restricted to bridge
      admins and to names inside the configured profile directory.
- [x] Bridge text in both directions, and replies inbound. Photos and
      unsupported kinds arrive as notices, so every message event is committed
      in order.
- [x] Spike: with `bridge.portal_event_buffer: 0` and `async_events: false`,
      `QueueRemoteEvent` handles the event inline and returns its real result.
      A queued or backgrounded result is not a commit signal. The connector
      commits only on a finished, error-free result, and the binary forces the
      safe settings.
- [x] Unit tests with a fake client: ordering, the commit rule, non-commit on
      failure (replay after restart), and single-attempt sends.
- [x] Live-validate against the local harness and the disposable accounts
      (2026-09-30; bridge logged in as account B from its clean-room profile,
      official Android 26.8.2 as account A, throwaway Synapse). Import login,
      A→Matrix text, Matrix→A text, clean shutdown, and resumed login after
      restart all worked. Only synthetic text was used.
- [x] Live-found bug: after a restart the framework calls `LoadUserLogin`
      before it creates the login's bridge-state queue, so a queue bound at
      construction was nil and every state was dropped. The queue is now looked
      up at send time; regression test added.
- [x] Live-found bug: the lab profile file is `state.json`, which the profile
      name rule rejected. Dots are now allowed after the first character;
      regression test added.
- [x] Catch up at connect time, before committing live events. Live finding:
      a message sent while the bridge was stopped was skipped, and the next live
      commit moved the cursor past it. The bridge now lists chats it has
      committed before whose server maximum is ahead (`Client.ResumeTargets`),
      recovers each interval with `CatchUp`, bridges and commits those messages
      in order, and only then subscribes to live events (the session buffers
      pushes meanwhile). An unrecoverable interval keeps its recorded gap and
      gets one notice in the room; any other failure aborts the connect. Chats
      never committed are left to opt-in backfill.
- [x] Live-found protocol bug: `CatchUp` declared `cnt=300`, but `cnt` is the
      number of messages the client already holds in the range, so the server
      returned nothing. Catch-up now sends `cnt=0`; validated live (two offline
      messages recovered in order before a live one). Regression-tested.
- [ ] Read-state follow-up: the client records a local read watermark after
      every `SYNCMSG`, including `cnt=0` recovery, which one run suggests does
      not mark messages read. Confirm with an A/B test before B4 read receipts
      rely on that watermark.
- [ ] Exercise the connector through the scripted mock backend. That backend
      lives in `internal/client` tests and is not yet reusable from other
      packages.

### B1: login and media

- [ ] QR login inside the bridge.
- [ ] Photos in both directions. Inbound photo events currently carry no
      author or timestamp; resolve that in the client first.
- [ ] Replies in both directions.
- [ ] Reactions in both directions, with the aggregate-to-per-sender strategy.

### B2: chat metadata (protocol research first)

- [ ] Ghidra-first dossiers for chat info, member lists, member profiles, and
      friend/contact sync, following the parity rules in `AGENTS.md`. Chat
      info, members, and member lists are traced in
      [`chat-metadata.md`](../chat-metadata.md); friend/contact sync and the
      listed live-encoding gaps remain.
- [ ] Client APIs for them, with synthetic fixtures.
      `Client.ChatInfo`, `Client.Members`, and `Client.MemberList` exist
      (`internal/protocol/chatmeta`); friend/contact sync APIs remain.
- [ ] Portal names, avatars, and members. Group portals.
- [ ] Membership events: `NEWMEM`, `DELMEM`, `LEFT`, `CHGCHATST`.

### B3: lifecycle

Reconnect is planned in detail in [`../reconnect.md`](../reconnect.md).

- [ ] Reconnect state machine, `CHANGESVR`, `KICKOUT`, and bridge-state
      reporting.
- [ ] Live-validate the resume and catch-up boundary through the bridge.
- [ ] Opt-in, bounded backfill with an explicit read-side-effect policy.

### B4: polish and packaging

- [ ] Read receipts in both directions, once the read-state dossier settles.
- [ ] Docker image, example configuration, and documentation with no operator
      values.
- [ ] Choose and validate the deployment targets: standard appservice and/or
      Beeper self-hosting.

## Open questions

- A message that fails to bridge stays at the head of its chat's commit queue,
  so later commits in that chat fail until a restart replays it. A persistent
  conversion failure would therefore replay on every restart. Decide on a
  bounded skip policy with an explicit gap record.
- Database: the framework supports cgo SQLite (`sqlite3-fk-wal`) and
  Postgres. Is SQLite enough for the homelab target?
- How should aggregate reaction updates reconcile with per-sender Matrix
  reactions when the detail lookup fails or disagrees?
- Does receiving a message through the bridge change any read-state
  expectations on the primary device?

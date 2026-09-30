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
| Chat metadata | No client API exists yet for chat list, names, avatars, members, or profiles. Until B2, portals use placeholder names derived from IDs. |

## Phases

### B0: skeleton

- [ ] Add mautrix-go. Scaffold the connector and `cmd/mooo-bridge`.
- [ ] Create a local dev harness: a disposable homeserver config plus a
      generated appservice registration. Keep all of it outside the repository
      or gitignored.
- [ ] Import-profile login for an existing lab profile.
- [ ] Bridge direct-message text both ways.
- [ ] Spike: confirm the framework hook that signals a remote event was
      handled, and wire `CommitEvent` to it. Add a crash-replay regression test.
- [ ] Unit tests with a fake client, plus one test through the scripted mock
      backend.

### B1: login and media

- [ ] QR login inside the bridge.
- [ ] Photos in both directions. Inbound photo events currently carry no
      author or timestamp; resolve that in the client first.
- [ ] Replies in both directions.
- [ ] Reactions in both directions, with the aggregate-to-per-sender strategy.

### B2: chat metadata (protocol research first)

- [ ] Ghidra-first dossiers for chat info, member lists, member profiles, and
      friend/contact sync, following the parity rules in `AGENTS.md`.
- [ ] Client APIs for them, with synthetic fixtures.
- [ ] Portal names, avatars, and members. Group portals.
- [ ] Membership events: `NEWMEM`, `DELMEM`, `LEFT`, `CHGCHATST`.

### B3: lifecycle

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

- Which framework hook gives a reliable "handled" signal for commits, and does
  it hold under the framework's async event handling?
- Database driver: cgo SQLite, pure-Go SQLite, or Postgres only?
- How should aggregate reaction updates reconcile with per-sender Matrix
  reactions when the detail lookup fails or disagrees?
- Does receiving a message through the bridge change any read-state
  expectations on the primary device?

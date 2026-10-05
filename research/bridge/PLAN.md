# Matrix bridge plan

Status: active work plan, updated 2026-10-04. Framework decision:
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
- The connector talks to the Kakao client through a narrow interface. Fake-client
  coverage and cross-package scripted-protocol integration are established
  (PR #163); controlled live acceptance remains separate.

## Mapping

This table describes the target design; the baseline and checklists below
distinguish implemented behavior from planned integrations.

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
| Chat metadata | The connector uses `ChatInfo`, `MemberList`, and requested `Members` profiles for initial names and membership. Complete and partial rosters remain distinct; unsupported OpenChat links fail explicitly. Avatars and membership updates are implemented (PR #173), with live encoding and parity gaps ([dossier](../chat-metadata.md)). Friend/contact sync remains separate. |

## Current baseline and execution order

B0 is a working minimal text bridge, with recorded live validation against a
throwaway Synapse on 2026-09-30. Text works in both directions, replies work
inbound, and restart catch-up recovers missed messages for previously committed
chats. Unsupported message kinds become notices. Initial room/member metadata,
outbound replies, bridge-native QR enrollment, and bounded photo transfers
have since landed with synthetic validation;
their direct/group live validation remains outstanding. The framework supplies appservice, encryption,
and double-puppeting machinery; that does not establish deployment validation
for every homeserver or Beeper configuration.

The next milestone is a usable single-user alpha. Execute the work in this
order; the B0–B4 sections below remain feature inventories rather than a strict
phase sequence:

1. **Reliability first (B0/B3):** finish bootstrap ownership and decoder
   cancellation, then exercise the connector through the real scripted backend.
   Cover disconnect during login/subscription, Matrix delivery failure, restart
   replay, shutdown timeout, and exclusive profile ownership. A cleanup timeout
   must retain the owner and block replacement until cleanup succeeds.
2. **Recognizable conversations (B2):** wire the existing metadata APIs into
   room names, avatars, ghost profiles, and initial membership; then implement
   membership updates. Validate direct and group conversations separately.
3. **Bridge-native enrollment (B1):** add QR login using the existing registration
   service, including expiry, cancellation, approval states, and secure profile
   persistence. Keep explicit profile import available.
4. **Everyday messaging (B1):** add outbound replies, then photos both ways after
   resolving inbound author/timestamp fields. Add reactions after defining
   aggregate-to-per-sender reconciliation and lookup-failure behavior.
5. **Controlled reconnect (B3):** add a single-owner state machine with bounded
   backoff, catch-up before live delivery, and distinct server-change versus
   revoked-session handling. Never retry an ambiguous outbound mutation.
6. **Deployment (B4):** produce a Docker image, example configuration, persistent
   volume and upgrade guidance, and validate a standard Matrix appservice
   installation. Evaluate Beeper self-hosting separately before claiming support.

Alpha acceptance requires QR enrollment, recognizable direct/group portals,
text/replies/photos in both directions, tested reaction reconciliation, recovery
across disconnect/restart, and a reproducible single-user deployment. Each feature
needs a source-derived client contract and appropriate synthetic/live validation;
complete official-client parity is not a prerequisite for all bridge work.

Read receipts and opt-in historical backfill follow the alpha. Resolve the
`SYNCMSG` read-side-effect and local watermark discrepancy before enabling either.
Define persistent conversion-failure handling before release: a failed message
currently blocks later commits in that chat, so any bounded skip must record an
explicit gap rather than silently advance the cursor.

## Goal and acceptance evidence

Deliver a usable, self-hostable single-user KakaoTalk–Matrix bridge that enrolls
its own device, presents recognizable conversations, exchanges everyday messages,
and recovers safely across disconnects and restarts.

Completion requires all of the following, with implementation and live evidence
tracked separately:

- Fresh bridge-native QR enrollment, cancellation/expiry, secure persistence,
  and subsequent login without repeating enrollment.
- Correct direct/group names, supported avatars, sender profiles, initial
  membership, and membership changes on existing portals.
- Text, replies, photos, and supported reactions in both directions, with tested
  missing-target, unsupported-kind, and reaction-lookup failure behavior.
- Recovery before live delivery in previously bridged chats; replay deduplication;
  reported unrecoverable intervals; a bounded persistent-conversion-failure policy
  that records gaps rather than silently moving the cursor.
- Exclusive session/checkpoint ownership, bounded cleanup and reconnect, retained
  ownership on cleanup timeout, and distinct server-change/revocation handling.
- Commit only after successful Matrix handling; no automatic resend after an
  ambiguous mutation; a documented and validated catch-up read-side-effect policy.
- A reproducible Docker installation with persistent database/profile storage,
  standard Matrix deployment validation, and separate Beeper compatibility
  evidence with supported configurations and limitations.
- Real scripted-protocol connector tests and controlled owned-account enrollment,
  direct/group messaging, offline recovery, reconnect, and restart tests; local
  checks, secret scanning, and required CI passing before squash merge.

Native QR enrollment (PR #166), photo transfer (PR #167), and scripted-protocol
connector tests (PR #163) have merged. Their synthetic tests do not complete
the live acceptance criteria. Avatars/membership updates (PR #173) and login
collision protection (PR #175)
have also merged with synthetic regression coverage. Reconnect (PR #172) and
reactions (PR #179) remain held for delivery and cleanup corrections. Container
packaging has merged
(PR #170); startup and restart smoke evidence is recorded in
[deployment validation](DEPLOYMENT-VALIDATION.md). Read receipts, historical backfill, cloud backup/restore,
and full official-client parity remain outside this alpha goal.

## Phases

### B0: skeleton

- [x] Add mautrix-go (v0.31.0, pure-Go Olm via the `goolm` tag). Scaffold the
      connector and `cmd/mooo-bridge`.
- [x] Document a local dev harness in [`DEV.md`](DEV.md): a disposable
      homeserver plus a generated appservice registration, all kept outside the
      repository.
- [x] Import-profile login for an existing lab profile, restricted to bridge
      admins and to names inside the configured profile directory.
- [x] Bridge text in both directions, and replies inbound. Unsupported kinds
      arrive as notices. Supported photos now use the B1 transfer path; every
      successfully handled message event is committed in order.
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
- [x] Exercise the connector through the reusable scripted protocol backend
      in `internal/testsupport/loco` (PR #163): failed catch-up aborts before
      live subscription, fresh-process replay precedes live messages, ambiguous
      sends are attempted once, and shutdown releases profile ownership.

### B1: login and media

- [x] Implement bridge-native QR login. The bridge creates a fresh owner-only
      client profile, applies an explicit clean-room QR URL allowlist, drives
      typed approval polling,
      cancellation, and expiry, persists only a complete server-issued
      credential set, and resumes through `LoadUserLogin`. The official Mac
      check-key algorithm and complete success-field parity remain unresolved;
      the URL allowlist is an explicit clean-room safety policy and does not
      claim official-client parity.
- [ ] Live-validate fresh bridge enrollment, device-authorization code,
      expiry/cancellation, and restart resume with the owned disposable
      account. The first bridge-native scan on 2026-10-04 displayed a QR in
      Matrix, but Android rejected it before approval. No fresh credentials
      were installed. The failing stage and corrective regression remain open;
      see [deployment validation](DEPLOYMENT-VALIDATION.md).
- [x] Bounded photos in both directions (PR #167): authenticated Matrix
      streaming download with encrypted-media validation, Kakao upload/download,
      transfer deadlines, and persisted photo source metadata. Optional inbound
      author/timestamp fields have synthetic parser coverage; live encoding
      remains an explicit gap. Image replies are rejected.
- [ ] Live-validate direct/group photo transfers, encrypted Matrix media, and
      author/timestamp attribution.
- [x] Inbound reply conversion with chat-scoped source message IDs.
- [x] Outbound replies with persisted source metadata, explicit missing-source
      rejection, chat/receiver guards, UTF-16-bounded previews, and single-attempt
      sends. Synthetic connector and SQLite round-trip tests pass (PR #164).
- [ ] Live-validate outbound replies, including reply after bridge restart.
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
- [x] Initial portal names and member profiles/rosters from existing client APIs
      (PR #162). Complete versus partial membership is explicit; unrequested
      profiles and invalid IDs are rejected.
- [ ] Live-validate initial metadata in direct and group portals.
- [x] Portal and ghost avatars, plus updates to existing portal metadata (PR #173).
      HTTPS CDN policy, byte bounds, and redacted failures are synthetic-tested;
      direct/group live validation remains outstanding.
- [x] Membership events: `NEWMEM`, `DELMEM`, `LEFT`, `CHGCHATST` (PR #173).
      Partial rosters preserve explicit joins/leaves even when profile lookup
      fails. Existing-portal live validation remains outstanding.

### B3: lifecycle

Reconnect is planned in detail in [`../reconnect.md`](../reconnect.md).

- [x] Report connection state and distinguish terminal `CHANGESVR` and `KICKOUT`;
      stop accepting later events after either terminal notice.
- [x] Bound active-session disconnect and retain cleanup ownership after a
      shutdown timeout; concurrent disconnects share the admission/deadline gate.
- [x] Complete bootstrap ownership before subscription and on failed connect;
      join/cancel the typed decoder on idle input and blocked output. Concurrent
      closed event admission is regression-tested (PR #160).
- [ ] Reconnect state machine with bounded backoff, exclusive ownership, and
      catch-up before live delivery. Current recovery requires a bridge restart.
- [x] Recorded live restart resume/catch-up validation for previously committed
      chats on 2026-09-30.
- [ ] Extend resume/catch-up validation to automatic reconnect, terminal events,
      delivery failures, and cleanup timeouts.
- [ ] Opt-in, bounded backfill with an explicit read-side-effect policy.

### B4: polish and packaging

- [ ] Read receipts in both directions, once the read-state dossier settles.
- [x] Docker image, example configuration, and documentation with no operator
      values (PR #170). Authenticated appservice startup/restart smoke passed;
      full messaging deployment acceptance remains separate.
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

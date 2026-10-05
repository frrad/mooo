# Matrix bridge plan

Status: active work plan, updated 2026-10-05. Framework decision:
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
| Reactions | Kakao pushes aggregate counts (`CHGLOGMETA`). PR #179 reconciles per-sender Matrix reactions from `ReactionMembers` / `MiniReactionDetails` with checked add/remove operations, database postconditions, replay-safe revision persistence, and explicit lookup/failure handling. Outbound failure categories and the no-retry rule are documented in [`reaction-failure-policy.md`](reaction-failure-policy.md). Live acceptance remains open. |
| Own messages | Messages written by the logged-in account on another device are sent through double puppeting. |
| Connection | The bridge decides when to reconnect. Kakao gives it no reconnect of its own. Retries use exponential backoff and then run a bounded `CatchUp`. A dropped connection is reported as a transient disconnect; `KICKOUT` is reported as logged out or bad credentials. |
| Read state | Matrix read receipts go to `MarkRead`. `DECUNREAD` becomes ghost read receipts. Catch-up and backfill use `SYNCMSG`, which can mark messages read on the server, so both are bounded and backfill is opt-in. |
| Chat metadata | The connector uses `ChatInfo`, `MemberList`, and requested `Members` profiles for initial names and membership. Complete and partial rosters remain distinct; unsupported OpenChat links fail explicitly. Avatars and membership updates are implemented (PR #173), with live encoding and parity gaps ([dossier](../chat-metadata.md)). Friend/contact sync remains separate. |

## Current baseline and execution order

B0 is a working minimal text bridge, with recorded live validation against a
throwaway Synapse on 2026-09-30. Text works in both directions, replies work
inbound, and restart catch-up recovers missed messages for previously committed
chats. The 2026-10-04 native run additionally exercised direct own-device text,
replies, and Matrix-to-Kakao photos; a 2026-10-05 (UTC) follow-up exercised
Kakao-to-Matrix photos. A separate 2026-10-05 (UTC) existing-portal crypto run
configured Megolm, synchronized a tester, and sent one encrypted Matrix text
that the bridge decrypted and persisted (message count 8 to 9). The encrypted
Matrix-to-Kakao write was acknowledged with positive source IDs, and the owned
B phone displayed the exact fixture in the existing self-chat. A 2026-10-05
Docker resume exercised
the existing profile, one text, one encrypted-media attachment in an
unencrypted portal, and one persistent restart; the reply and reaction probes
were rejected/failed and were not retried. Group/other-participant delivery,
inbound encrypted Kakao-to-Matrix text, encrypted media parity, reaction
acceptance, and broader recovery remain open. The framework supplies appservice,
encryption, and double-puppeting machinery; that does not establish deployment
validation for every homeserver or Beeper configuration.

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
Deterministic typed-photo failures now become persisted notices under their
original source IDs (PR #181; [policy](conversion-failure-policy.md)). Transient
transfer or Matrix failures remain uncommitted. Parser failures with validated source identities now become explicit notices;
PR #187 has merged with continuity guards. PR #197 classifies validated
chat/log positions with invalid content metadata as explicit `Type=0` gaps;
unidentifiable envelopes stop admission
without inventing a cursor and report the stable `kakao-unidentifiable-message`
state without automatic reconnect; operator recovery remains an open acceptance
item ([policy](continuity-failure-policy.md)).
Neither a silent cursor advance nor an indefinite chat block satisfies alpha
acceptance.

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
have also merged with synthetic regression coverage. Reconnect (PR #172) has merged with real scripted recovery and shutdown
regressions. Reactions (PR #179) have merged with actual framework
failure/replay coverage; direct/group and live reaction acceptance remains open. Container
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
      rely on that watermark; see
      [`syncmsg-read-side-effect-procedure.md`](syncmsg-read-side-effect-procedure.md).
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
      were installed. PR #221 records a fresh default Low-renderer trial with
      exact selected-image identity and payload equality; Android rejected it,
      and the failing stage and underlying cause remain unknown. No QR-info
      endpoint conclusion is established from this run, and the corrective
      regression remains open;
      see [deployment validation](DEPLOYMENT-VALIDATION.md).
- [x] Bounded photos in both directions (PR #167): authenticated Matrix
      streaming download with encrypted-media validation, Kakao upload/download,
      transfer deadlines, and persisted photo source metadata. Optional inbound
      author/timestamp fields have synthetic parser coverage; live encoding
      remains an explicit gap. Image replies are rejected.
- [ ] Live-validate direct/group photo transfers, Matrix room E2EE media, and
      author/timestamp attribution. The Docker attachment probe covered
      encrypted media transport in an unencrypted portal only.
- [x] Inbound reply conversion with chat-scoped source message IDs.
- [x] Outbound replies with persisted source metadata, explicit missing-source
      rejection, chat/receiver guards, UTF-16-bounded previews, and single-attempt
      sends. Synthetic connector and SQLite round-trip tests pass (PR #164).
- [ ] Live-validate outbound replies, including reply after bridge restart. The
      Docker synthetic-target probe was rejected before Kakao mutation.
- [x] Reactions in both directions, with checked aggregate-to-per-sender
      reconciliation, replay-safe revisions, and explicit Matrix failure
      handling (PR #179). Live direct/group acceptance remains outstanding; the
      Docker heart probe produced the connector's generic mutation failure
      (PR #219); the retained evidence cannot distinguish server rejection
      from transport or another ambiguous outcome, so no server-rejection
      claim is made and it was not retried.

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

The implemented supervisor and its bounded policy are documented in
[`../bridge-reconnect-design.md`](../bridge-reconnect-design.md).

- [x] Report connection state and distinguish terminal `CHANGESVR` and `KICKOUT`;
      stop accepting later events after either terminal notice.
- [x] Bound active-session disconnect and retain cleanup ownership after a
      shutdown timeout; concurrent disconnects share the admission/deadline gate.
      PR #223 additionally covers a real framework delivery blocked during the
      event pump: repeated cleanup retains the same owner until that pump joins,
      then admits a fresh connection.
- [x] Complete bootstrap ownership before subscription and on failed connect;
      join/cancel the typed decoder on idle input and blocked output. Concurrent
      closed event admission is regression-tested (PR #160).
- [x] Reconnect state machine with bounded backoff, exclusive ownership, and
      catch-up before live delivery (PR #172). The real scripted backend proves
      missed-message recovery before subscription, old-lease release before
      replacement, one outbound WRITE despite a dropped response, terminal
      KICKOUT, and CHANGESVR recovery.
- [x] Require confirmed gap notices before live subscription (PR #198), and
      stop safely on unidentifiable message identity while preserving
      identity-bearing content gaps (PR #197). These are synthetic/scripted
      validations; live recovery and operator tooling remain open.
- [x] Recorded live restart resume/catch-up validation for previously committed
      chats on 2026-09-30.
- [ ] Extend controlled owned-account resume/catch-up validation to automatic
      reconnect, terminal events, delivery failures, and cleanup timeouts.
      Synthetic regressions cover these paths; live acceptance remains open.
- [ ] Opt-in, bounded backfill with an explicit read-side-effect policy.

### B4: polish and packaging

- [x] Crypto startup and persistence smoke (PR #224): goolm-backed
      `CryptoHelper` schema/device reopening and clean shutdown are validated
      against the existing bridge database. This scope covers initialization
      and persistence only; it does not prove Matrix room E2EE or encrypted
      message/media delivery.
- [ ] Read receipts in both directions, once the read-state dossier settles.
- [x] Docker image, example configuration, and documentation with no operator
      values (PR #170). Authenticated appservice startup/restart smoke passed;
      a bounded existing-profile text/media resume and one restart are recorded
      in [direct messaging validation](DIRECT-MESSAGING-VALIDATION.md); full
      messaging deployment acceptance remains separate.
- [ ] Validate both required deployment targets: standard Matrix appservice
      installation and separate Beeper self-hosting.

## Open questions

- How should operator recovery surface an unidentifiable malformed MSG without
  inventing a cursor? Typed photo notices are implemented, and parser-gap
  continuity guards have merged (PR #187). Infrastructure and Matrix
  failures remain uncommitted and recoverable.
- Database: the framework supports cgo SQLite (`sqlite3-fk-wal`) and
  Postgres. Is SQLite enough for the homelab target?
- How should aggregate reaction updates reconcile with per-sender Matrix
  reactions when the detail lookup fails or disagrees?
- Does receiving a message through the bridge change any read-state
  expectations on the primary device?

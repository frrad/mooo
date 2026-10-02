# Plan

This is the working plan. Findings can reorder it; each phase should leave behind
sanitized, reproducible evidence.

## Phase 0 — foundation

- [x] Choose Go, a public MIT-licensed repository, and a modular architecture.
- [x] Create `frrad/mooo` and protect the default branch with required CI.
- [x] Establish an Android 15 emulator and baseline macOS analysis tooling.
- [x] Inventory the installed KakaoTalk client without logging in.
- [x] Complete the initial public prior-art survey and clean-room workflow decision.

## Phase 1 — controlled lab

- [x] Sign the emulator into a lab Google account, install a signature-verified
      official KakaoTalk package, and create a disposable test account.
- [x] Record emulator, Android, KakaoTalk Android, and macOS client versions.
- [x] Define external and ignored locations for sensitive captures and notes.
- [x] Let the disposable account's automated user-protection restriction age out;
      a single controlled retry on 2026-09-23 cleared the `-997` approval block,
      although the Mac session then failed its server connection and did not
      remain registered.
- [x] Complete a clean-room QR authorization, persist the returned client-owned
      state, reconnect from fresh processes, and receive one synthetic self-chat
      message on 2026-09-28.
- [ ] Establish repeatable experiments for login, device registration, reconnect,
      logout, and revocation.
- [ ] Determine which observations are possible through logs, metadata, static
      analysis, and authorized traffic inspection.

## Phase 2 — protocol specification

Execution through first text send/receive follows
[`research/client-through-messaging-plan.md`](research/client-through-messaging-plan.md).

- [ ] Map secondary-device authentication and approval states; see
      `research/device-registration/PLAN.md`.
- [ ] Identify endpoints, framing, serialization, cryptographic boundaries, and
      session lifecycle without publishing live secrets; see
      `research/session-login/PLAN.md`.
- [ ] Determine how candidate macOS preference values are transformed and, if
      applicable, specify a versioned local recovery recipe; see
      `research/credential-storage/PLAN.md`.
- [ ] Specify chat/contact synchronization and message send/receive behavior.
- [ ] Create synthetic fixtures and a conformance-oriented protocol model.

## Phase 3 — Go protocol client

- [x] Generate a new client-owned device identity and complete QR-based secondary-
      device authorization without importing official Mac state.
- [x] Persist only authentication state returned to that new identity, with
      redacted diagnostics and secure local storage boundaries.
- [x] Implement and synthetically verify LOCO framing, BSON, and secure transport.
- [x] Implement `GETCONF` -> `CHECKIN` -> `LOGINLIST` in the bounded lab probe and
      complete one bounded disposable-account login.
- [x] Implement and validate one-shot refresh-token rotation under the profile
      lease, followed by exactly one fresh LOGINLIST attempt.
- [ ] Specify and validate inbound text delivery, acknowledgements, cursors,
      deduplication, reconnect behavior, and the fact that SYNCMSG catch-up may
      have read side effects.
- [ ] Complete the Ghidra-first parity dossiers in `research/protocol-parity.md`,
      beginning with continuity, acknowledgement/read-state, membership/chat
      changes, mutation identity, and reconnect lifecycle.
- [x] Specify and validate one explicit outbound text send with safe idempotency
      and no automatic retry after ambiguous delivery.
- [ ] Implement transport, framing, and serialization packages.
- [ ] Implement credential/session storage interfaces with secure defaults.
- [ ] Implement device login and reconnect state machines.
- [ ] Add synchronization, then text receive/send, with read side effects
      modeled explicitly.
- [ ] Specify and validate read-state semantics: DECUNREAD and NOTIREAD,
      explicit markAsRead/read-all routing through SYNCMSG, local CHATOFF
      teardown, and the required separation of delivery, commit, and read
      state.
- [x] Introduce a long-lived client owner that lazily establishes and reuses one
      session, validates response-body login status, completes login paging, and
      never reconnects or retries mutations implicitly.
- [x] Implement and live-validate single-photo send and idle receive/download
      between owned disposable accounts, with exact-byte checksum verification,
      bounded parsing, and no automatic retry of ambiguous upload stages.
- [x] Add a deterministic scripted Kakao backend harness that exercises the real
      client across booking, check-in, paginated login, renewal, idle pushes,
      text, photo upload, and ambiguous-disconnect/no-retry behavior.
- [x] Add a single-consumer typed event stream for inbound text, photos,
      unsupported message types, unknown methods, and non-fatal decode errors.
- [x] Implement and live-validate direct-message replies, reaction mutation,
      aggregate reaction events, and reaction-member attribution using the
      persisted session profile.
- [x] Live-validate the mini/custom reaction detail endpoint and add it as a
      separate, Mac-compatible data source without conflating it with legacy
      reaction-member attribution.
- [ ] Add a private versioned continuity checkpoint and make the long-running
      Matrix/Beeper bridge the exclusive per-profile session owner. On bridge
      restart, perform one cursor-based resumed login rather than QR
      authorization or a retry loop.
- [x] Implement the private versioned checkpoint, explicit application commit
      boundary, resumed `LOGINLIST` cursor inputs, duplicate suppression, and
      bounded no-progress-detecting `SYNCMSG` recovery with synthetic tests.
- [ ] Live-validate the resume/catch-up boundary and wire the eventual bridge as
      the sole checkpoint committer and per-profile session owner.
- [ ] Verify against the disposable account and add regression tests.

### Durable parity implementation pipeline

Small parity slices move through one repeatable sequence: an approved public
specification and synthetic characterization tests are reviewed first; an
implementation agent then writes an independent reducer or decoder against
that contract; the branch runs the repository checks and is squash-merged only
after CI reports it mergeable. The CHGMETA slice is complete through its
bounded effect-selection reducer gates. CHGMCMETA tracing and the chat-metadata API remain
separate follow-up work, while persistence, failure behavior, and other
unresolved effects stay outside this slice until their contracts are reviewed.

The reconnect owner is progressing through the same boundary. The current
feature branch has a deterministic, injected relative timer owner with
generation invalidation and terminal shutdown, exercised through the real
Session completion seam. The production Session binds that owner before the
first LOGINLIST request; LOGINLIST/LCHATLIST use the single reader's
correlated raw-request path, which schedules before page interpretation and
buffers unsolicited packets until bootstrap handoff. Tests cover empty-BSON
PING delivery during the completion-to-next-request gap, bounded cancellation,
and failed-bootstrap cleanup. Remaining runtime work is explicit: cover
push-receipt cancellation/scheduling and preserve shutdown generation guards
through every terminal fan-out path.
Mapping the serialized ping configuration key and claiming official queue
timing remain separate evidence gaps.

### Default status/config owner binding proposal

The next reconnect slice is a constructor-bound integration layer for the
reviewed status and timeout contracts. It must be implemented on a branch based
on the current main and reviewed independently from the opt-in asynchronous
submission adapter.

The constructor should receive three distinct inputs: the `LocoAgent` transport
status source used by the status-3 producer gate, the out-segment timeout
configuration source, and the queue/clock owner dependencies. Manager status
values (`0x15`, `0x16`, `0x17`, `0x1a`) must not be used as the agent status
source. The owner and these providers must be immutable before `readLoop` or
the first LOGINLIST admission begins; a late setter is not an acceptable
lifecycle seam.

The binding sequence is: establish the carriage and its status source, create
the receive-header and out-segment owners, bind them to the Session, start the
single reader, and then admit LOGINLIST. At request execution, status other than
3 must return the reviewed producer failure without packet allocation, socket
write, or receive-header timeout admission. Status 3 uses the existing exact
method-and-ID correlation and async submission path. The out-segment owner reads
configuration at admission and again only for queued enable execution; a
non-positive value skips timer admission while allowing the write to complete.

The integration must keep queue operations enqueue-only and must not invoke
Session, socket, or timer callbacks while holding Session or owner locks. The
worker is FIFO and bounded. `Close` interrupts and is safe from callback
context; external teardown uses `Session.Shutdown(ctx)` to join the worker with
a caller deadline. Terminal write errors resolve their exact pending request
before reader fan-out can replace the original error. Partial progress disables
the out timer while retaining the request; ambiguous partial failure closes the
carriage without retry.

Required synthetic and real-client tests are:

1. Constructor ordering and immutable owner binding before reader startup.
2. Status 3 versus non-3 execution, with assertions for allocation, write, and
   receive-timeout effects.
3. Positive, zero, and negative timeout configuration, including queued
   execution reread and no-reread cancellation.
4. LOGINLIST/LCHATLIST accepted-status handling, preserved initial pushes, and
   EOF/close cleanup through the same reader.
5. Exact UID response correlation, wrong-method same-ID routing, partial
   progress before terminal completion, zero-byte error, ambiguous partial
   error, queued cancellation, full-write cancellation, and bounded Shutdown.

The serialized configuration key/override and initial admission source remain
unresolved evidence inputs. The startup manager's observed 15/20/10/10
configuration values are documented separately; their mapping into this
constructor and the serialized override key still require source evidence.
Until those inputs are landed as public contracts, the default constructor
must not activate these owners or invent a fallback status.

### Existing ownership and shutdown call sites

`Client` is the sole production owner of a live `Session`: `ensureSession`
stores the lazily connected session under `Client.mu`, and chat, sync, media,
metadata, and event APIs all obtain that same pointer. The connect path creates
the Session after secure carriage setup and starts `readLoop` before the first
LOGINLIST request. The future binding point is therefore between carriage
assignment and reader startup, before LOGINLIST admission. The bridge reaches a
Session only through `Client`; there is no second bridge-owned carriage owner.

`Client.Close` currently holds `Client.mu` while calling `Session.Close`, then
marks the checkpoint clean and releases the profile lease. A bounded worker
join must not be added under that lock because worker callbacks can complete
pending Session requests and may call higher-level code. A future context-aware
Client shutdown should capture the Session while holding `Client.mu`, mark the
Client closed, unlock, call `Session.Shutdown(ctx)`, and finalize the
checkpoint and lease only after the join succeeds. If the deadline expires,
the exclusive profile lease and checkpoint ownership must remain held so a
later shutdown can retry the join; marking the Client closed must not discard
the live Session reference. Existing `Close` call sites and test dials use
interrupt-only cleanup and must remain valid until that API is approved.

The integration tests belong at two levels. Session tests should exercise
constructor binding, reader startup, status/config gates, UID correlation,
worker joins, and terminal fan-out on scripted carriages. Client tests should
inject a dialer returning that Session, assert one shared owner across
concurrent operations, and verify bounded shutdown releases the session,
checkpoint, and profile lease without holding `Client.mu` during the worker
join. A timeout case must assert that the lease remains held and a later
successful join releases it; a closed Client must reject new operations during
that interval.

## Phase 4 — Matrix/Beeper bridge

Execution follows [`research/bridge/PLAN.md`](research/bridge/PLAN.md).

- [x] Reassess current mautrix-go and Beeper bridge conventions; adopt
      `bridgev2` ([ADR 0003](docs/adr/0003-bridge-on-mautrix-bridgev2.md)).
- [ ] Define identifier mapping, portals, puppeting, backfill, and state recovery.
- [ ] Implement standard Matrix application-service behavior while preserving
      Beeper compatibility.
- [ ] Package for a single-user Linux homelab deployment without baking in any
      operator-specific values.

## Questions to resolve through evidence

- Which official clients are treated as secondary devices, and what are their
  concurrent-device and approval rules?
- Does the macOS client share protocol behavior with Windows or tablet clients?
- Where are device credentials generated and stored, and how are they revoked?
- Which parts of transport and payloads are encrypted independently of TLS?
- What server-visible properties distinguish official secondary devices?

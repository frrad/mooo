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

Parity work follows [`research/parity-fixtures.md`](research/parity-fixtures.md).
A slice starts from a traced official-client chain and a synthetic fixture under
`research/fixtures/` with explicit provenance. If mooo already has the code path,
a CI test feeds the fixture to that production code, red first when it differs,
and the branch fixes mooo or records a deliberate deviation. If mooo has no code
path yet, the slice ends at the research document and fixture: no Go model of
the official client and no test-only production code. Parity is claimed only
against `executed` or `observed` fixtures; a lab harness upgrades `static`
fixtures by running the official client's own code on the same inputs.

The CHGMETA slice, the reconnect owner, and the push-receipt work below predate
this rule. Their status text is historical and is being reconciled with the
code during the migration below.

### Parity test migration

Audit of `main` at 94dc92e (2026-10-05): 45 test files checked self-contained
models against fixtures without calling mooo, and about 25 non-test files were
reachable only from tests (`deadcode -tags=goolm ./...`). The production
receive path decodes BSON with mongo-driver and implements none of the decoder
behaviour those fixtures describe.

- [x] Record the policy in `AGENTS.md` and `research/parity-fixtures.md`, and add
      the `internal/testpolicy` lint with a shrink-only allowlist.
- [x] Delete the 45 model-only tests and empty the allowlist.
- [x] Delete test-only production code with no planned consumer: the
      pre-QR passcode registration state machine and coordinator, the
      sessionlogin ping-intent, ping-status, status-handler, send-order,
      reconnect-policy, in-segment-timeout, and token cursor/helper/observer
      models, the CHGMETA/CHGCHATST/CHGMCMETA reducers, and `loco.Parser`.
- [ ] Move fixtures to `research/fixtures/` with `provenance`.
- [x] Read receipts: the bridge now drives `MarkRead` and `DECUNREAD`
      directly; `readstate` and `notiread` were deleted (#232).
- [ ] Connect or delete the remaining test-only code (`deadcode -tags=goolm
      ./...`). Each is a real capability that production never binds:
  - Push delivery receipts: `Session.BindPushReceipt`, the sessionlogin
    `push_receipt_*` owners, `receipt_*` builders, `sgjson_object`,
    `foundation_ti`, and the client out-segment worker/submitter. Needs a
    product decision.
  - Receive-header timeout: `sessionlogin.NewReceiveHeaderTimeoutOwner` and
    its effects planner. Session has the controller seam, but production
    never installs one, so no hung-read timeout is active.
  - Connection recovery: `sessionlogin.ReduceRecovery`, `EndpointCache`, and
    `InvalidateMatchingFailure`.
- [ ] Run fixtures with a production equivalent (BSON decoding, events, receive
      path, LOCO framing) through the real code; land each difference as a
      failing test, then fix it or record a deviation.
- [ ] Build the first lab harness for the official BSON dictionary decoder and
      upgrade its invalid-UTF-8 fixture to `executed`.

### Push-receipt transport integration plan

The reviewed manager and carriage-agent owners are intentionally transport
independent. Session now has a generic, transport-independent opt-in receipt
hook: `BindPushReceipt` installs an injected sender and eligibility predicate
before reader startup, `dispatchPushReceipt` runs it only for unmatched packets,
and shutdown waits for the binding worker and any optional sender owner. The
ordinary raw/typed push stream remains intact. The hook forwards the unmatched
input `loco.Packet` to the injected sender; it does not build, serialize,
encrypt, or write a receipt packet itself.

The source-derived receipt body and packet constructor contracts are reviewed.
The remaining integration work is to resolve typed, source-qualified incoming
eligibility through the official parser, notice decoder, and handler chain, then
compose the reviewed body/packet/encryption/write layers behind the existing
opt-in hook. The adapter must remain opt-in and must not activate receipt
sending by default.

The adapter must keep receipt sending separate from the existing out-segment
worker until the receipt packet shape, serialization, callback-tag mapping, and
completion semantics are approved. The worker currently accepts serialized
payloads, reports partial/ambiguous write progress, and closes the carriage on
partial write failure; it has no receipt-specific packet identity or callback
tag contract. Reusing it prematurely would conflate reviewed receipt admission
with unresolved packet construction and write completion behavior.

The signed tag derived by the agent owner is an admission/send argument. It
must be tracked separately from any lower socket-write tag. The base
`LocoAgent` lower socket path zero-extends the uint32 packet-header ID and
ignores the signed admission tag; an override exists in `LocoNWAgent`.
Active carriage transport selection and runtime callback mapping remain
unresolved, so this plan does not prescribe a wire-tag field.

The current implementation confirms this boundary concretely: `OutSegmentSubmitter`
accepts only `[]byte`, arms its timeout owner after worker admission, and forwards
`OutSegmentWriteResult` without any signed tag or packet identity. A blocked write
is interrupted by `Close`; a partial write or zero-byte-after-progress failure
terminates the submitter/carriage and is not retryable. These behaviors pass the
existing race-enabled synthetic worker tests, but they cannot report whether a
receipt was accepted, written, or acknowledged.

Pending-request correlation is likewise separate. `dispatchPacket` keys waiters
by method plus packet ID and routes misses as pushes; a negative receipt tag is
an agent send argument and must never become a Session request ID or pending-map
key. Any future receipt completion path must define whether it is fire-and-forget
or correlated before adding entries to the pending maps.

The request path inserts a positive uint32 ID into all three pending maps before
serialization and submission. A receipt adapter that casts the signed tag back
to uint32 could produce values outside the bounded request range or accidentally
reuse a packet ID under a future wider allocator. Receipt admission therefore
must not add the signed send argument to Session pending maps or use it as a
request ID. Inbound receipt or push handling remains a separate dispatch path;
the source does not yet establish an acknowledgement method or wire identity.

Required synthetic integration coverage after request approval:

1. An eligible unmatched push reaches the injected receipt callback while the
   ordinary push stream remains ordered and usable.
2. Manager cancellation and scheduling retain the manager instance target,
   while inline dispatch resolves the current carriage agent instance.
3. Agent status is reread at execution; non-3 suppresses packet access and
   sending, and status 3 preserves the uint32 packet identity and signed
   admission argument at the owner boundary. A separate test must prove the
   eventual lower socket-tag mapping once its source trace is corrected.
4. The receipt path never inserts the signed send argument into request
   correlation. Any unsolicited receipt acknowledgement remains an inbound
   dispatch concern until an approved method/ID model defines whether it can
   complete a waiter.
5. Out-segment partial progress, zero-byte errors, ambiguous failures, close,
   and bounded shutdown are exercised only after the receipt serialization and
   completion contract specifies how they map to receipt outcomes.

The manager/agent composition and Session opt-in lifecycle are now covered by
merged owner and session tests. Remaining work is typed eligibility and the
source-qualified builder/encryption/write composition; no default activation is
authorized.

### Push-receipt builders and remaining integration boundaries

The merged scalar conformance tests cover boolean, double (including signed
negative zero), signed int32, signed int64, and null BSON payload widths.
Unsupported Objective-C encodings remain fixture rejection cases; they do not
justify a generic fallback converter.

The merged typed `BuildReceiptBody` composes the reviewed SGJSON projection,
static `method` / `packetId` removal, BLOCKSYNC renames, and production BSON
encoder. HINT produces the canonical five-byte empty BSON document. BLOCKSYNC
produces `r` and `pr` as signed BSON int32 values, including zero and signed
boundary values; its body is 20 bytes. BSON map iteration does not establish a
wire key order. The source contract is
[`PUSH-RECEIPT-BODY-COMPOSITION.md`](research/reconnect/PUSH-RECEIPT-BODY-COMPOSITION.md).

The merged `BuildReceiptPacket` takes an explicit uint32 packet ID and method,
copies them to the LOCO header, and frames the built body. It uses header status
zero, BSON body type zero, and the actual body length. Tests cover exact HINT
bytes and BLOCKSYNC field types and payloads independently of map order.
The reviewed constructor boundary is
[`PACKET-CONSTRUCTOR-CONTRACT.md`](research/reconnect/PACKET-CONSTRUCTOR-CONTRACT.md).
Neither builder allocates request IDs, touches pending maps, encrypts, writes,
or activates a Session path.

The outer adapter remains layered: typed receipt model -> SGJSON
projection/mapping -> BSON body -> packet-data framing -> packet encryption ->
connection write. The merged outer encryption specification records the
no-crypto identity path and crypto length-prefix/append behavior in
[`ENCRYPT-PACKET-CONTRACT.md`](research/reconnect/ENCRYPT-PACKET-CONTRACT.md).
The AES-GCM primitive has a separate bounded contract in
[`V2SL-GCM.md`](research/session-login/V2SL-GCM.md). These encryption
contracts alone do not prove composed receive and caller failure handling.
The agent owner's signed admission tag remains separate from the uint32 header
ID and lower socket tag.

Before runtime binding, complete these remaining client boundaries:

1. Trace incoming header/body parsing through HINT/BLOCKSYNC notice decoding
   and handler dispatch. A framing-level zero-body packet and an outbound
   empty BSON receipt do not establish typed incoming notice eligibility.
2. Validate production encryption against the reviewed primitive, including
   authentication failure without plaintext, and trace its surrounding caller
   failure handling.
3. Preserve manager scheduling/cancellation targets, execution-time agent
   status, uint32 packet identity, and the reviewed lower transport callback
   semantics through real receipt submission.
4. Exercise ordered unmatched-push delivery, partial and ambiguous writes,
   cancellation, and bounded shutdown through the composed opt-in path.

Keep request correlation separate until an acknowledgement method/ID contract
establishes it. Receipt sending remains opt-in and has no default Session
activation.

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
- [x] Implement the bridgev2 skeleton, profile-import login, identifier mapping,
      bidirectional text, inbound replies, and commit-after-Matrix handling.
- [x] Live-validate the minimal bridge and restart catch-up against a disposable
      Synapse with owned disposable accounts.
- [ ] Complete the single-user alpha in bridge-plan execution order: lifecycle
      reliability, room/member metadata, QR enrollment, replies/photos/reactions,
      controlled reconnect, and deployment validation.
- [ ] Add opt-in backfill and read receipts after settling read-state semantics.
- [ ] Validate standard Matrix appservice deployment and Beeper compatibility.
- [ ] Package for a single-user Linux homelab deployment without baking in any
      operator-specific values.

## Questions to resolve through evidence

- Which official clients are treated as secondary devices, and what are their
  concurrent-device and approval rules?
- Does the macOS client share protocol behavior with Windows or tablet clients?
- Where are device credentials generated and stored, and how are they revoked?
- Which parts of transport and payloads are encrypted independently of TLS?
- What server-visible properties distinguish official secondary devices?

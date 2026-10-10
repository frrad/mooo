# Plan

This is the working plan. Findings can reorder it; each phase should leave behind
sanitized, reproducible evidence.

Last code/evidence audit: 2026-10-09, committed baseline `8caaca0` (PR #267).
Implementation, synthetic coverage, and owned-account acceptance are tracked
separately; in-progress branches are not counted as shipped features.

## Current feature gaps and next work

The client and bridge already support client-owned QR enrollment, secure leased
profiles, resumed login, durable commit/deduplication, bounded bridge reconnect,
text/replies/photos/reactions, read-receipt routing, initial room/member metadata,
operator chat listing and contact thumbnails. Inbound conversion additionally
supports video, audio, files, contacts, profiles, locations, stickers, URL-based
albums, text polls/posts and observed nonanimated Mini emoticons. Regular
three-person encrypted Matrix text and normal restart recovery have passed.
These are scoped capabilities, not full official-client parity.

| Priority | Missing implementation or acceptance | Code/evidence boundary |
|---|---|---|
| 1 | Regular-group announcements; rename, join/leave and metadata lifecycle acceptance | Inbound text and rich Boards announcements project to the room topic (PRs #269, #283); Matrix topic changes are explicitly rejected and restored because the Boards write contract is untraced. Initial shared title/roster works. Membership handlers exist, but live mutations remain unvalidated. See [chat metadata](research/chat-metadata.md) and [group evidence](research/bridge/GROUP-MESSAGING-VALIDATION.md). |
| 2 | Remaining group media acceptance | Group photos (PR #277), text and image replies (PRs #278–279) and reactions (PR #280) are accepted. |
| 3 | Live read-receipt and catch-up read-side-effect policy; operator malformed-message recovery; terminal/failure lifecycle acceptance | Routing, bounded recovery and ownership are implemented; `SYNCMSG cnt=0`, terminal events, Matrix failures and cleanup timeouts need controlled acceptance. Receive-header timeout and push receipts are not installed by default. |
| 4 | Reproducible deployment acceptance and Matrix crypto recovery | Docker/configuration exist. Complete standard appservice installation and separate Beeper validation; test key rotation, missing keys/trust transitions and encrypted replies/reactions. |
| 5 | Broader outbound messaging and interactive content | `HandleMatrixMessage` accepts text/notice/emote and JPEG/PNG photos only; image replies are rejected. Outbound video/audio/files/albums/stickers/cards/polls/posts, edits/deletions and poll/board actions have no supported connector path. |
| 6 | Eligible unsupported formats and broader chat/contact scope | Link, Schedule, Nudge, SharpSearch, sticker variants, LargeVideo/LargeFile, call/business formats and Open/Team/Secret Chat need independent contracts/fixtures. Friend creation has a bounded API; full friend/contact synchronization remains open. |

Keep opt-in historical backfill pending its read-side-effect policy. Continue
Mac-first full-chain protocol research and production-fixture migration alongside
these slices. Cloud backup/restore remains out of scope; paid or unavailable
emitters are evidence blockers, not reasons to relabel supported messages.

## Code structure simplification (2026-10-10)

A structural audit of the Go code at `3995d0c` found: about 1,350 production
lines in `sessionlogin` and ~550 in `client/session.go` that no binary
reaches (`deadcode -tags goolm ./cmd/...` reports 61 functions), the BSON
shadow decoder running on every packet by default, four copies of the Mac
HTTP client, six sets of BSON field helpers, eleven hand-rolled strict JSON
loops, a 25-field hand-written connection lifecycle in the connector, and 29
"no bridge DB" branches that exist only for the unit-test harness. The
task-by-task refactor plan, with acceptance criteria and merge order, is in
[`docs/code-structure-plan.md`](docs/code-structure-plan.md). Its task A1
resolves the "Connect or delete the remaining test-only code" item below.

## Regular-group completion objective (2026-10-09)

Complete each slice through a separate reviewable PR. Every slice requires the
Mac request/response/caller/persistence/consumer/failure contract (with gaps
recorded), production-path regression fixtures, owned A/B/C acceptance in an
encrypted Matrix room, relevant offline/restart checks, sanitized evidence,
`make check`, secret scanning, and passing required CI before squash merge.
Existing direct-room acceptance does not satisfy a group acceptance gate.

- [x] Native group discovery and creation lifecycle (PR #270): online/offline inventory,
      correct name and full roster, no duplicate portal; determine whether the
      source requires a first message before a group exists.
- [x] Explicit Matrix group creation (PR #271) with selected participants and safe handling
      of invalid participants and ambiguous creation/invitation failures.
- [x] Membership and access lifecycle (PR #272), including offline changes and bridge-user
      removal; prevent forwarding to removed members.
- [x] Shared/personal group names and group avatar replacement/clearing (PR #273), with
      convergence and clear rejection of unsupported outbound changes.
- [x] Member profile refresh with stable ghosts and isolated profile failures (PR #274).
- [x] Bounded opt-in historical backfill (PR #275) with durable progress, source visibility,
      ordering/deduplication, and observed read/unread policy.
- [x] Failure recovery and ambiguous sends (PR #276): delivery pause and bounded
      replay, Matrix refusal and multipart commit checks, stable live/history
      transaction IDs, first-delivery replay floor, catch-up retry during a
      continuing outage, one Kakao send per Matrix event, explicit unconfirmed
      outbound status, and the portal-lock deadlock fix. Owned acceptance and
      recorded gaps: [failure recovery](research/bridge/GROUP-FAILURE-RECOVERY.md).
- [ ] Failure-recovery follow-ups: owned kickout/change-server acceptance,
      owned multipart partial success, a reproduced applied-but-unacknowledged
      outbound send, linking a later source echo of an unconfirmed send to its
      Matrix event, and the remaining Mac recovery trace gaps.
- [x] Group JPEG/PNG photos (PR #277): exact bytes both directions, inbound and
      outbound captions (`cmt`), unavailable/expired downloads as committed
      notices, millisecond expiry, `image/jpeg` MIME, no framework error notices
      on transient failure, fetch-before-reservation for outbound images,
      cancellable inbound/outbound transfers on Disconnect, owned offline
      catch-up, restart and media-fault acceptance:
      [group photos](research/bridge/GROUP-PHOTOS.md).
- [ ] Group-photo follow-ups: live expired/CDN-refused download, observed video and
      album expiry units, the Mac download/upload strategy selection gaps, and
      resumable uploads if a manual-retry surface is added.
- [x] Group text replies (PR #278): both directions, offline catch-up and history
      import; a Kakao reply to an unbridged source quotes the embedded source
      preview instead of losing its context; a Matrix reply to an unmappable
      target is refused before mutation with a clear status:
      [group replies](research/bridge/GROUP-REPLIES.md).
- [ ] Group-reply follow-ups: author line in the missing-source quote, an owned
      inbound reply to an unbridged source, owned history import of replies, and
      the Mac thread-versus-reply selection in regular groups.
- [x] Image replies and reply attachments (PR #279): Kakao sticker replies become
      Matrix sticker replies, unsupported reply attachments become explicit
      notices, a Matrix reply to a photo quotes "Photo", and a Matrix image sent
      as a reply is refused before mutation with a clear status:
      [image replies](research/bridge/GROUP-IMAGE-REPLIES.md).
- [ ] Image-reply follow-ups: album ("%d photos") and video source previews,
      sound stickers and attach types 22/25, and live album/video reply sources.
- [x] Per-member reactions (PR #280): offline changes recovered through the
      Mac's `sync-meta` resync with per-chat cursors, Android quick reactions
      shown as emoji, and a Matrix reaction no longer redacts the same
      account's mini reactions: [group reactions](research/bridge/GROUP-REACTIONS.md).
- [ ] Reaction follow-ups: outbound mini (quick-reaction) mutations, the Mac
      `/rx/logmetas` batch path, double-puppet attribution for the bridge
      account's phone, offline legacy changes from another device, and long
      resync backlogs.
- [x] Group read receipts (PR #281): forward-only per-member receipts, offline
      reads recovered through `CHATONROOM` watermarks, and an owned differential
      showing catch-up's `cnt=0` and `CHATONROOM` produced no visible read
      acknowledgement: [group read receipts](research/bridge/GROUP-READ-RECEIPTS.md).
- [ ] Read-receipt follow-ups: the one unexplained own-watermark advance, room
      tokens for delta snapshots, double-puppet attribution, and large rooms.
- [x] More inbound group formats (PR #282): albums, video, audio, files, contacts,
      profiles, locations, stickers, polls and Boards posts accepted in the
      encrypted group (offline catch-up for location, poll and sticker), an
      observed fixture for every claimed format, and no silent drop path:
      [group formats](research/bridge/GROUP-FORMATS.md).
- [ ] Format follow-ups: a live unsupported-type message, Mini text,
      LargeVideo/LargeFile and resource-only albums in groups.
- [x] Rich announcements (PR #283): IMAGE/POLL/VIDEO/FILE announcements summarize
      to the topic in the Mac banner order, replacement/clear/reconnect accepted
      live, content with nothing to show keeps the topic, and rich Boards posts
      no longer become malformed notices:
      [group announcements](research/bridge/GROUP-ANNOUNCEMENTS.md).
- [ ] Announcement follow-ups: mentions and SCHEDULE D-day text, live VIDEO/FILE/
      QUIZ/SCHEDULE announcements, multiple polls per post.
- [x] Explicit outbound announcements (this PR): the Boards write contract is
      untraced, so Matrix topic set/replace/clear is rejected once before any
      source request, with a notice, and the bot restores the announcement
      topic; accepted live with restart:
      [outbound announcements](research/bridge/GROUP-OUTBOUND-ANNOUNCEMENTS.md).
- [ ] Boards write follow-ups: trace the Swift Boards write chain (request,
      response, permissions, failures) before sending any announcement or post
      mutation; restore Matrix name/avatar after their rejection; encrypt the
      bot's status notices in encrypted rooms.
- [x] Outbound files, video, audio and albums (this PR): Mac-traced SHIP/POST
      and MSHIP/MPOST/WRITE paths, extension classification and deny list,
      pre-send limits, one reserved send per Matrix event, native A opened
      each result (byte-identical except server-re-encoded video), and a
      catch-up album duplicate fixed:
      [outbound media](research/bridge/GROUP-OUTBOUND-MEDIA.md).
- [ ] Outbound media follow-ups: streaming uploads for the Mac's 300 MiB
      limit, large media, server deny-list refresh, GIF/WebP album photos,
      voice messages, live upload faults for files and albums.
- [x] Edits and deletions (this PR): SYNCMODMSG/SYNCDLMSG and feed-25/14
      catch-up become Matrix edits and redactions with author and revision
      guards; deleted logs never reveal content; offline edits read with
      GETMSGS and reached through LOGINLIST `ll`; own Matrix edits and
      redactions send one MODIFYMSG/DELETEMSG within Kakao's limits; three
      live regressions fixed:
      [edits and deletions](research/bridge/GROUP-EDITS-DELETIONS.md).
- [ ] Edit/delete follow-ups: outbound reply and emoticon edits, the
      account delete-time setting, live provocation of −210/−211/−212 and the
      24-hour limits, media edit/delete coverage, hidden trailing feeds.
- [x] Group settings and permissions (this PR): server `p` maps to the Matrix
      user's mute at portal creation; Matrix mute stays local (the Mac sends
      no request); no roles in regular groups, so member power changes are
      rejected and restored; Matrix kicks, bans and invites of KakaoTalk
      members are rejected and undone:
      [settings and permissions](research/bridge/GROUP-SETTINGS-PERMISSIONS.md).
- [x] Matrix invites (this PR): a Matrix invite of a KakaoTalk user sends
      one reserved ADDMEM in a plain regular group; −402/−405 refusals name
      the blocked-friends list and revoke the invite; a fresh source roster
      decides the Matrix membership; accepted live with restart:
      [settings and permissions](research/bridge/GROUP-SETTINGS-PERMISSIONS.md).
- [ ] Settings follow-ups: favorites via SETMCMETA `favorite`, team-chat
      roles, the server notification flag, live mute verification with a
      double puppet, and a live refused invite.

The regular-group slices through settings and permissions shipped in PRs
#270–287; remaining work is the follow-ups above.

Cloud backup/restore and Secret Chat remain excluded. Do not claim format,
room-type or scale parity beyond supporting evidence.

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
- [x] Implement transport, framing, and serialization packages.
- [x] Implement credential/session storage interfaces with secure defaults.
- [x] Implement device login and bridge-owned bounded reconnect state machines.
- [x] Add synchronization and text receive/send with separate delivery, commit
      and explicit read watermarks. Live catch-up read-side-effect policy remains open.
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
- [x] Add a private versioned continuity checkpoint and make the long-running
      Matrix/Beeper bridge the exclusive per-profile session owner. On bridge
      restart, perform one cursor-based resumed login rather than QR
      authorization or a retry loop.
- [x] Implement the private versioned checkpoint, explicit application commit
      boundary, resumed `LOGINLIST` cursor inputs, duplicate suppression, and
      bounded no-progress-detecting `SYNCMSG` recovery with synthetic tests.
- [x] Live-validate the normal restart resume/catch-up boundary and wire the bridge as
      the sole checkpoint committer and per-profile session owner.
- [x] Verify the implemented messaging/restart slices against disposable accounts
      and add live-found regressions; broader lifecycle acceptance remains open.

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
- [x] Move fixtures to `research/fixtures/` with `provenance`, enforced by
      `internal/testpolicy`.
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

`Client.Shutdown(ctx)` now interrupts and joins outside `Client.mu`, retaining
checkpoint/profile ownership on deadline expiry for a later cleanup attempt.
Production bridge shutdown uses the bounded owner path; client shutdown and
connector blocked-delivery regressions cover retention and eventual release.
The constructor status/config proposal above remains unimplemented: existing
Session timeout seams and standalone owners do not establish default binding.

## Phase 4 — Matrix/Beeper bridge

Execution follows [`research/bridge/PLAN.md`](research/bridge/PLAN.md).

- [x] Reassess current mautrix-go and Beeper bridge conventions; adopt
      `bridgev2` ([ADR 0003](docs/adr/0003-bridge-on-mautrix-bridgev2.md)).
- [x] Implement the bridgev2 skeleton, profile-import login, identifier mapping,
      bidirectional text, inbound replies, and commit-after-Matrix handling.
- [x] Live-validate the minimal bridge and restart catch-up against a disposable
      Synapse with owned disposable accounts.
- [x] Live-validate fresh bridge-native QR enrollment, persistent device approval,
      credential persistence, and one clean restart on an owned account
      (2026-10-07); see `research/bridge/QR-ENROLLMENT.md` for scope and gaps.
- [ ] Complete the single-user alpha in bridge-plan execution order: lifecycle
      reliability, room/member metadata, QR enrollment, replies/photos/reactions,
      controlled reconnect, and deployment validation.
- [x] Implement bidirectional read-receipt routing with production regressions.
- [ ] Live-validate read receipts and catch-up read effects; add opt-in backfill
      only after its policy is settled.
- [ ] Validate standard Matrix appservice deployment and Beeper compatibility.
- [x] Package Docker/configuration for a single-user Linux homelab without
      operator-specific values; full deployment acceptance remains open.

## Questions to resolve through evidence

- Which official clients are treated as secondary devices, and what are their
  concurrent-device and approval rules?
- Does the macOS client share protocol behavior with Windows or tablet clients?
- Where are device credentials generated and stored, and how are they revoked?
- Which parts of transport and payloads are encrypted independently of TLS?
- What server-visible properties distinguish official secondary devices?

## E2E tooling maintenance

Use the shared emulator launcher with private configuration for owned A/B
preparation and follow
[the E2E maintenance contract](research/e2e-tooling.md) whenever startup, login,
or navigation encounters a new edge case. Add a sanitized regression and improve
the shared helper before resuming the experiment.

### Encrypted direct-room acceptance (2026-10-07)

Owned A/B text and photos passed both directions in an encrypted Matrix portal,
including exact decrypted inbound PNG bytes and offline catch-up once after a
normal bridge restart with retained keys. See
[`ENCRYPTED-ROOM-VALIDATION.md`](research/bridge/ENCRYPTED-ROOM-VALIDATION.md).
The reusable `cmd/mooo-matrix-lab` companion records the harness parsing/login
edge cases and prevents automatic repeat sends. Remaining acceptance includes
key rotation/missing-key recovery/trust transitions, encrypted replies and
reactions, Beeper deployment, and group media/replies/reactions. Regular three-person encrypted text and
restart passed on 2026-10-09; see
[group validation](research/bridge/GROUP-MESSAGING-VALIDATION.md).

### Inbound sticker acceptance (2026-10-08)

Types 12/20 now map to native Matrix stickers with bounded fixed-origin resource
fetching and the independently traced resource transform. Owned static PNG and
animated WebP passed encrypted attachment verification and restart uniqueness;
synthetic GIF and official executed codec vectors exercise production code.
See [research/stickers.md](research/stickers.md). Sound/composition, outbound
stickers, paid-pack behavior and Matrix client animation playback remain gaps.

### MultiPhoto acceptance and LargeVideo/LargeFile gaps (2026-10-08)

Inbound URL-based MultiPhoto (27) passed owned emulator emission, encrypted Matrix
exact media bytes, two-part ordering, offline catch-up, live delivery and restart
uniqueness. Missing-part upserts prevent partial delivery from being mistaken for
a complete duplicate; shared send-album tooling has guarded durable receipts.
See [research/multi-large-media.md](research/multi-large-media.md). LargeVideo (28)
needs an eligible owned sender/resource fixture: the current video attempt emitted
3 and the account UI offered subscriptions. LargeFile (29) is skipped when not
live-testable, as the maintainer permits; the normal Android file policy does not
select LARGE. Do not retag another type and call it E2E coverage. Resource-only
albums, full official storage parity and outbound albums remain gaps.


### Owned-emulator message-type rollout (2026-10-08)

Implement types that can be independently emitted and verified end to end. Land
one type per PR, or two closely related types when sharing a coherent contract.
For each: trace official models/callers/persistence/consumers/failures, document
untraced layers, capture a controlled owned-account fixture, add production-path
regressions, verify native encrypted Matrix delivery/media and restart behavior,
run required checks and secret scans, then merge through passing CI.

- [x] Ordinary Video (3): controlled owned A/B encrypted delivery, exact received
      bytes/metadata, native receiver playback and restart verified; see
      [video.md](research/video.md).
- [x] Audio (5) and File (18): independently verified owned A/B encrypted
      catch-up/live delivery, media metadata and exact bytes, retained keys and
      restart uniqueness; landed separately in PRs #248 and #247.
- [x] Profile (17): observed owned A/B readable encrypted identity/status
      rendering, shared picker guards and restart acceptance; PR #250. Avatar
      fetching and Kakao profile actions remain gaps.
- [x] Contact (4): original vCard bytes and metadata preserved through encrypted
      catch-up/live delivery, native receiver Details, restart and later text;
      landed in PR #251.
- [x] Location (16): synthetic emulator GPS, native receiver map, exact received
      coordinates through encrypted catch-up/live delivery and restart verified.
- [ ] Link (9): ordinary Android URL sharing was observed to emit Text (1),
      not Link (9). The static receiver has multiple KakaoLink format branches;
      a controlled type-9 emitter and full-chain fixture remain pending. See
      [the emitter/receiver audit](research/link.md).
- [x] Vote (14): observed text-poll creation snapshots preserve titles and
      ordered options through encrypted catch-up/live delivery and normal
      restart. Zero legacy vote-ID placeholders have a live regression fixture;
      interactive voting, results and lifecycle changes remain gaps. See
      [vote.md](research/vote.md).
- [x] Post (24): observed text-only board posts preserve structured source text
      through encrypted catch-up/live delivery, native details and normal
      restart. Rich/multimedia forms and board interaction remain gaps. See
      [post.md](research/post.md).
- [ ] Schedule (13), Nudge (21), and sticker variants (6/22/25):
      determine which controlled A/B UI flows are available without purchases.
      The owned sender's Calendar creation was rejected by user protection
      policy; no retry or Schedule fixture was produced.
      Nudge's ordinary Android view selector uses an unsupported bubble; no
      controlled emitter has been established. Free Classic and Mini sends
      produced type 12 and type 1 with `emojis`, respectively, rather than
      fixtures for 6/22/25. See [nudge.md](research/nudge.md) and the
      [free-pack audit](research/stickers.md). Mini resource/placement rendering
      is now implemented for observed nonanimated resources as ordered encrypted
      text/image parts; animated resources and inline layout remain gaps. See
      [mini-emoticons.md](research/mini-emoticons.md).
- [ ] Audit remaining types against actual emitter/receiver availability;
      document evidence and blockers rather than claiming enum-only support.

LargeFile (29) may be skipped when not live-testable per maintainer. LargeVideo
(28) still needs an eligible owned sender fixture.

After the independently testable types land:

- [x] Audit and implement operator-facing chat listing, including production
      callers, pagination/state reconciliation and owned-account E2E checks (PR #261).
- [x] Audit and implement room-scoped contact profile-photo retrieval, using the traced
      profile/resource path, fresh lookup without URL caching, bounded downloads
      and owned-account E2E checks (PR #262). Full cache/consumer parity remains open.
- [x] Create a third disposable owned lab account C and begin multi-participant
      chat testing, after the listing/profile-photo phase.

Keep any
phone verification and account state private. Purchases, primary-account tests,
backup/restore and entitlement bypass remain outside this rollout. Existing
owned A/B research authorization remains in force.


### SharpSearch availability checkpoint (2026-10-09)

The owned Android sender exposes neither a search entry in the inspected
attachment picker nor search controls after normal `#weather` composer input.
The draft was discarded without sending. The official SharpSearch predicate
reads the `available2` preference; its account value and update lifecycle remain
untraced. Existing-log forwarding/results consumers do not prove an available
original emitter. See [sharp-search.md](research/sharp-search.md).

Keep type 23 unsupported pending a controlled eligible share. Remaining paid,
business, call and Open Chat formats still require their own owned emitters and
fixtures; enum names do not establish E2E support. Operator chat listing and
contact-photo retrieval have landed. Account C and initial regular-group text
testing completed afterward; group lifecycle and broader media acceptance
remain open.


Operator listing audit: `Client.InitialChatData` is the current login's raw
`LOGINLIST`/`LCHATLIST` deltas, not the complete resumed inventory. Continuity
persists `KnownChats` and applies explicit removals; its reopen/delta regression
protects retention when a later login returns no chat data. The operator path
must reconcile that inventory and resolve typed room metadata, including rooms
without last messages, and expose incomplete list synchronization honestly.
The new operator listing requests a complete zero-token login inventory while
preserving committed message positions, and reduces each page in deletion-before-
update order. See [operator-chat-list.md](research/operator-chat-list.md) for
validation and remaining gates. Room-scoped contact-photo retrieval landed in PR #262; its observed absence
and synthetic-avatar acceptance is separate from connector avatar support.

Contact-photo implementation checkpoint: the room-scoped operator command and
production API retrieve the current thumbnail from a single matching MEMBER
profile. Owned acceptance covers absent default avatar and a synthetic uploaded
avatar corroborated by the peer's official Android profile view. See
[operator-contact-photo.md](research/operator-contact-photo.md) for exact scope,
resource safety and official-chain gaps. PR #262 is merged; broader profile
images, cache lifecycle and full official-chain parity remain open.

- [x] Bridge regular-group TEXT Boards announcements to Matrix room topics, including live replacement/removal and reconnect snapshots. Rich announcements and outbound Boards editing remain separate work.
      Mac request/response, revision merge, database/UI state, removal and
      failure behavior. Owned native Announce showed all three clients; Matrix
      pin/topic projection is absent. Keep this separate from shared
      `CHGMETA` Notice type 1; see research/chat-metadata.md and its fixture.

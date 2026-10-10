# Code structure simplification plan

Baseline: `main` at `3995d0c` (PR #287), audited 2026-10-10. Line numbers
below refer to that commit and will drift; re-locate by function name.

This plan covers production Go code under `internal/` and `cmd/` only. It does
not touch `research/`, `tools/`, fixtures, or protocol behaviour. Every task is
intended to be behaviour-preserving unless its acceptance section says
otherwise.

## 1. Ground rules for every task

Read these before starting any task. They apply to all of them.

1. **One task, one branch, one PR.** Branch from current `main`, name it after
   the task ID (for example `refactor/A1-delete-unbound-seams`). Do not bundle
   tasks. Do not commit to `main`.
2. **Behaviour-preserving.** The bridge and lab CLI must do exactly what they
   did before. If you find a bug while refactoring, do not fix it silently:
   write a failing regression test first, fix it in a separate commit in the
   same PR, and call it out in the PR description. Only tasks whose
   acceptance says "behaviour change" may change behaviour.
3. **Validation before opening the PR**, run from the repository root:
   ```bash
   make check
   ```
   This runs race tests, vet, gofmt, golangci-lint and govulncheck with the
   `goolm` build tag. For ad-hoc Go commands use
   `GOFLAGS='-tags=goolm' go test ./internal/client/...` etc. Never change
   user-wide Go defaults.
4. **Dead-code check** after any deletion or move:
   ```bash
   GOFLAGS='-tags=goolm' go run golang.org/x/tools/cmd/deadcode@latest -tags goolm ./cmd/...
   ```
   The baseline reports 61 unreachable functions (section 2). A task that
   touches a package must not increase that package's count.
5. **No test-only production code.** `internal/testpolicy` rejects test files
   that reference no production symbol, and `AGENTS.md` forbids adding
   production code that only tests call. A task that moves code must not
   create either.
6. **Keep the layering.** `cmd` → `internal/bridge/connector` →
   `internal/client` → `internal/protocol/*`. Protocol packages must not
   import `client` or `connector`. Verify with
   `GOFLAGS='-tags=goolm' go list -f '{{.ImportPath}} {{.Imports}}' ./internal/...`.
7. **Secrets.** Run the gitleaks pre-merge check as CI does. Never add real
   identifiers, tokens or captures to fixtures. Synthetic values only.
8. **Commits.** Small, reviewable commits with the attribution trailer the
   session reminder specifies. Squash-merge once CI is green and the PR is
   mergeable; agents are authorised to merge their own PRs.
9. **PR description template.** State: task ID, what moved/was deleted, LOC
   delta (`git diff --stat main`), the `deadcode` count before and after for
   the touched packages, and any behaviour change with its regression test.
10. **Line-number drift.** Where this plan says `file.go:NNN`, open the file and
    locate the named function; do not trust the number blindly.

## 2. Baseline measurements

Non-test / test LOC at `3995d0c`:

| Package | Prod LOC | Test LOC | Notes |
|---|---:|---:|---|
| internal/bridge/connector | 8,244 | 13,476 | `client.go` 1,915 lines |
| internal/client | 4,611 | 10,160 | `session.go` 1,720 lines |
| internal/protocol/events | 2,029 | 1,515 | `events.go` 1,082 lines |
| internal/protocol/media | 1,937 | 1,353 | |
| internal/protocol/sessionlogin | 1,767 | 2,961 | 32 files; ~1,350 prod LOC unreachable |
| internal/protocol/registration | 1,322 | 873 | |
| internal/protocol/chatmeta | 1,190 | 1,021 | |
| internal/continuity | 639 | 543 | |
| internal/authstate | 553 | 287 | |
| cmd/mooo-matrix-lab | 527 | 176 | imports no mooo package |

`deadcode -tags goolm ./cmd/...` at baseline: 61 unreachable functions
(sessionlogin 46, client 9, tokenrefresh 2, registration 2, loco 1,
connector 1). `make test` takes about 30 s.

Import graph is acyclic and layered correctly; no task changes that.

## 3. Workstreams and tasks

Tasks are grouped by workstream. Each has: goal, why, files, steps,
acceptance, dependencies and a rough size (S = under 200 changed lines,
M = 200–800, L = over 800). Section 4 gives the recommended order.

---

### Workstream A: remove code that production never binds

#### A1. Delete the unbound Session seams and the sessionlogin owners (L)

**Goal.** `internal/client.Session` keeps only what production installs: one
mutex, the pending-request map, the wire, the push channel, the read loop,
the ping timer, Close/Shutdown. The sessionlogin package keeps only what
production calls.

**Why.** `deadcode` reports 46 of sessionlogin's functions and 9 of client's
as unreachable from any binary. Production never calls
`Session.BindPushReceipt`, `Session.BindInSegmentTimeout`,
`Session.installOutSegmentSubmitter`, nor sets `receiveHeaderTimeout`,
`receiveHeaderTimeoutEnable`, `headerObserver`. `PLAN.md` ("Parity test
migration" and "Current feature gaps", row 3) already records these as
"connect or delete; needs a product decision". `AGENTS.md` forbids production
code that only tests call. The seams add ~550 LOC to `session.go`, three
extra interfaces, one extra mutex, and a nil-check on every request.

**Decision gate.** The maintainer chooses one of:

- (a) **Delete** (recommended). Git history and `research/reconnect/*.md`
  retain the reviewed contracts. When the status/config owner binding in
  `PLAN.md` ("Default status/config owner binding proposal") is actually
  implemented, the owners are re-added together with the code that binds
  them, which is what the rule in `AGENTS.md` requires anyway.
- (b) **Quarantine.** Move the same files unchanged to
  `research/reconnect/go-owners/` (not compiled; no `go.mod`) with their
  tests, so `go build ./...` ignores them.

The executing agent must state in the PR description which option the
maintainer selected. If no answer is available, implement (a) and say so.

**Files.**

- Delete entirely from `internal/protocol/sessionlogin/`:
  `push_receipt_owner.go`, `push_receipt_agent_owner.go`,
  `receive_header_timeout_owner.go`, `receive_header_timeout_effects.go`,
  `out_segment_timeout_owner.go`, `out_segment_submit.go`,
  `receipt_body.go`, `receipt_packet.go`, `receipt_projection.go`,
  `receipt_json_mapping.go`, `sgjson_object.go`, `foundation_ti.go`, and
  their `_test.go` files.
- In `sessionlogin/sessionlogin.go` delete the recovery reducer and endpoint
  cache: `RecoveryState`, `NewRecoveryState`, `ReduceRecovery`,
  `RecoveryState.Apply`, `recoveryGeneration`, `advanceRecoveryGeneration`,
  `Endpoint.valid`, `EndpointCache`, `EndpointCache.Eligible`,
  `CacheEligible`, `InvalidateMatchingFailure`, and their tests.
- Delete `internal/client/out_segment_worker.go` and
  `out_segment_worker_test.go`, `out_segment_error_order_test.go`.
- In `internal/client/session.go` delete: the `lifecycleShutdown`,
  `receiveHeaderTimeoutController`, `InSegmentTimeoutController`,
  `PushReceiptSender`, `pushReceiptCloser`, `pushReceiptWaiter` interfaces;
  `EligiblePushReceiptPacket`; the whole `pushReceiptBinding` type and
  methods; `receiveHeaderTimeoutToken`; the methods
  `prepareReceiveHeaderTimeout`, `commitReceiveHeaderTimeout`,
  `abortReceiveHeaderTimeout`, `resetReceiveHeaderTimeout`,
  `disarmReceiveHeaderTimeout`, `observeHeader`,
  `closeReceiveHeaderTimeout`, `BindInSegmentTimeout`,
  `closeInSegmentTimeout`, `BindPushReceipt`, `dispatchPushReceipt`,
  `closePushReceipt`, `bodyProgressCallbacks`, `readBodyWithProgress`; the
  Session fields `receiveHeaderTimeoutMu`, `outSegmentSubmitter`,
  `headerObserver`, `receiveHeaderTimeout`, `inSegmentTimeout`,
  `pushReceipt`, `receiptDone`, `receiveHeaderTimeoutEnable`,
  `receiveHeaderTimeoutState`; the `pingSessionOptions` fields
  `inSegmentTimeoutOwner` and `beforeBootstrapRequest` if they become unused.
- Simplify the write path: `writeRequestWithResult` loses its `onResult`
  parameter; `writeRawPayloadProgress` collapses into `writeRawPayload`
  (drop `progress`). `wireConn.readWithHeaderObserverAndProgress` and
  `readWithHeaderObserver` collapse into `read` (keep the BSON shadow call;
  task B1 changes how it is installed).
- `Session.Shutdown` no longer joins a submitter or receipt worker; it
  becomes `Close` plus joining the read loop.
- The teardown sequence copied three times in `finishRead`, `Close` and
  `Shutdown` (`stopLifecycle; closeReceiveHeaderTimeout; closeInSegmentTimeout;
  closePushReceipt; resetReceiveHeaderTimeout; submitter.Close`) shrinks to
  `stopLifecycle` only.
- Fix the stale comment at `session.go:78-79` that references
  `sessionlogin.NewInSegmentTimeoutOwner`, which does not exist.
- Tests to delete alongside (they only exercise removed code; list them by
  grepping for the removed identifiers): `session_push_receipt_test.go`,
  `session_receive_header_timeout*_test.go`, `session_in_segment*_test.go`,
  and the sessionlogin tests of the deleted files. Tests that mix removed and
  retained behaviour must be trimmed, not deleted.

**Steps.**

1. Run `deadcode` and save the output as the "before" list.
2. Delete the sessionlogin files and the reducer/cache block. Build. Fix
   compile errors only by deleting more unreachable code, never by adding.
3. Delete `out_segment_worker.go` and its tests. Build.
4. Remove the Session seams listed above, top to bottom. After each
   removal, `go build ./...` and `go vet ./...`.
5. Collapse the write and read helper ladders.
6. Delete or trim the tests. Run the client and connector test packages.
7. Update `PLAN.md`: in "Parity test migration", mark the "Connect or delete"
   bullet done for push receipts, receive-header timeout and connection
   recovery, and note the chosen option. In "Current feature gaps" row 3,
   replace "Receive-header timeout and push receipts are not installed by
   default" with "not implemented; see docs/code-structure-plan.md A1".
8. Run `make check`.

**Acceptance.**

- `deadcode` reports zero unreachable functions in
  `internal/protocol/sessionlogin` and `internal/client` except
  `OpenWithTestDialers`/`testSessionDialers` (addressed in D8) and
  `BSONShadowStats` (B1).
- `sessionlogin` contains at most four files: the LOGINLIST request and BSON
  marshalling, `ClassifyLoginStatus`, `FormatPacketUniqueID`, plus tests.
- `session.go` is under 1,000 lines. `Session` has exactly one mutex
  (`mu`) plus `lifecycleMu`.
- All remaining tests pass with `-race`. No test file was rewritten to
  model removed behaviour.

**Depends on.** Nothing. Do this first; A1 makes D1–D3, D7 and D8 much
smaller.

#### A2. Prune the registration package's vestigial surface (S)

**Goal.** `internal/protocol/registration` describes what it does and exports
only what the connector and command use.

**Why.** The package doc says it "contains no transport, clock, storage",
but it owns `HTTPExecutor`, `NewHTTPRequest`, `ApplyMacClientHeaders` and
`QRRegistrationService`. Several exported identifiers have no production
consumer.

**Files.** `internal/protocol/registration/{registration.go, policy.go,
http_profile.go, http_executor.go, form.go, json.go, qr_service.go}`.

**Steps.**

1. For each exported identifier in the package, run
   `grep -rn 'registration\.<Name>' internal cmd --include='*.go' | grep -v _test.go | grep -v internal/protocol/registration/`.
   Delete every identifier with zero production hits outside the package
   **and** no use inside the package's own production code. Known
   candidates: `RoutePasscode*` (policy.go), `FieldMask`, `FormStructure`,
   `DeviceShape`, `ProfileFor` (http_profile.go; keep only if
   `http_request.go` still needs the `form.Profile != expected` comparison,
   in which case inline a direct struct comparison), the `HTTPDoer` alias,
   the `RegistrationFormContentType` alias, `DecodeQROutcome64`, and
   `Outcome` constants other than `OutcomePending` and
   `OutcomeUnregisteredDevice` (check `connector/login.go` for the exact set
   read).
   `QRGenerateResponse.String/GoString` are on the deadcode list; keep them
   only if they redact secrets from `%v` output (read the body; if they
   exist to prevent token leakage in logs, keep and add a comment saying so).
2. Replace `OptionalString`, `OptionalBool`, `OptionalInt64`, `RawJSONField`
   (json.go) with pointer fields (`*string`, `*bool`, `*int64`,
   `json.RawMessage`) if and only if the existing tests still pass with the
   same fixtures; otherwise leave them and note why in the PR.
3. Rewrite the package doc comment to describe the current contents: QR
   registration state, HTTP request/response models, Mac header policy.
4. Delete tests that only covered removed identifiers.

**Acceptance.** `go doc ./internal/protocol/registration` lists no
identifier without a production caller. Package doc is accurate. Tests pass.

**Depends on.** Nothing.

#### A3. Small deadcode leftovers (S)

**Files and actions.**

- `internal/bridge/connector/avatar.go`: `validateAvatarURL`,
  `maxAvatarBytes`, `errAvatarDownload` are referenced only from
  `avatar_test.go`. Delete them and the tests that use them; keep
  `avatarFromURL` and its tests. If `avatar.go` then only re-exports
  `media` functions, fold it into the caller.
- `internal/protocol/loco/secure.go`: `NewSecureV3WithKey` and
  `SecureV3.KeyForTesting` are test-only. Move them to
  `internal/protocol/loco/export_test.go` (package `loco`), which makes them
  available to `loco` tests. `internal/testsupport/loco` and connector tests
  that need a keyed SecureV3 must instead construct it through a function
  that lives in `internal/testsupport/loco` and is itself only imported by
  tests. If that is impossible without duplicating crypto code, keep
  `NewSecureV3WithKey` and document the exception in the PR.
- `internal/protocol/tokenrefresh/tokenrefresh.go`: `Rotation.String` and
  `Rotation.GoString`. Same rule as `QRGenerateResponse` in A2: keep if they
  redact secrets, otherwise delete.
- `internal/protocol/sessionlogin/foundation_ti.go:coerceFoundationTi` is
  covered by A1.
- `internal/testpolicy`: the model-only allowlist is empty and documented as
  shrink-only. Delete `ReadAllowlist`, the `testdata/model-only-allowlist.txt`
  file, and the code path that consults it.

**Acceptance.** `deadcode` count for connector, loco, tokenrefresh is zero
(or each remaining entry is justified in the PR as a secret-redacting
`String`). `make check` passes.

**Depends on.** A1 (so the deadcode list is short enough to read).

---

### Workstream B: research tooling off the production hot path

#### B1. Make the BSON shadow decoder an explicit, injected observer (M)

**Goal.** The bridge does not decode every packet twice unless configured
to; `internal/client` does not import `testing`; the comparison code lives
outside `internal/protocol`.

**Why.** `wireConn.read*` runs `shadowDecode` on every incoming BSON body up
to 1 MiB (`internal/client/bson_shadow.go`). The default mode outside test
binaries is `Log`, so every bridge user pays a second full decode per
packet. `bson_shadow.go` imports `"testing"` in a non-test file to pick the
default. `loco/bounded_bson.go` says it is "deliberately not wired into
Packet or Session" and exists only for `bsonshadow.Compare`.

**Files.** `internal/client/bson_shadow.go`, `internal/client/session.go`
(`wireConn.read*`), `internal/client/client.go` (`OpenOptions`),
`internal/protocol/loco/bounded_bson.go`, `internal/protocol/bsonshadow/`,
`internal/bridge/connector/connector.go` (`SetBSONShadow` call),
`cmd/mooo-lab/main.go`.

**Steps.**

1. Create `internal/shadow/` (name is free; must not be under
   `internal/protocol`). Move `loco/bounded_bson.go` there as
   `bounded_bson.go` (rename `DecodeObservedBSON` callers accordingly) and
   move `bsonshadow/compare.go` there. Move the report/dump/stat code from
   `client/bson_shadow.go` there too. `loco` must no longer contain any
   decoder that production does not use; verify with `deadcode`.
2. Replace the process-global `atomic.Pointer` config with an injected
   observer: add `PacketObserver func(loco.Packet)` to `client.OpenOptions`
   (and to `OpenWithTestDialers`'s options). `wireConn` stores it; `read`
   calls it when non-nil. Default nil means zero cost.
3. `internal/shadow` exposes `func NewObserver(cfg Config) func(loco.Packet)`
   with the same `Mode`, `DumpDir`, `MaxBodyBytes`, `Report` semantics.
   `Mode` default is `Off`. There is no `testing.Testing()` call anywhere;
   test packages that want `Panic` mode construct the observer in their own
   `TestMain` or helper.
4. `connector.go` builds the observer from the bridge config and passes it
   through `OpenWithOptions`. Default remains Off unless the config sets a
   mode. `cmd/mooo-lab` does the same from its environment variable.
5. Delete `client.SetBSONShadow`, `BSONShadowStats`, `ParseBSONShadowMode`
   from `client` (move `ParseBSONShadowMode` to `internal/shadow`).
6. Move the `bson_shadow_test.go` tests with the code; keep the tests that
   assert "the production packet is never altered by the shadow" by driving
   `connectSession` with an observer that records calls.

**Acceptance.** Behaviour change, deliberate: the bridge's default shadow
mode becomes Off. State this in the PR and in `PLAN.md` under "E2E tooling
maintenance". `internal/client` imports neither `testing` nor
`internal/shadow`. `internal/protocol/loco` contains only framing and
SecureV3. Lab runs with `MOOO_BSON_SHADOW=log` still produce the same
warnings.

**Depends on.** A1 (the wire read ladder is collapsed there).

#### B2. Move `cmd/mooo-matrix-lab` to `tools/` (S)

**Goal.** `go build ./cmd/...` builds only product binaries.

**Why.** `cmd/mooo-matrix-lab/main.go` (527 lines) imports no mooo package
and is a lab tool for an owned homeserver, referenced only from `PLAN.md`
and `research/*.md`. Its `run` function returns string "kinds" and `main`
dispatches on `err.Error()` text.

**Steps.**

1. `git mv cmd/mooo-matrix-lab tools/matrix-lab`. Keep it a Go `main`
   package in the same module so `make check` still compiles and tests it.
2. Update every reference: `grep -rn 'mooo-matrix-lab' --include='*.md' --include='*.go' --include='*.yml' --include=Makefile --include=Dockerfile .`
3. Inside `main.go`, replace the string-kind protocol with typed sentinel
   errors and one `exitKind(err error) string` mapper. Split `run` into one
   function per operation (`validate`, `startup`, `sync`, `decrypt`,
   `sendText`, `sendFile`). Do not change the CLI flags or exit codes; the
   existing `main_test.go` must pass unchanged apart from the import path.

**Acceptance.** `ls cmd` shows `mooo-bridge` and `mooo-lab` only.
`research/matrix-lab.md` and `PLAN.md` point at the new path. Tests pass.

**Depends on.** Nothing.

---

### Workstream C: protocol package consolidation

#### C1. One authenticated Mac HTTP helper (M)

**Goal.** `reactions`, `tokenrefresh`, `friends` and `registration` share a
single profile type, `Doer` interface, header policy, bounded response
reader and strict JSON decoder.

**Why.** Four copies of `type Doer interface { Do(*http.Request) ... }`
(`tokenrefresh.go:29`, `registration/http_executor.go:25`,
`reactions.go:177`, `friends/add_by_phone.go:122`), four header blocks
setting `A`/`Accept-Language`/`User-Agent`/`Authorization`, three
`ClientProfile` structs plus `MacClientProfile`, and six copies of
"Do → LimitReader(max+1) → size check → status check → Decode" that
disagree on details (tokenrefresh accepts only 200, others any 2xx;
`ErrTransport` exists only in reactions).

**Files.** New `internal/protocol/macweb/` (or `kakaoweb`); edits in the four
packages; `internal/client/client.go` (`friends.Doer` parameter type);
`internal/bridge/connector/{reactions.go,login.go}` where profiles are built.

**Steps.**

1. Create `macweb` with:
   - `type Profile struct { AppVersion, OSVersion, Language, DeviceUUID, AccessToken string; UserID int64 }` and `func (p Profile) Validate(requireAuth bool) error`.
   - `type Doer interface { Do(*http.Request) (*http.Response, error) }`.
   - `func ApplyHeaders(req *http.Request, p Profile, authorized bool)`.
   - `func Do(ctx context.Context, d Doer, req *http.Request, maxBody int64) ([]byte, error)` returning wrapped sentinel errors `ErrTransport`, `ErrStatus` (carrying the code), `ErrResponseTooLarge`.
   - `func DecodeJSONObject(body []byte, v any) error` that rejects trailing data (fold `reactions.requireJSONEOF` and the tokenrefresh inline check).
2. Decide the status policy once: accept any 2xx. This is a behaviour change
   for `tokenrefresh` (previously 200 only). Record it in the PR; add a test
   that a 204 from the token endpoint is now accepted, or keep the 200-only
   rule via an option if the maintainer prefers. Default: accept 2xx.
3. Port each package to `macweb`. Each keeps only its request builder, URL,
   response struct and response-specific validation. Keep the existing
   exported request/response types so the connector and client compile
   with minimal edits; the per-package `ClientProfile` becomes
   `type ClientProfile = macweb.Profile` for one release, then is removed in
   a follow-up commit within the same PR once callers are updated.
4. Consolidate the connector's profile construction (grep `ClientProfile{`
   in `internal/bridge/connector`) into one `kc.webProfile()` helper.
5. Keep every existing fixture-driven test; retarget assertions that checked
   package-specific error values to the `macweb` sentinels.

**Acceptance.** One `Doer`, one profile type, one header function in the
repo (`grep -rn 'Accept-Language' internal` returns one production hit).
`tokenrefresh.validSecret`'s UTF-8 and 16 KiB bounds are preserved (they are
response validation, not transport). All four packages' tests pass.

**Depends on.** Nothing. Can run in parallel with A1.

#### C2. Shared BSON field helpers and a single chat-log envelope (M)

**Goal.** One set of typed BSON accessors and one decoder for the
`chatId`/`chatLog`/`logId`/`type`/`authorId`/`sendAt` envelope.

**Why.** Six private helper sets with different int32/int64 widening rules:
`events/events.go` (`requiredInt64`, `exactInt64`, `optionalExactInt64`,
`optionalString`, `optionalInt64`, `requiredString`),
`events/message_gap.go` (`uniqueOptionalInt64`), `chat/create.go`
(`int64Field`), `chat/write.go` (`int64Value`), `chatmeta/chatmeta.go`
(`lookup`, `integer`, `int64Field`, `int32Field`, `stringField`,
`boolField`, `int64Array`, `stringArray`), `media/media.go` (`stringField`,
`integerField`, `optionalInt64`, `integerValue`), `syncmsg/syncmsg.go`
(`integer`), `client/session.go` (`bsonInt64`). The MSG envelope is parsed
in `events.messageEnvelope`, `events.messageDeliveryEnvelope`, and again
inside every `media.Decode*Message`.

**Files.** New `internal/protocol/bsonfield/`; the packages above.

**Steps.**

1. Create `bsonfield` with generic accessors returning `(value, present bool, err error)`:
   `Int64`, `Int32Exact`, `String`, `Bool`, `Int64s`, `Strings`,
   `Document`, each taking `(raw bson.Raw, keys ...string)` where multiple
   keys mean "first present wins" (chatmeta needs this). Document the
   widening rule once: `int32` and `int64` both widen to `int64`; `double`
   is rejected. Write table tests from the union of the existing helper
   tests.
2. Before porting a package, diff its helper semantics against `bsonfield`.
   Where a package is stricter (for example `exactInt64` rejects int32),
   use the exact variant; do not loosen or tighten silently. List every
   difference in the PR.
3. Port packages one at a time: `chat`, `syncmsg`, `media`, `chatmeta`,
   `events`, then `client`. Delete the private helpers as each is ported.
4. Add `events.ChatLogEnvelope{ChatID, LogID, AuthorID, SentAt int64; Type int32; Log bson.Raw}`
   and `events.DecodeChatLogEnvelope(body bson.Raw)`. Change
   `media.Decode{Photo,Audio,Video,File,Contact,MultiPhoto,Sticker}Message`
   to take `(env ChatLogEnvelope)` (or the envelope fields plus the
   attachment string) instead of the raw packet body. Because `media` must
   not import `events` (layering: `events` imports `media`), put the envelope
   type in `media` or in a new leaf package `internal/protocol/chatlog`
   that both import. Recommended: `chatlog`.
5. Collapse the six identical stanzas in `events.decodeMessage` into a
   table (see C3).

**Acceptance.** `grep -rn 'func .*int64Field\|func integer(\|func requiredInt64' internal/protocol` returns nothing. Every parity fixture test still passes unchanged.

**Depends on.** Nothing, but do C3 in the same or the next PR.

#### C3. Embedded `Position` and a decode table in `events` (M)

**Goal.** `events.MessagePosition` is not a 111-line type switch; message
event types share a header; `decodeMessage` dispatches through a table.

**Why.** `events.go:84-193` enumerates 18 event types in value and pointer
forms to read `ChatID`/`LogID`. `Decode` returns values, so pointer cases are
dead. The `Kind()/isEvent()/String()/GoString()` quartet repeats ~30 times.
`decodeMessage` (`events.go:730-816`) has six identical "call
`media.DecodeX`, map error, wrap" stanzas and mixes `messagetype.Post`,
`media.PhotoType`, `chat.ReplyType` spellings for the same constants.

**Steps.**

1. Add `type Position struct { ChatID, LogID int64 }` with
   `func (p Position) Position() Position`. Embed it in every message event
   struct (text, reply, media wrappers, edit/delete, mini text, gap). Keep
   field names so fixtures and the connector (`X.ChatID`) compile; embedding
   promotes them.
2. `MessagePosition(event Event) (Position, bool)` becomes a single
   interface assertion. Delete the switch. The connector's
   `remoteEventFor` (E7) benefits.
3. Replace the `decodeMessage` switch with
   `var messageDecoders = map[int32]func(chatlog.Envelope) (Event, error)`
   where media types register their decoder. Keep the error mapping to
   `ErrMalformedEvent` in one place.
4. Delete the type aliases `chat/write.go:15`, `chat/reply.go:12`,
   `media/media.go:30`, `media/upload.go:20-21`, `media/album.go:13` and use
   `messagetype.*` everywhere. The connector already does in ten files.
5. Generate the `String()`/`GoString()` methods only where they redact; if
   they are plain, delete them and rely on `%T`. Check tests that compare
   `String()` output.

**Acceptance.** `events.go` under 700 lines. `MessagePosition` under 10
lines. One spelling of each message-type constant. Fixture tests unchanged.

**Depends on.** C2 (envelope).

#### C4. One strict JSON decoder for attachment payloads (M)

**Goal.** A single bounded, duplicate-key-rejecting, trailing-data-rejecting
JSON object decoder replaces the hand-rolled key loops.

**Why.** The "Token `{` → loop keys with `seen[key] || len(seen) >= 64` →
trailing-EOF" loop is copied in `media/{audio,contact,file,sticker,video,multiphoto}.go`,
`events/{location,profile,message_gap}.go`, `chatmeta/display.go`, while
`events/bounded_json.go` has a stricter recursive version used only by
post/vote, and `registration/json.go` has a third hand-rolled decoder.

**Steps.**

1. Create `internal/protocol/strictjson/` with
   `func Unmarshal(data []byte, v any, limits Limits) error` where `Limits`
   has `MaxKeys`, `MaxDepth`, `MaxArray`, `MaxBytes`. Implementation: walk
   with `json.Decoder` enforcing bounds and duplicate keys, then
   `json.Unmarshal` into `v` with `DisallowUnknownFields` optional. Start
   from `events/bounded_json.go`, which is the strictest existing version.
2. Write a table test that captures every rejection the eleven existing
   loops perform (duplicate key, more than 64 keys, trailing bytes, nested
   depth, non-object root). Derive cases from the existing tests of each
   file before deleting them.
3. Port each file: decoder shrinks to
   `strictjson.Unmarshal(text, &a, limits) == nil && validX(a)`. Keep each
   file's semantic validation (`validX`) untouched.
4. Port `registration/json.go` `decodeJSONObject`/`decodeInteger`/`decodeFiniteNumber`/`decodeString`/`decodeBool` and the manual nested decode in `QRRegistrationService.Poll`.
5. Have C1's `macweb.DecodeJSONObject` call `strictjson` too.

**Acceptance.** `grep -rn 'len(seen) >= 64' internal` returns nothing. Every
fixture under `research/fixtures/` that these decoders consume still passes.
Any fixture whose outcome changes is a behaviour change: stop, add a failing
test, and report it in the PR rather than adjusting the fixture.

**Depends on.** C1 for the last step only.

#### C5. Move protocol field knowledge out of `internal/client` (M)

**Goal.** `internal/client` only orchestrates; wire field names and status
codes live in `internal/protocol`.

**Why.** `client/session.go` hard-codes LOGINLIST/LCHATLIST page fields
(`chatDatas`, `delChatIds`, `lastTokenId`, `lastChatId`, `lbk`, `eof`, `c`,
`l`, `ll`, statuses `-305`/`-310`) in `finishLoginSyncSession`,
`updateLoginCursor`, `loginChatTarget`, `setLoginTarget`,
`parseChatPageContent` (~205 lines); GETCONF/CHECKIN request bodies as raw
`bson.D` literals and reply parsers `bookingTargets`/`endpoint` (~75 lines);
`client/getmsgs.go` defines `GetMessagesCommand` and hand-builds
`chatIds`/`logIds`/`chatLogs`. Every other command has a
`protocol/<pkg>.XCommand` plus `Request.MarshalBSON()` and
`DecodeXResponse()`, which is what makes it fixture-testable.

**Steps.**

1. `sessionlogin`: add `LoginListPage` struct and
   `DecodeLoginListPage(body bson.Raw) (LoginListPage, error)` covering the
   fields above and the `-305`/`-310` handling. `client.updateLoginCursor`
   consumes the struct. Add a synthetic fixture test for the decoder.
2. New `internal/protocol/booking/` with `GetConfRequest`, `CheckinRequest`
   (fields `MCCMNC`, `model`, `os`, `ntype`, `appVer`, `lang`, `countryISO`,
   `useSub`) with `MarshalBSON`, and `DecodeBookingTargets`,
   `DecodeCheckinEndpoint`. Move the constants.
3. `chat`: add `GetMessagesRequest`/`GetMessagesCommand`/`DecodeGetMessagesResponse`;
   `client/getmsgs.go` shrinks to orchestration.
4. Move `client.responseStatus` to `loco.Status(packet) (int32, error)`.
   Then delete the redundant status re-checks after `Session.Request` in
   `client/getmsgs.go` and `client/media.go` (`Session.Request` already
   returns `StatusError` for non-zero status; verify by reading `requestRaw`
   before deleting each).
5. Add `events.DecodeChatLog(chatID int64, chatLog bson.Raw) (Event, error)`
   and replace the three hand-built synthetic `MSG` packets in
   `client/sync.go`, `client/getmsgs.go`, `client/history.go` (and the
   internal one in `events/edit_delete.go`). Delete the duplicated nested
   `chatId` consistency check in `history.go` and `getmsgs.go` by doing it
   once inside `DecodeChatLog`.

**Acceptance.** `grep -n '"chatDatas"\|"delChatIds"\|"lastTokenId"\|"lbk"\|"MCCMNC"\|"ntype"' internal/client` returns nothing. `session.go` under 800 lines after A1 and C5. New decoders have fixture tests under `research/fixtures/` with `provenance` per `research/parity-fixtures.md` (`static` is acceptable for decoders moved without behaviour change; say so).

**Depends on.** A1 (smaller session.go), C2 (bsonfield).

---

### Workstream D: client simplification

#### D1. Deduplicate `Client.ensureSession`; unify `Close`/`Shutdown` (S)

**Why.** `client.go:ensureSession` contains the dial → `InstallSession` →
publish → `closed` dance twice (before and after credential renewal), ~65
lines duplicated. `Client.Close` duplicates the first half of
`Client.Shutdown`; only `internal/command/{chats,contact_photo}.go` call
`Close`.

**Steps.**

1. Extract `func (c *Client) adoptSession(session *Session) (*Session, error)`
   containing the checkpoint install, the late-close check and the publish.
   `ensureSession` becomes: loop { fast path; wait for in-flight; dial;
   adopt; on `-950` renew once and dial again }.
2. Make `Close()` call `Shutdown` with a short internal timeout (document the
   value as a constant) or change the two command callers to use
   `Shutdown(ctx)` and delete `Close`. Prefer the latter; it removes a public
   method.
3. Keep every existing test in `client_shutdown_test.go` and
   `client_test.go` green; they are the regression net for the lifecycle.

**Acceptance.** `ensureSession` under 70 lines. One shutdown path.

**Depends on.** A1.

#### D2. Generic request helpers and a single nil-guard (S)

**Why.** Twenty `Client` methods are
`session, err := c.ensureSession(ctx); if err != nil { return zero, err }; return session.X(...)`.
Twelve `Session` methods are
`body, err := req.MarshalBSON(); reply, err := s.Request(ctx, Cmd, body); return Decode(reply.Body)`.
Six HTTP methods repeat the same closed-check and `state, doer := c.state, c.http`
prologue. The `s == nil || ctx == nil` guard appears 44 times, placed
inconsistently.

**Steps.**

1. Add `func withSession[T any](c *Client, ctx context.Context, fn func(*Session) (T, error)) (T, error)`
   and port the twenty methods.
2. Add `func call[Req interface{ MarshalBSON() ([]byte, error) }, Resp any](s *Session, ctx context.Context, command string, req Req, decode func([]byte) (Resp, error)) (Resp, error)`
   and port the twelve methods.
3. Add `func (c *Client) httpProfile() (friends.Doer, authstate.State, error)`
   (after C1, `macweb.Doer`) and port the six HTTP methods.
4. Move the nil guards into `Session.Request` and `withSession`; delete the
   other 40-odd copies.

**Acceptance.** No method in `internal/client` begins with
`session, err := c.ensureSession(ctx)` except `withSession`. Tests pass.

**Depends on.** A1, D1.

#### D3. One pending-request map (S)

**Why.** `Session` keeps `pending`, `pendingByUniqueID`,
`pendingUniqueIDByID` in sync by hand in five methods with lazy nil
re-initialisation. The unique ID is a pure function of `(method, packetID)`.

**Steps.**

1. Replace the three maps with `pending map[uint32]pendingRequest` where
   `pendingRequest{method string; result chan requestResult}`.
2. `dispatchPacket` looks up by `header.PacketID` and compares `method`;
   a mismatch is routed exactly as today (read the current
   `pendingUniqueIDByID` branch to preserve the wrong-method same-ID rule
   that `session_uid_correlation_test.go` asserts).
3. Replace the goroutine-per-write cancellation in `writeRawPayload` with
   `context.AfterFunc`, as `media.go` already does.

**Acceptance.** `session_uid_correlation_test.go` passes unchanged. No
`make(map` inside `requestRaw`/`failPending`.

**Depends on.** A1.

#### D4. Wrap causes instead of returning bare sentinels (M)

**Why.** `ErrProtocol` is returned bare at ~100 sites and `ErrBootstrap` at
12 sites in `session.go`, discarding the underlying DNS/TLS/BSON error. Only
`media.go` wraps with `%w`.

**Steps.**

1. Add `func protocolErr(command, field string) error` returning
   `fmt.Errorf("%w: %s.%s", ErrProtocol, command, field)` and use it at
   every bare `ErrProtocol` site that has a field in scope.
2. At each `return nil, ErrBootstrap` / `ErrLogin` site that has an `err`,
   return `fmt.Errorf("%w: <stage>: %w", ErrBootstrap, err)` (Go supports
   multiple `%w`). Stage names: `getconf dial`, `getconf`, `checkin dial`,
   `checkin`, `loginlist`, `lchatlist`.
3. Audit tests that compare errors with `==`; switch them to `errors.Is`.
4. Confirm no wrapped message can contain a secret: the wrapped errors come
   from `net`, `tls`, `bson`; none include request bodies. Say so in the PR.

**Acceptance.** `grep -c 'return nil, ErrBootstrap$' internal/client/session.go` is 0. `errors.Is(err, client.ErrBootstrap)` still true in the connector's `bootstrapFailure` path (test exists in connector).

**Depends on.** A1, C5 (fewer sites).

#### D5. Shared private-JSON file store for `authstate` and `continuity` (M)

**Why.** `authstate/authstate.go:335-524` and
`continuity/continuity.go:507-639` both implement `validatePrivateDir`,
`writeAtomic` (CreateTemp → Chmod 0600 → Encode → Sync → Close → Rename →
fsync dir), `writeInitial` (O_EXCL), `syncDirectory` with the Windows no-op,
and `read` (LimitReader 2 MiB + DisallowUnknownFields + trailing-decode EOF
check), with cosmetic differences.

**Steps.**

1. Create `internal/privatejson/` with
   `Read[T any](path string) (T, bool, error)`, `WriteAtomic[T any](path string, v T) error`,
   `WriteInitial[T any](path string, v T) error`, `ValidatePrivateDir`,
   `ValidatePrivateFile`. Decide the one `SetEscapeHTML` policy and the one
   "missing file" return shape; document both.
2. Port `authstate` then `continuity`. Keep their public APIs.
3. In `continuity`: replace the five sorted-slice `remove*` helpers and the
   four hand-rolled sorted inserts with two generics `removeByChat` /
   `upsertByChat` over a `chatKeyed interface{ chatID() int64 }`.
4. Move `client/sync.go`'s `hasGapThrough`, `committedMax`,
   `firstDeliveryStart` onto `continuity.Checkpoint` as methods next to the
   sort invariant they depend on.
5. Keep every existing permission/mode test (`0600`, directory `0700`,
   symlink rejection) and run them on both packages through the shared code.

**Acceptance.** `authstate.go` and `continuity.go` each lose at least 100
lines. All file-permission tests pass. `privatejson` has its own tests for
atomic rename and fsync ordering (use a fake `os`-level hook only if one
already exists; otherwise test via observable file state).

**Depends on.** Nothing.

#### D6. Export hygiene in `internal/client` (S)

**Steps.**

1. Unexport `Session` → `session` and its methods `Request`, `Pushes`,
   `InitialChatData`, `SyncMessages`; nothing outside the package references
   them (the connector uses its own `kakaoClient` interface).
2. Delete `Client.Pushes`, `pushConsumerMode`, `ErrPushConsumerSelected`
   and the two-mode admission in `Events` if `grep -rn '\.Pushes(' internal cmd --include='*.go' | grep -v _test.go | grep -v internal/client/`
   is empty.
3. For each exported identifier with no production use outside the package
   (`ErrCommitOrder`, `ErrContactProfileUnavailable`,
   `ErrOutSegmentWorkerQueueFull`, `ReactionDetails`, `AddFriendByPhone`,
   `InSegmentTimeoutController`, `PushReceiptSender`,
   `EligiblePushReceiptPacket`): delete if it is also unused in production
   inside the package; otherwise unexport. `AddFriendByPhone` and
   `ReactionDetails` are wired to real HTTP APIs with no caller; delete them
   and their protocol-level request builders only if the maintainer confirms
   in the PR that friend-add and reaction-detail lookups are not planned;
   otherwise keep and note the exception.
4. Collapse `connectSession` → `connectSessionWithResume` →
   `connectSessionWithResumeOptions` into one function taking a
   `sessionConfig` struct.

**Acceptance.** `go doc ./internal/client` lists only `Client`,
`OpenOptions`, `Open*`, errors with a consumer, and types the connector's
`kakaoClient` interface needs.

**Depends on.** A1, B1.

#### D7. Consolidate client tests onto `internal/testsupport/loco` (M)

**Why.** `internal/client/mock_backend_test.go` (`scriptedBackend`,
`expectRequest`, `pushAfter`, `writeBackendPacket`, `readSecurePayload`,
plus a `wireConn.readRequest` method defined in a test file) duplicates
`internal/testsupport/loco/script.go`. Thirty-one client test files build
`&Session{...}` by hand and twenty-four assign `session.wire =` directly.

**Steps.**

1. Give `client.TestDialers` a constructor from a `testloco` endpoint so
   tests can drive `connectSession`/`Client` end to end.
2. Port tests file by file from `scriptedBackend` to `testsupport/loco`.
   After each file, delete the now-unused helpers from
   `mock_backend_test.go`.
3. Leave white-box read-loop tests that genuinely need a bare `session`
   (document which, in a comment at the top of each such file).
4. Delete `mock_backend_test.go` when empty.

**Acceptance.** One scripted LOCO peer implementation in the repo.
`testpolicy` passes. Test runtime for `internal/client` does not grow by
more than 20 %.

**Depends on.** A1 (removes ~2,400 test LOC first), D6.

---

### Workstream E: connector simplification

#### E1. Role interfaces instead of one fat interface plus runtime upgrades (S)

**Why.** `connector/client.go:34-58` declares a 23-method `kakaoClient`,
and the package additionally type-asserts the same value to seven optional
interfaces at runtime (`reactionAPI` in `reactions.go`, anonymous
`MiniReactionDetails`, `reactionMetaSyncer` in `reaction_resync.go`,
`groupHistorySource` and anonymous `InitialSyncTargets` in
`group_history.go`, `messageFetcher` in `edits.go`, `chatOnRoomReader` in
`read_receipts.go`). `client.Client` implements all of them; the optional
shape exists only so `fakeKakao` need not. A missing method on the real
client would degrade silently (`resyncReactions`, `recoverReadWatermarks`
just return).

**Steps.**

1. Define in `connector/client.go`:
   `sessionAPI` (Connect, Events, CommitEvent, ResumeTargets, CatchUp, Close, Shutdown),
   `chatMetaAPI` (ListChats, ChatInfo, PersonalMeta, MoimMeta, Members, MemberList, ChatOnRoom),
   `outboundAPI` (SendText, SendReply, SendImage, SendUpload, SendAlbum, ModifyMessage, DeleteMessage, MarkRead, CreateChat, AddMembers),
   `reactionAPI` (React, ReactionMembers, MiniReactionDetails, ReactionMetaSync),
   `historyAPI` (ReadHistoryPage, GetMessages, InitialSyncTargets),
   and `kakaoClient interface { sessionAPI; chatMetaAPI; outboundAPI; reactionAPI; historyAPI }`.
2. Delete the seven runtime assertions; call methods directly.
3. Add the missing methods to `fakeKakao` in `client_test.go` once (return
   zero values or a configurable error). Remove the decorator fakes
   (`fetchingKakao`, `chatOnRoomKakao`, `metaSyncBackend`,
   `reactionTestBackend`, `historyTestSource`) where they only existed to
   add a method.
4. Narrow parameter types where a function uses one role (for example
   `sendMatrixImage(ctx, c outboundAPI, ...)`).

**Acceptance.** `grep -n '\.(interface' internal/bridge/connector/*.go | grep -v _test` returns nothing. Tests pass.

**Depends on.** Nothing. Good first connector task.

#### E2. One key-value helper and one portal-metadata save helper (S)

**Why.** Raw SQL against `kv_store` appears in `group_history.go`,
`group_metadata.go`, `outbound_edits.go`, `outbound_attempts.go` (eight
statements, three shapes), while `read_receipts.go` and
`reaction_resync.go` use `DB.KV.Get/Set`. The "save, re-read, verify
durable" ritual with bespoke error strings is copied six times
(`group_create.go` ×2, `group_access.go`, `group_membership.go`,
`group_metadata.go`, `group_history.go`).

**Steps.**

1. Add `connector/kv.go`: `type kvStore struct { db *dbutil.Database; bridgeID networkid.BridgeID }` with `get(ctx, key) (string, bool, error)`, `put(ctx, key, value) error` (upsert then read back and compare; return one `errNotDurable`), `putIfAbsent(ctx, key, value) (bool, error)`, `deleteOlderThan(ctx, prefix, stamp) error`.
2. Port the eight raw statements. Keep the comment in `group_metadata.go`
   explaining why raw SQL rather than `DB.KV` was chosen, moved to `kv.go`,
   and either convert the two `DB.KV` users to `kvStore` or document why
   they differ.
3. Add `func (kc *KakaoClient) savePortalMeta(ctx, portal *bridgev2.Portal, mutate func(*KakaoPortalMetadata), verify func(*KakaoPortalMetadata) bool) error`
   implementing clone → mutate → Save → GetByKey → compare → rollback once.
   Port `recordSourceRemoval`, `saveMembershipCheckpoint`, `saveGroupAttempt`
   and the other three.

**Acceptance.** `grep -n 'kv_store' internal/bridge/connector/*.go | grep -v _test | grep -v kv.go` returns nothing. The persistence tests (`framework_conversion_persistence_test.go`, `group_*_test.go`) pass unchanged.

**Depends on.** Nothing.

#### E3. A store interface that removes the "no bridge DB" branches (M)

**Why.** `kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil`
(or its inverse) appears 29 times across 15 production files. Several guard
whole alternative code paths that run only under the unit-test harness
(`newTestClient` builds a `UserLogin` with no `Bridge`): `groupDiscovery`
in `discovery.go`, the no-DB branch of `applySourceLeave` and
`checkSourceAccess` in `group_access.go`, and the in-memory
`reactionRevisions` map plus `reactionMu` in `reactions.go` which exist only
because tests have no DB. These are test-only production paths.

**Steps.**

1. Define `type store interface` in `connector/store.go` covering the calls
   the connector makes: `portalByKey`, `firstMessagePart`,
   `lastNMessagesInPortal`, `updateMessage`, the four `kvStore` methods from
   E2, `allPortalsForLogin`. Implement `bridgeStore{*database.Database}`.
2. `newKakaoClient` takes a `store`; `LoadUserLogin` passes
   `bridgeStore{login.Bridge.DB}`.
3. In `client_test.go` add `memStore` (maps) and make `newTestClient` use
   it.
4. Delete every nil-DB guard and its alternate branch. Delete
   `groupDiscovery`, `reactionRevisions`, `reactionMu`.
5. Where a deleted alternate branch was the only thing a test exercised,
   rewrite the test to assert the DB-backed behaviour through `memStore`.

**Acceptance.** `grep -rn 'Bridge.DB == nil\|Bridge.DB != nil' internal/bridge/connector` returns nothing. `KakaoClient` has no `reactionRevisions` field. Tests pass.

**Depends on.** E2.

#### E4. Split `chatInfoFromClient` into fetch and project; one roster check (M)

**Why.** `chatInfoFromClient(ctx, portal, c, regularGroupOnly bool, requirePersonal ...bool)`
is 181 lines doing CHATINFO → PersonalMeta → display checkpoint → MEMLIST →
MEMBER → member map → mute → name/avatar → DM detection → announcement. The
variadic bool has one `true` caller. Callers in `group_membership.go`,
`group_create.go` and `group_reconcile.go` then call `MemberList` a second
time for the roster the function already fetched (two MEMLIST round-trips
per refresh, per bootstrap group, per history page). The "expected set vs
`info.Members.MemberMap`" check is copied three times; the "cache profiles"
loop twice (`client.go`, `convert.go`).

**Steps.**

1. Replace the bools with `type chatInfoOptions struct { regularGroupOnly, requirePersonalMeta bool }`.
2. Split into `fetchChatSnapshot(ctx, c chatMetaAPI, chatID int64, opts) (chatSnapshot, error)`
   (all I/O, exactly one MEMLIST; `chatSnapshot{data chatmeta.ChatData; roster []int64; profiles []chatmeta.Member; personal *chatmeta.RoomMeta}`)
   and pure `projectChatInfo(kc, portal, snap chatSnapshot) (*bridgev2.ChatInfo, error)`.
3. Add `rosterMatches(snap, expected map[int64]bool, allowSubset bool) error`
   and use it in create/membership/reconcile. Delete their second
   `MemberList` calls.
4. Add `kc.cacheProfiles(profiles []chatmeta.Member, requested []int64)` and
   use it in both places.
5. Write unit tests for `projectChatInfo` using synthetic `chatSnapshot`
   values (no fake client needed).

**Acceptance.** Exactly one `MemberList` call per chat refresh (assert with a
counting fake). No variadic bool parameters in the package. `projectChatInfo`
has direct unit tests.

**Depends on.** E1.

#### E5. Extract the connection lifecycle out of `KakaoClient` (L)

**Why.** 25 of `KakaoClient`'s ~35 fields are lifecycle bookkeeping
(`client, cleanup, connecting, stopping, done, cleanupPumpDone, cleanupDone,
cleanupBusy, cleanupRetry{Cancel,ID,Attempts,Done}, connectCancel,
lifecycle, lifecycleCancel, retry{Cancel,Done,ID}, generation,
connectingGeneration, recoveryTry, wait, disconnectGate`) under one mutex
that also guards the unrelated `profiles` and `sourceBlocked` caches.
`Disconnect` is 147 lines. The "worker with cancel + done + monotonic id"
pattern is implemented twice (`retryAfter`, `scheduleCleanupRetry`); the
"shutdown `c` then release ownership if `kc.cleanup == c`" block appears
five times; `group_reconcile.go` writes `kc.cleanup = c` directly from
outside the lifecycle code.

**Steps.** Do these as separate commits in one PR, running
`reconnect_test.go` and `delivery_recovery_test.go` after each.

1. Split the mutex: `cacheMu` for `profiles`, `profileRefreshAfter`,
   `sourceBlocked`; `mu` stays for lifecycle. Mechanical.
2. Add `type worker struct { cancel context.CancelFunc; done chan struct{}; id uint64 }`
   with `start(parent context.Context, fn func(ctx))` and
   `stop(ctx context.Context) error` (cancel then join with deadline).
   Replace the retry and cleanup-retry field groups with two `worker`
   values. `Disconnect`'s hand-written `joinWorkers` becomes two `stop`
   calls.
3. Add `type connection struct { c kakaoClient; pumpDone chan struct{}; generation uint64 }`
   and `func (kc *KakaoClient) release(conn *connection, err error)` holding
   the single copy of the "if `kc.cleanup == c` then clear busy/done/cleanup"
   block. Replace the five copies.
4. Move the temporary-client path in `group_reconcile.go` behind
   `kc.openDetached()` / `kc.releaseDetached(conn, err)` so no file outside
   `client.go` touches `kc.cleanup`.
5. In `connectOnce`, replace the seven
   `if kc.isCurrent(generation, ctx) { kc.sendState(...) }` with a local
   `report := func(s status.BridgeState) { ... }` closure.
6. Only now, if `Disconnect` is still over 60 lines, extract
   `shutdownWithDeadline(conn, ctx)`.
7. Move the lifecycle code (`Connect`, `connectOnce`, `retryAfter`,
   `recoveryDelay`, `retryableRecoveryError`, `shutdownBootstrap`, `run`,
   `sessionEnded`, `scheduleCleanupRetry`, `Disconnect`, `worker`,
   `connection`, `release`) into `connector/lifecycle.go`. `client.go`
   keeps the NetworkAPI surface.

**Acceptance.** `KakaoClient` has at most 12 fields outside the
`connection`/`worker` types. `Disconnect` under 60 lines. The release block
exists once. `reconnect_test.go`, `delivery_recovery_test.go`,
`client_shutdown`-style tests pass unchanged (they are the specification;
if one must change, stop and explain why in the PR).

**Depends on.** E3 (fewer fields), E4 (smaller client.go). Do last in this
workstream.

#### E6. Outbound send scaffolding (S)

**Why.** `HandleMatrixMessage`, `sendMatrixImage`, `sendMatrixUpload`,
`sendMatrixAlbum` in `client.go` repeat: a 7-line timeout-plus-lifecycle
prologue (×3), the "download failed → Retriable/NetworkError/IsCertain"
status (×3), the "no replies with media" refusal and intent lookup (×2),
and the `MatrixMessageResponse{DB: &database.Message{...}}` epilogue (×4).
`WrapErrorInStatus(...).WithStatus(Fail).WithErrorReason(Unsupported).WithIsCertain(true).WithMessage(m).WithSendNotice(true)`
appears 20 times package-wide with four thin wrappers.

**Steps.**

1. Add `connector/status.go`: `certainRefusal(err, msg)`,
   `uncertainFailure(err, msg)`, `retriableFetchFailure(err, msg)`. Port
   the 20 chains and collapse `replyTargetStatus`, `albumRejectedStatus`,
   `uploadRejectedStatus`, `editRejected`, `deleteRejected`,
   `unsupportedRoomMetadataStatus` onto them.
2. Add `kc.outboundCtx(ctx, timeout)`, `kc.sentMessage(chatID, logID, sendAt, typ, preview)`,
   and `kc.withMediaSend(ctx, msg, timeout, func(ctx, c outboundAPI, intent) (*bridgev2.MatrixMessageResponse, error))`.
3. Move lines ~1424–1853 of `client.go` (media send and bounded Matrix
   download helpers) to `connector/outbound_media.go`.

**Acceptance.** `HandleMatrixMessage` under 40 lines. `grep -c 'WithSendNotice(true)' internal/bridge/connector/*.go` totals at most 3 (inside `status.go`). Outbound tests pass.

**Depends on.** E1.

#### E7. Inbound attachment conversion table (S)

**Why.** `audio.go`, `video.go`, `file.go`, `contact.go`, `convertPhoto`
(`convert.go`), `sticker.go`, `multiphoto.go`, `mini_text.go` all do:
timeout → `media.DownloadX` → `deterministicPhotoFailure` → gap notice →
`intent.UploadMedia` → build `MessageEventContent` → blank `URL` when
encrypted `File` is set. `remoteEventFor` in `convert.go` repeats
`newMessage(kc.messageMeta(X.ChatID, X.LogID, X.AuthorID, X.SentAt), makeMessageID(X.ChatID, X.LogID), evt, convertX)`
twelve times.

**Possible bug to verify first.** `convertPhoto` is the only converter that
leaves `content.URL` set when `file != nil` (encrypted room). Every sibling
blanks it. Check the Matrix spec and mautrix behaviour for `m.image` with
both `url` and `file`; if clients prefer `file`, this is harmless drift, if
some clients prefer `url` it is a bug. Either way, write a test asserting
the chosen behaviour for photos in encrypted rooms before refactoring, and
report the finding in the PR.

**Steps.**

1. Add `connector/attachment.go` with
   `type attachmentSpec struct { kind string; msgType event.MessageType; filename, mime string; download func(ctx) ([]byte, error); info func(data []byte) *event.FileInfo; body func() string; meta func() *KakaoMessageMetadata }`
   and one `convertAttachment(ctx, portal, intent, spec) (*bridgev2.ConvertedMessage, error)`.
2. Rewrite audio/video/file/contact/photo/sticker/multiphoto as 5–15-line
   spec constructors. Keep `mini_text.go` separate if its branching does not
   fit; say so.
3. In `remoteEventFor`, use `events.Position()` from C3 and a
   `kc.messageEvent(pos, evt, convert)` helper to halve the switch.

**Acceptance.** `grep -c 'intent.UploadMedia' internal/bridge/connector/*.go | grep -v _test` totals 1 (in `attachment.go`) plus any justified exception. Conversion tests (`*_test.go` per media type) pass unchanged.

**Depends on.** C3 for step 3; steps 1–2 can go first.

#### E8. `handleEvent` as one type switch (S)

**Why.** `client.go:handleEvent` (135 lines) is five separate
`if X, ok := evt.(type); ok { if kc.checkSourceAccess(...) != nil { return false } ... }`
blocks plus a sixth generic guard, two early `switch notice := evt.(type)`
blocks on the same value, and a duplicated "commit if positioned" tail.

**Steps.**

1. Compute `chatID, hasChat := chatOf(evt)` once; run `checkSourceAccess`
   once (after the removal case, which stays first).
2. One `switch evt := evt.(type)` with cases for `ReactionChanged`,
   `ReadStateChanged`, `MessageEdited`, `DeletedMessage`, `MessageDeleted`,
   membership events, `default` → `deliverRemote`.
3. One `commitIfPositioned(c, evt)` helper for the tail.
4. Move membership-specific handling to `group_membership.go` as
   `kc.handleMembershipEvent`.

**Acceptance.** `handleEvent` under 60 lines; one `checkSourceAccess` call
site inside it. Event-routing tests pass unchanged.

**Depends on.** E1.

#### E9. Reactions tidy-up (S)

**Steps.**

1. Add `type legacyReaction struct { typeID reactions.Type; emoji string; emojiID networkid.EmojiID }`;
   replace the seven inline anonymous structs in `reactions.go`.
   `var legacyReactions = [...]legacyReaction{...}` indexed by type with a
   derived `byEmoji` map; `legacyReactionByType` becomes an index.
2. Move `applyReactionChange` from `client.go` to `reactions.go`.
3. `validateMatrixReaction` returns `(reactions.Request, legacyReaction, error)`.

**Acceptance.** No anonymous struct literals with a `typeID` field remain.
Reaction tests pass.

**Depends on.** Nothing.

#### E10. One sqlite framework fixture for connector tests (M)

**Why.** `dbutil.NewWithDialect(..., "sqlite3")` is built at 25 sites in 13
test files under five differently named constructors
(`newFrameworkBridge`, `newGroupCreationFramework`, `newReplyFramework`,
`newReadReceiptFramework`, `newFrameworkReactionFixture`), with about twelve
near-identical `*Intent` and `*MatrixConnector` fakes.

**Steps.**

1. Add `connector/testsupport_test.go` with
   `newFrameworkBridge(t, opts frameworkOpts) (*bridgev2.Bridge, *dbutil.Database)`
   and one `recordingIntent` (embeds `bridgev2.MatrixAPI`; records
   `UploadMedia`, `SendMessage`, `SendState`, `EnsureJoined`) with
   per-method override hooks.
2. Port one test file at a time; delete each private constructor and intent
   fake as it becomes unused.
3. After E3, `memStore` and `newFrameworkBridge` are the only two ways a test
   obtains persistence; document when to use which at the top of
   `testsupport_test.go`.

**Acceptance.** One `NewWithDialect` call site in test code. Connector test
runtime does not grow.

**Depends on.** E3 recommended first.

---

### Workstream F: lab CLI

#### F1. Table-driven subcommands in `internal/command` (S)

**Why.** `command.go` walks `args` by hand with four separate `--help`
checks; `parseOptions` reimplements `--name value`/`--name=value`;
`chats.go` and `contact_photo.go` duplicate the private output-file
reservation (private parent dir → `O_EXCL` 0600 → remove unless kept);
`cmd/mooo-lab/main.go` re-spells subcommand names to pick the shadow mode.

**Steps.**

1. `var handlers = map[string]handler{"auth init": ..., "auth inspect": ..., "chats list": ..., "contacts photo": ...}`
   where `handler{live bool; run func(args []string, out io.Writer) error}`.
2. One `flag.FlagSet` per handler with `ContinueOnError` and
   `SetOutput(io.Discard)` so no flag value is ever echoed (preserve the
   current no-echo property; add a test that a bogus flag produces no output
   containing the value).
3. `reservePrivateOutput(path string) (*os.File, commit func() error, abort func(), error)`
   used by both commands.
4. Export `command.IsLive(name string) bool` and use it from `mooo-lab`'s
   `main.go` instead of the duplicated names.

**Acceptance.** `command.go` under 120 lines. Existing `command` and
`mooo-lab` tests pass; help text byte-identical (add a golden test before
refactoring if none exists).

**Depends on.** B1 (shadow mode wiring in `main.go`).

## 4. Recommended order

Tasks with no shared files can run in parallel on separate branches. Merge
in this order to keep rebases small:

1. **A1** (delete unbound seams) — unlocks most of D and shrinks everything.
2. In parallel: **C1**, **D5**, **E1**, **E2**, **E9**, **B2**.
3. **B1**, **A2**, **A3**, **D1**, **D3**.
4. **C2** then **C3**; **E3** then **E4**.
5. **C4**, **C5**, **D2**, **D4**, **E6**, **E7**, **E8**.
6. **D6**, **E5** (lifecycle), **F1**.
7. **D7**, **E10** (test consolidation last; they are easier once the
   production surface is settled).

After each merge, re-run the `deadcode` command and record the count in the
PR. Expected end state: zero unreachable functions, `session.go` and
connector `client.go` each under 800 lines, no duplicated HTTP/BSON/JSON/file
helpers across protocol packages.

## 5. Things deliberately out of scope

- Any change to wire behaviour, fixtures' expected outputs, or bridge
  features. Those follow `PLAN.md`.
- `research/`, `tools/lab`, `deploy/`, the Dockerfile.
- Renaming packages for taste alone. Package boundaries change only where a
  task says so (`shadow`, `macweb`, `bsonfield`, `strictjson`, `chatlog`,
  `booking`, `privatejson`).
- Adding the status/config owner binding from `PLAN.md`. A1 removes the
  unbound half; the binding is a feature, not a refactor.

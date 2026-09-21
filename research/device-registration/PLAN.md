# Secondary-device registration research plan

Status: active work plan, 2026-09-20.

## Progress

- **DR-0: passed.** The versioned artifact, private output locations, and clean-room
  boundary are recorded.
- **DR-1: partial.** Seven passcode/QR routes, their builders, and their controller
  lifecycles are inventoried. Current-device unregister is attributed; established-
  device listing and selected-device revocation remain unknown.
- **DR-2: static model passed.** Challenge, polling, display-expiry, device-auth,
  refresh, cancellation, and stale-callback behavior are modeled. Poll scheduling
  policy is recovered; server time representation remains open.
- **DR-3: partial.** Route ownership, request fields, and nested device shapes are
  mapped. HTTP method/encoding, signing, shared headers, base URL, and cookies are
  not implementable from current evidence.
- **DR-4: partial.** Numeric QR outcomes and relevant error fields are mapped;
  passcode numeric statuses and complete success schemas remain unresolved.
- **DR-5: partial.** New-device authorization, permanent/temporary success, and the
  restore-or-skip handoff are modeled. The password-check operation's exact owner
  and schema remain unresolved.
- **DR-6: partial.** The encrypted auto-login storage boundary, restore convergence,
  and high-level session inputs are known; persistence flags and final LOCO schema
  remain open.
- **DR-7: partial.** Current-device unregister and server kickout exist; unregister
  schemas, other-device management, and kickout reasons remain open.
- **DR-8: implemented for the reviewed semantic subset.** The pure Go reducer and
  synthetic tests perform no network or credential operations.

## Target outcome

Specify the complete lifecycle by which a current macOS client becomes and remains
an authorized KakaoTalk secondary device. The result must be detailed enough to
implement and test the control plane independently in Go without consulting the
official binary.

This investigation stops at the boundary where registered credentials enter the
LOCO session bootstrap. LOCO carriage login is tracked separately in
`../protocol-bootstrap.md`, but both specifications must name and agree on their
shared handoff values.

## Evidence levels

- **Observed/static:** established from control flow or data flow in the versioned
  official client.
- **Observed/synthetic:** reproduced locally using invented values.
- **Observed/disposable:** observed with the authorized disposable account.
- **Public prior art:** reported externally but not independently reproduced.
- **Hypothesis:** a testable interpretation that must not enter code as a fact.

## Work packages

### DR-0 — Baseline and controls

Pin the macOS client and architecture, private Ghidra project, analysis date, and
public artifact identifier. Keep binaries, addresses, symbols, decompiler output,
QR payloads, credentials, and account-specific observations outside Git.

Exit gate: the private analysis is reproducible without exposing account material.

### DR-1 — Operation inventory and ownership

Account for every operation involved in:

- passcode generation, polling/registration, and cancellation;
- QR generation, approval polling/login, refresh, and cancellation;
- password checking and first-time device authorization;
- device listing, naming, logout, and remote revocation;
- transition from registration success into normal login.

For each operation, identify its transport family, caller state, cancellation
behavior, decoder, and terminal callback. Do not infer relatedness from names alone.

Exit gate: every UI transition has an owned asynchronous operation or is explicitly
recorded as local-only.

### DR-2 — Challenge and timer lifecycle

Recover the passcode and QR challenge state machines, including:

- prerequisites and generation failures;
- challenge identifiers and presentations;
- absolute versus relative expiry values;
- polling cadence and overlap prevention;
- display expiry versus device-authorization expiry;
- refresh, close, cancellation, and stale-callback behavior;
- retryable versus terminal outcomes.

Exit gate: a deterministic reducer can model the lifecycle without UI assumptions.

### DR-3 — Request schemas and request integrity

Trace each request encoder from semantic input to the HTTP boundary. Record:

- route and HTTP method at a publishable level;
- field meanings, scalar types, encodings, and optionality;
- client, locale, device-class, version, and capability fields;
- device identity and password-derived inputs;
- timestamps, nonces, signatures, hashes, or request-authentication inputs;
- cookies, headers, or shared session state required between operations.

Exit gate: each operation has a typed, implementation-neutral request schema, or
the exact unresolved fields are isolated and named.

### DR-4 — Response schemas and result taxonomy

Trace response decoding and map transport errors, protocol status values, payload
fields, and semantic outcomes. Distinguish at least pending, approved, rejected,
expired, unsupported, restricted, suspended, unregistered-device, malformed, and
unknown results.

Exit gate: numeric values and payload presence rules are mapped where evidence
permits; unknown values fail closed in the model.

### DR-5 — Device authorization and login modes

Specify the new-device authorization step and the distinction between permanent
and temporary login, including:

- when a four-character authorization code appears;
- the password-check operation's role and failure behavior;
- main-device confirmation and registered-device reuse;
- history-restoration choice and its effect on the handoff;
- concurrent-device and account-protection outcomes.

Exit gate: all branches converge on explicit success, retry, expiry, or terminal
failure states.

### DR-6 — Credential and persistence handoff

Follow successful registration into credential creation, local persistence,
automatic login, and LOCO bootstrap. Determine which values are local versus
server-issued, their lifetime and sensitivity, and which are cleared by logout,
reset, rejection, expiry, or revocation.

Exit gate: the registration-to-LOCO handoff and cleanup rules are specified without
publishing live material.

### DR-7 — Revocation and recovery

Specify local logout, remote device removal, account password change, server-side
revocation, concurrent-device displacement, expired credentials, and protection
blocks. Identify how each condition is detected and whether re-registration is
allowed.

Exit gate: a headless client can distinguish recoverable reconnect from required
operator action.

### DR-8 — Clean-room Go model

After transfer review, implement a pure state machine under `internal/protocol/`
using semantic inputs and effects. It must not perform network or credential I/O.
Use synthetic tests for happy paths, every terminal result, timer expiry, refresh,
cancellation, stale callbacks, invalid events, and permanent/temporary branches.

Exit gate: formatting, tests with the race detector, and vet pass; the model cites
only public behavioral specifications.

## Current blockers

The disposable account is under an automated secondary-device protection measure.
Do not perform repeated login attempts. Static analysis and invented-value tests
can proceed; disposable-account validation waits for a deliberate future retry.

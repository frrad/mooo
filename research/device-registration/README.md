# Secondary-device registration

Status: active binary-analysis specification, 2026-09-20.

This note describes control flow recovered from an authorized macOS 26.8.0
client. The analysis was static and logged out: it did not submit a login, inspect
an account, or capture traffic. Raw decompiler output and binary-specific anchors
remain outside the repository under `CLEANROOM.md`.

## Documents

- `PLAN.md` defines the end-to-end research work packages and exit gates.
- `PROTOCOL.md` is the reviewed implementation-neutral protocol and state-machine
  specification.
- `EVIDENCE.md` is the append-only evidence and hypothesis ledger.
- `experiments/DR-EXP-001-static-registration-control-flow.md` records the current
  static-analysis pass.
- `experiments/DR-EXP-002-wire-schemas-and-timing.md` records request shapes, QR
  result codes, payload handling, and timer policy.
- `experiments/DR-EXP-003-session-handoff.md` records restore, persistence, LOCO
  handoff, logout, and current-device unregister behavior.

The remainder of this file is an overview. Where it differs from `PROTOCOL.md`,
the latter is authoritative for implementation.

## Common lifecycle

Both supported registration presentations separate three concerns:

1. the displayed challenge's validity;
2. polling for an approval or registration result;
3. expiry of the subsequent device-authorization step, when required.

Each concern has independent state and timer cleanup. Closing a controller stops
its timers and cancels active server-side work when an active registration
identifier exists. A successful registration leaves this state machine and enters
the client's normal login path.

## Four-character passcode flow

The passcode path follows this state model:

```text
generate → display challenge → poll registration ──approved──> normal login
                            │
                            ├──pending──> poll again
                            ├──expired──> expired UI
                            └──cancelled/closed──> cancel and stop
```

The controller receives a four-character challenge and two absolute expiry-like
values from the surrounding login state. One timer updates the visible countdown;
another polls device registration. When the visible validity reaches zero, the
client renders `00:00`, stops registration polling and device-auth timing, and
presents an expired state.

The interpretation of both absolute values is strongly suggested by their use but
is not fully proven. Exact polling intervals and request fields remain unspecified.

## QR flow

QR generation returns an identifier and QR expiry. The client then runs a display
expiry countdown and an approval-poll loop independently:

```text
generate → awaiting phone approval ──approved──────────────> login
   │                 │               ├─ permanent login mode
   │                 │               └─ temporary session
   │                 ├──pending────────────────────────────> poll again
   │                 ├──unregistered device───────────────> device-auth code
   │                 ├──rejected───────────────────────────> terminal failure
   │                 ├──unsupported/restricted/suspended───> terminal failure
   │                 └──invalid response───────────────────> failure
   └──expired──────────────────────────────────────────────> refreshable UI
```

Polling cannot begin without the QR identifier. Expiry stops polling and marks the
presentation refreshable; refreshing clears the expired state and generates a new
challenge. When the device-auth step expires, the client clears the QR identifier
and device-auth code, cancels outstanding work, and returns to an expired/failure
presentation.

The success path carries a boolean distinction between permanent login mode and a
temporary session. The permanent path may offer history restoration or starting
without restoration before normal login. Static control flow does not yet show
where persistent device credentials are stored.

## Observed result categories

The QR response decoder and UI distinguish at least:

- awaiting main-device approval;
- unregistered device requiring device authorization;
- main-device rejection;
- expired QR challenge;
- unsupported device version;
- suspended user;
- restricted email account;
- invalid response;
- unknown failure;
- success, with permanent or temporary semantics.

The QR numeric mapping and relevant error-response fields are now specified in
`PROTOCOL.md`. Passcode numeric statuses and complete success schemas remain open.

## Network operations

The client has distinct operations for:

- generating, registering, and cancelling a passcode challenge;
- generating, polling/logging in with, and cancelling a QR challenge;
- checking a password during the QR/device-registration path.

They share a common HTTP request and asynchronous response-decoding layer. Request
field and nested device shapes are established, but HTTP method/encoding, common
headers, base URL, cookies, and signing remain unresolved.

The current macOS client contains this seven-operation route family:

```text
/mac/account/passcodeLogin/generate
/mac/account/passcodeLogin/registerDevice
/mac/account/passcodeLogin/cancel
/mac/account/qrCodeLogin/generate
/mac/account/qrCodeLogin/cancel
/mac/account/qrCodeLogin/login
/mac/account/qrCodeLogin/passwordCheck
```

Each route is directly associated with its request fields and device-object shape
in `PROTOCOL.md`. That does not yet provide enough shared HTTP-layer detail to send
requests safely.

## Next verification work

- recover HTTP verb/encoding, shared headers, base URL, cookies, and signing;
- recover passcode numeric results and complete success schemas;
- determine the QR URL grammar and check-key validation recipe;
- explain the password-check operation's role in the permanent-login path;
- recover the auto-login persistence flag truth table and final LOCO `LOGIN` schema;
- recover the current-device unregister body and failure cleanup ordering;
- identify list/selected-device revocation APIs and map kickout reasons.

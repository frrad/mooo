# Secondary-device registration state machine

Status: preliminary binary-analysis specification, 2026-09-20.

This note describes control flow recovered from an authorized macOS 26.8.0
client. The analysis was static and logged out: it did not submit a login, inspect
an account, or capture traffic. Raw decompiler output and binary-specific anchors
remain outside the repository under `CLEANROOM.md`.

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

These are semantic states, not numeric wire codes. Their exact response schema and
status-code mapping remain to be recovered.

## Network operations

The client has distinct operations for:

- generating, registering, and cancelling a passcode challenge;
- generating, polling/logging in with, and cancelling a QR challenge;
- checking a password during the QR/device-registration path.

They share a common HTTP request and asynchronous response-decoding layer. This
analysis establishes operation boundaries and callback behavior, but not request
field names, signatures, or cryptographic construction.

## Next verification work

- recover encoder and decoder schemas for each operation;
- map semantic results to numeric status values;
- determine the QR payload encoding;
- explain the password-check operation's role in the permanent-login path;
- trace device identity and request-signing inputs;
- follow permanent success into Keychain/database persistence and LOCO login.

# Registration-to-session login

This package tracks the vertical slice from approved secondary-device registration
through an authenticated, reconnectable LOCO carriage session.

The work is deliberately narrower than general messaging. Completion means a
clean-room client can turn reviewed registration output into a persistent session,
classify disconnects correctly, and reconnect without guessing protocol fields.

## Documents

- `PLAN.md` defines work packages and evidence gates.
- `EVIDENCE.md` records reviewed findings and superseded hypotheses.
- `PROTOCOL.md` holds the implementation-neutral wire and state-machine
  specification and its explicit remaining gaps.
- `experiments/TEMPLATE.md` defines sanitized experiment records.

Raw Ghidra output, decompiler text, live tokens, device identifiers, packet
captures, server addresses tied to an account, and account-specific observations
remain in private lab storage under the project clean-room policy.

## Current boundary

Already established:

- booking, ticket/check-in, and carriage are distinct agents;
- LOCO uses a fixed 22-byte frame, BSON bodies, and an AES-GCM secure layer;
- registration clears transient QR/device-auth values before common session login;
- downstream login consumes at least user identity, access token, and background
  state.
- one client-owned QR authorization persisted credentials, and fresh Go processes
  repeatedly completed `GETCONF -> CHECKIN -> secure-v3 carriage -> LOGINLIST`
  without another approval;
- an authenticated session decoded and matched one exact synthetic self-chat text
  delivered in an unsolicited `MSG` BSON packet.

Still required:

- QR check-key validation, the remaining passcode statuses, and complete success
  schemas;
- remaining response key mappings and production bootstrap transport;
- exact recovery delay and catch-up/delivery-gap behavior;
- complete `chatLog` typing, acknowledgement, cursor persistence, and deduplication;
- token expiry, current-device revocation, kickout, and operator-action boundaries;
- synthetic codecs, state-machine fixtures, and conformance tests.

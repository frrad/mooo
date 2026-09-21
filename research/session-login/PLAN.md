# Session-login research plan

Status: active work plan, 2026-09-20.

## Progress

- **SL-0: passed.** Artifact, private outputs, provenance, and transfer controls are
  established.
- **SL-1: partial.** Base URL, POST/form transport, body shapes, empty explicit
  headers, absence of registration-specific signing, JSON error boundary, and
  password-check contract are mapped. QR check-key validation, passcode numeric
  statuses, and full success schemas remain open.
- **SL-2: partial.** Registration-to-auth state boundaries and current access-token
  placement are known. Complete registration success fields and temporary
  persistence flags remain open.
- **SL-3: static schema passed.** LOGINLIST command, 17 request fields/types,
  response properties, token placement, and status predicates are mapped. Unset
  object serialization and some response wire-key mappings await synthetic proof.
- **SL-4: partial.** Booking/ticket bounds, endpoint cache validation/expiry, and
  route invalidation are mapped. Exact retry formulas and all negotiation variants
  remain open.
- **SL-5: partial.** Ordinary recovery gates and cursor reuse, CHANGESVR, KICKOUT,
  and database-reset decisions are mapped. Exact delay constants, reason labels,
  and delivery-gap semantics remain open.
- **SL-6: implemented for the reviewed pure subset.** Typed LOGINLIST validation,
  status classification, endpoint-cache rules, registration HTTP metadata, and a
  recovery reducer have synthetic tests. Network transmission remains deferred
  until serializer tests close the remaining byte-level gaps.
- **SL-7: blocked by the deliberate account-protection cooldown.** No live retry is
  authorized in this phase.

## Target outcome

Specify and test one complete secondary-device session lifecycle:

```text
approved registration
  -> install reviewed authentication state
  -> booking/configuration
  -> ticket/check-in
  -> secure carriage connection
  -> final LOCO login
  -> authenticated session
  -> disconnect classification
  -> reconnect or explicit re-registration requirement
```

No work package may fill a gap from historical implementations without independent
current-client evidence. The disposable account remains unused while its protection
measure is active.

## Evidence levels

- **Observed/static:** complete control/data flow in a versioned official client.
- **Observed/synthetic:** invented data through an isolated serializer or helper.
- **Observed/disposable:** one bounded authorized-account experiment.
- **Public prior art:** a pinned public source used only as a lead until reproduced.
- **Hypothesis:** testable but non-implementable.

## Work packages

### SL-0 — Baseline and clean-room controls

Pin the macOS 26.8.0 artifact and private query locations. Keep raw binary output,
credentials, account/device identifiers, live endpoints, and captures outside Git.

Exit gate: every public claim has a non-sensitive evidence ID and confidence.

### SL-1 — Registration HTTP transport

Recover the shared transport below the seven known registration builders:

- base URL/host selection and version gates;
- HTTP methods and parameter placement/encoding;
- common headers, cookies, and session state;
- signing, hashing, timestamps, nonces, and canonicalization;
- success/error envelope decoding and request-construction failures;
- password-check ownership, QR check-key validation, and passcode statuses.

Exit gate: an invented request can be serialized and parsed without network access,
with every byte or text field supported by reviewed evidence.

### SL-2 — Authentication-state installation

Trace successful permanent and temporary registration results into the auth model:

- returned user identity, access token, auto-login material, and flags;
- local versus server-issued values;
- in-memory versus persistent state;
- storage failure, partial success, rollback, logout, and self-unregister behavior;
- exact values visible to the common session-login coordinator.

Exit gate: a typed handoff distinguishes transient registration state from
sensitive session state and defines cleanup ownership.

### SL-3 — Final LOCO carriage login

Recover the authenticated carriage operation:

- command name, request identifier behavior, body type, and BSON field order/types;
- required and optional user, token, device, client-version, locale, network,
  capability, and background fields;
- transformations applied to authentication material;
- response fields that establish session identity, revisions, and server state;
- numeric status and malformed-response behavior.

Exit gate: a synthetic request/response pair round-trips through an independent Go
codec and matches an invented-value client serializer comparison where safe.

### SL-4 — Endpoint and secure-session lifecycle

Complete the booking -> check-in -> carriage transition:

- address selection, IPv4/IPv6 ordering, secure versus fallback ports;
- endpoint cache keys, expiry, invalidation, and version coupling;
- secure-layer negotiation and unsupported-version behavior;
- connection timeouts, concurrent-attempt suppression, and cancellation;
- which failures restart carriage, check-in, or the complete booking sequence.

Exit gate: the state machine chooses the next safe action for every classified
bootstrap failure.

### SL-5 — Reconnect, kickout, and revocation

Specify:

- clean disconnect, transient network loss, idle timeout, and app wake;
- retry/backoff/failover and stable-session reset behavior;
- access-token expiry, current-device unregister, remote revocation, account policy,
  concurrent-device displacement, and server kickout reasons;
- when cached endpoints or credentials must be discarded;
- when operator approval or re-registration is mandatory.

Exit gate: recoverable reconnect cannot silently become credential guessing, and
terminal security events fail closed.

### SL-6 — Clean-room Go conformance layer

Implement only reviewed behavior:

- registration HTTP codecs when SL-1 passes;
- LOCO login request/response codecs when SL-3 passes;
- a pure bootstrap/login/reconnect reducer;
- typed errors and redacted diagnostics;
- synthetic malformed, partial-read, unknown-status, retry, failover, expiry,
  revocation, and stale-callback tests.

Exit gate: formatting, race tests, vet, lint, vulnerability scan, and secret scan
pass; fixtures contain invented values only.

### SL-7 — Controlled disposable validation

Wait for the account protection measure to plausibly age out. First confirm one
official Mac login and registered-device persistence. Then perform at most one
preplanned validation of each already-specified boundary, with cleanup and private
artifact handling defined in advance.

Exit gate: observed results either validate the specification or produce a bounded
revision; no speculative repeated login attempts occur.

## Decision gate for messaging research

Begin chat-list and message-command work only after a synthetic implementation can
reach the authenticated-session state and model reconnect. Live validation may
remain pending, but final login fields and security boundaries may not be guessed.

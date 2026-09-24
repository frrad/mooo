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

- [ ] Generate a new client-owned device identity and complete QR-based secondary-
      device authorization without importing official Mac state.
- [ ] Persist only authentication state returned to that new identity, with
      redacted diagnostics and secure local storage boundaries.
- [ ] Implement and synthetically verify LOCO framing, BSON, and secure transport.
- [ ] Implement `GETCONF` -> `CHECKIN` -> `LOGINLIST` and complete one bounded
      disposable-account login.
- [ ] Specify and validate read-only inbound text delivery, acknowledgements,
      cursors, deduplication, and reconnect behavior.
- [ ] Specify and validate one explicit outbound text send with safe idempotency
      and no automatic retry after ambiguous delivery.
- [ ] Implement transport, framing, and serialization packages.
- [ ] Implement credential/session storage interfaces with secure defaults.
- [ ] Implement device login and reconnect state machines.
- [ ] Add read-only synchronization, then text receive/send.
- [ ] Verify against the disposable account and add regression tests.

## Phase 4 — Matrix/Beeper bridge

- [ ] Reassess current mautrix-go and Beeper bridge conventions.
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

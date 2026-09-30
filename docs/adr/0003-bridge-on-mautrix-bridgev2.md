# ADR 0003: Build the Matrix bridge on mautrix-go bridgev2

- Status: accepted
- Date: 2026-09-30

## Context

Phase 4 needs a puppeting Matrix bridge that also works with Beeper. The
mautrix-go `bridgev2` framework ("megabridge") is now the base for the
maintained mautrix bridges. It supplies portal and ghost management, message and
reaction ID storage, double puppeting, end-to-bridge encryption, relay mode,
provisioning and login flows, bridge-state reporting, backfill plumbing, and
Beeper compatibility. A network implementation supplies a `NetworkConnector`
plus a per-login `NetworkAPI`.

The Kakao client already defines the invariants a bridge must respect: one
exclusive session owner per profile, no implicit reconnect or mutation retry,
an explicit post-persistence commit boundary, and read side effects from
`SYNCMSG`.

## Decision

Implement the bridge as a `bridgev2` network connector in
`internal/bridge/connector`, with the executable in `cmd/mooo-bridge` using the
framework's standard entry point. The connector depends on `internal/client`;
protocol and client packages never import Matrix or bridge code.

Do not choose a production homeserver target yet. Development uses a disposable
local homeserver. Standard appservice deployment and Beeper self-hosting are
both evaluated at packaging time.

## Consequences

- We inherit framework conventions for configuration, database schema, and
  login UX, and must track mautrix-go releases.
- The framework keeps its own database; the Kakao continuity checkpoint remains
  a separate private file. Delivery between them is at-least-once, and the
  framework's message-ID deduplication absorbs replays.
- Kakao-specific semantics that the framework does not model (aggregate
  reactions, read side effects of history fetches, ambiguous send outcomes)
  must be handled explicitly in the connector.

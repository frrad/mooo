# ADR 0001: Begin with a modular Go monorepo

- Status: accepted
- Date: 2026-09-20

## Context

The immediate work is secondary-device protocol research, but the intended product
is a self-hosted Matrix/Beeper bridge. The implementation must not bake in one
operator's account or deployment.

## Decision

Use Go and a single repository. Keep the Kakao protocol core independent of Matrix,
storage, and executable concerns. Introduce stable public packages only after the
protocol interfaces have evidence behind them.

## Consequences

Go aligns with the Matrix bridge ecosystem and Linux deployment target. A monorepo
keeps early research and rapidly changing interfaces coherent. The separation may
later permit extracting the protocol client without requiring it today.

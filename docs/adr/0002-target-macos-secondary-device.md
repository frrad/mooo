# ADR 0002: Target the macOS secondary-device flow

- Status: accepted
- Date: 2026-09-20

## Context

The operator must keep using KakaoTalk on the primary Android phone while also
using the account through a future Beeper bridge. Public prior art indicates that a
direct Android protocol login can replace the primary phone session, while official
desktop clients use a concurrent secondary-device flow.

## Decision

Model the bridge client after the official macOS secondary-device behavior. Use an
official Android client and disposable account to create, approve, inspect, and
revoke test secondary-device sessions. Other clients remain comparison targets.

## Consequences

The immediate research centers on Mac login, approval, session persistence, and
LOCO transport. Android analysis is still important because the primary device owns
approval UI and may contain cross-platform authentication logic. Experiments must
be conservative because registration attempts can affect sub-device access.

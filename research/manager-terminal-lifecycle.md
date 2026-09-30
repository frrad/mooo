# Manager-owned CHANGESVR and KICKOUT lifecycle

Status: first-party static trace, macOS KakaoTalk 26.8.0, reviewed
2026-09-30.

This dossier follows the official unsolicited-event chain beyond the generic
carriage agent. It is implementation-neutral and publishes no proprietary
source, offsets, account values, or raw captures.

## Event dispatch and models

The LOCO default receive dispatcher maps the `CHANGESVR` and `KICKOUT` methods
to their respective push-notice response models. The model handlers verify
that the manager delegate responds, then forward the notice to the manager.
The clean-room event stream currently exposes both as `UnknownPacket`; it does
not silently treat either event as a normal message or a successful request
response.

`CHANGESVR` reaches the manager as a typed push notice, but the traced manager
path does not consume a notice payload field. `KICKOUT` reaches the manager as
a typed push notice whose user-info includes an integer reason code under the
official kickout-reason key. The manager's downstream logout path, rather than
the generic carriage status callback, owns interpretation of that reason.

## CHANGESVR path

The manager delegates the change-server notice to its change-server logout
operation. That operation clears the cached carriage route, advances the
ticket-address cursor when another candidate exists, and calls the ordinary
logout path. This is a terminal route-change decision followed by a fresh
manager-controlled login opportunity; it is not an automatic request retry.

The event handler does not persist a message/application mutation or issue a
replacement carriage request from inside the carriage callback. A stale or
duplicate manager callback must not be allowed to resurrect the previous
route; the pure reducer represents this as a terminal `ChangeServer` action
with route-clear and logout effects.

## KICKOUT path and guards

The manager's kickout consumer first checks that the session is logged in and
not already logging out. Only then does it derive the reset choice from the
reason-bearing user-info and invoke logout-with-reset. Reasons 1 and 10 are
the reviewed reset-database cases; the reason labels themselves remain
uninterpreted.

This guard is materially different from ordinary carriage disconnect. A
socket failure fails pending callbacks and publishes transport status; a
guarded `KICKOUT` is a manager-owned terminal logout decision. Neither path
implicitly retries an ambiguous mutation. The reducer already models reason
1/10 reset effects, generation checks, terminal-state rejection, and stale
callbacks, but currently applies `KICKOUT` even when `Authenticated` is false
and has no explicit `isLoggingOut` state.

## Persistence, checkpoint, and reconnect ownership

The reviewed manager path owns route-cache clearing and logout/reset calls.
The carriage agent has no durable event state and does not schedule reconnect.
The clean-room reducer keeps CHANGESVR and KICKOUT terminal and emits
transport/UI/storage-neutral effects; the outer client must execute those
effects and decide whether a new login is appropriate. Existing client event
dispatch does not yet connect the unknown push packets to this reducer.

## Synthetic handoff

`internal/protocol/sessionlogin/terminal_guard_test.go` encodes the narrow
proven guard: a KICKOUT received outside an authenticated session must not
change recovery state or emit logout/reset effects. It intentionally fails
against the current reducer because that reducer applies KICKOUT without the
official login guard. Existing tests continue to cover reason 1/10 reset
mapping, CHANGESVR route-clear/logout effects, generation staleness, and
terminal duplicate rejection.

## Provenance and confidence

- Source: authorized official KakaoTalk Mac binary, version 26.8.0, analyzed
  read-only in the maintained Ghidra project.
- Method: exact selector lookup and decompilation of default push dispatch,
  CHANGESVR/KICKOUT handlers, manager delegate blocks, change-server logout,
  kickout login/logout guards, and logout-with-reset selection.
- Confidence: high for event delegation, CHANGESVR route/logout ownership,
  KICKOUT login/logout guards, and reset reason values 1/10; medium for the
  complete set of user-info keys and any recovery observer outside the traced
  manager path.

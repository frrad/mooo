# Shared carriage callback lifecycle

Status: first-party static trace, macOS KakaoTalk 26.8.0, reviewed
2026-09-30.

This note records the generic carriage-agent lifecycle behind request
completion. It is separate from any one command, including `NOTIREAD`.
Only implementation-neutral behavior is published; private decompiler output,
addresses, account values, and proprietary assets remain in the authorized
external lab.

## Registration and normal completion

The carriage agent initializes two in-memory pending maps and a packet-ID
counter. A request accepted while the agent is in its send-ready state obtains
one packet ID, constructs its packet, derives a transport tag, stores the
completion under the request's correlation identities, sends the packet, and
arms the receive-header timeout. The traced path contains no request retry.

When a matching response arrives, the pending callback is looked up, removed,
and invoked with the response packet and a nil error. An unmatched packet is
handled as an unsolicited packet by the default receive path. No durable
callback or request record is created by this agent.

The packet-ID allocator starts at `100000000`, returns the current value, and
advances while the next value remains below `200000000`. At the upper boundary
it resets the next value to `100000000`; it does not emit `200000000`. This
bounded reuse is safe only after the old pending entry has been removed or
failed, so implementations must not reuse an ID while its callback remains
live.

## Failure fan-out and teardown

The agent has an explicit pending-failure operation. It enumerates the pending
callback map, invokes each stored completion with no response and the supplied
error, then clears both pending maps. The socket-disconnect callback performs
the same fan-out and map clearing after publishing a status/error transition.
Thus socket close, receive timeout, and explicit disconnect are ambiguous
request failures, not successful responses; each pending completion is
terminally failed once and no retry is initiated by the carriage agent.

The receive-header timeout schedules agent disconnect. Explicit disconnect is
queued onto the carriage socket queue and asks the socket to disconnect; the
pending error fan-out is reached through the socket-disconnect callback. The
static trace does not show the status-change callback scheduling a reconnect.
Reconnect/recovery therefore belongs to the owning manager, not to the
generic carriage callback layer. The pending maps and packet counter are
in-memory state and are discarded with the agent.

## Server-directed lifecycle events

`CHANGESVR` is first delegated from the packet handler to the manager. The
manager's change-server logout path clears the cached carriage route, advances
the ticket-address cursor when another address is available, and calls the
normal logout path. `KICKOUT` is likewise delegated to the manager; when the
session is logged in and not already logging out, the manager derives its
logout/reset choice from the event information and calls the logout-with-reset
operation. The carriage agent itself does not choose a reconnect or persist
either event.

These event paths are distinct from an ordinary socket failure. The former
are manager-owned terminal/recovery decisions; the latter only fail pending
callbacks and publish transport status. The clean-room client must preserve
that separation and must not retry an ambiguous mutation merely because a
later manager recovery establishes a new carriage.

## Synthetic conformance coverage

`internal/client/session_lifecycle_test.go` covers the generic consequences
without reproducing proprietary implementation details:

- reader teardown fails every pending waiter once, clears pending state, and
  closes the unsolicited stream;
- explicit session close reaches the same pending-request failure boundary;
- packet-ID wrap and collision avoidance pass at the official upper boundary;
  the NOTIREAD concrete disconnect path is covered by
  `TestNotiReadDisconnectFailsOnceWithoutReplay`.

These tests pass against the current client and preserve the one-shot failure
and bounded-ID behavior established by the shared carriage evidence.

## Provenance and confidence

- Source: authorized official KakaoTalk Mac binary, version 26.8.0, analyzed
  read-only in the maintained Ghidra project.
- Method: exact selector lookup and decompilation of request registration,
  response dispatch, packet-ID allocation, socket connect/disconnect callbacks,
  pending-failure fan-out, timeout/disconnect, manager CHANGESVR/KICKOUT
  delegation, and logout paths.
- Confidence: high for pending-map registration/removal/clearing, error
  fan-out, packet-ID range/wrap, and manager ownership boundaries; medium for
  the exact distinction between the two internal correlation-map keys; open
  for any reconnect policy outside the traced manager callbacks.

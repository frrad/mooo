# Client architecture

Status: implementation direction; protocol details remain evidence-driven.

## Layers

1. `internal/protocol/*` contains transport-independent request/response models,
   exact field encoders, parsers, and state machines. The generic `CREATE`
   primitive belongs here; one member denotes a direct chat and multiple members
   denote a group.
2. `internal/client/Session` owns one authenticated LOCO carriage, packet IDs,
   response correlation, login synchronization, and unsolicited packets. It is a
   mechanism owned by the higher-level client, not the normal application entry
   point.
3. `internal/client/Client` owns loaded client state, authenticated HTTP behavior,
   and one cached `Session`. Operations use the session owned by that client.
4. The long-running Matrix/Beeper bridge is the global owner for each configured
   Kakao identity. It holds an exclusive per-profile lease and owns exactly one
   `Client`. Matrix is the application interface; no additional local daemon or
   IPC layer is required. A one-process-per-command CLI cannot provide
   persistent-session semantics and is limited to offline research and
   administration while the bridge is stopped.

Once the API is stable enough for downstream use, the application-facing layer
can move from `internal/client` to a public package without exposing wire types.

## Session lifecycle invariants

- Repeated `Client.Connect` calls after success are idempotent.
- Initial establishment may admit one access-token renewal under the profile
  lease, atomically rotate the persisted token triple, and make one fresh
  LOGINLIST attempt. Renewal never loops and is not a general request retry.
- Chat operations use the session already owned by `Client`; they do not invoke a
  free-standing login function.
- `LOGINLIST` is accepted from its BSON `status`, not the LOCO frame header.
- All required `LCHATLIST` pages are consumed before the session becomes usable.
- No operation transparently reconnects. A disconnect during a mutation is
  ambiguous, so reconnect and reconciliation must be an explicit higher-level
  decision.
- `Client.Close` is terminal. Creating another client/session is deliberate.
- Only the process holding the profile's exclusive lease may connect. This
  prevents two CLI invocations, bridge processes, or probes from independently
  logging in with the same secondary-device identity.
- Credentials, device identifiers, endpoints, server IDs, message contents, and
  raw responses never appear in default diagnostics.

## Durable continuity state

A live socket is an operating-system resource and cannot be serialized to disk.
The bridge keeps it open for normal operation. Its private, versioned checkpoint
stores everything needed to resume after an unavoidable process or machine
restart:

- the existing client-owned device identity and credentials;
- last global token/cursor and per-chat maximum log IDs;
- synchronized chat identifiers needed by `LOGINLIST`;
- a bounded, expiring route cache;
- server-directed reconnect/backoff state, including the last permitted attempt;
- clean versus interrupted shutdown state; and
- the last fully committed inbound/outbound reconciliation markers.

The checkpoint lives beside the private auth state with owner-only permissions,
atomic replacement, exact schema-version checks, and redacted diagnostics. It is
never committed. A restart necessarily opens a new network socket and performs
one resumed login; it must not redo QR authorization or start an unbounded login
loop.

The bridge exposes chat, send, sync, and event behavior through Matrix/Beeper.
Separate local IPC is unnecessary unless a concrete operational need appears.
Research or repair commands must respect the same profile lease and cannot touch
Kakao transport while the bridge owns the profile.

## Event direction

The current session preserves unsolicited packets encountered while correlating
a response, but it does not yet run a continuous receive loop while idle. Before
text receive/send is promoted from the lab, `Session` must gain one reader pump
that exclusively reads the carriage and dispatches packets to:

- a pending-request map keyed by packet ID;
- a bounded application event stream for `MSG` and other pushes; and
- terminal session-state handling for disconnect, `CHANGESVR`, and `KICKOUT`.

This keeps one socket and one reader per identity while allowing multiple
high-level operations to share the authenticated session safely.

# Membership and chat-change inventory

Status: bounded first-party inventory and negative evidence, 2026-09-30.
No membership production implementation is authorized by this note.

## Inventory

The official macOS client contains unsolicited push-notice models for member
and chat changes, including `DELMEM`, `NEWMEM`, `LEFT`, and chat-status/meta
changes. It also contains request/response paths for operations such as
`ADDMEM`, `GETMEM`, and `CHECKJOIN`. Presence in the client is not evidence
that a command's full parity contract is understood.

## Highest-value candidate: DELMEM

`DELMEM` has a dedicated unsolicited model with one model-owned field,
`chatLog`, whose type is a chat-log model. The chat-log metadata exposes the
following implementation-neutral fields: int64 chat ID and log ID, int32 raw
type, and optional message, attachment, and supplement strings. The handler
passes the notice to a manager delegate before any state consumer. The traced
manager callback schedules database-context work rather than performing a
network retry or an automatic reconnect.

Static evidence also shows a membership-oriented database consumer that builds
sets of user IDs, subtracts invitee IDs, compares the current user's ID and
member type, and removes a member by user ID. A separate omitted-history repair
path looks up a room by chat ID, enumerates stored members, and calls the
member-removal operation for selected IDs. These observations establish that
membership persistence exists, but they do not prove which user-ID source in
the database block corresponds to each `DELMEM` wire field, nor whether the
same consumer is used for every direct-chat and group-chat shape.

## Explicit gaps

The following layers remain unproven for `DELMEM`, so this slice does not add a
typed event or member mutation:

- exact BSON nesting and optional/null rules for the `chatLog` body;
- the mapping from a deletion notice to the departed member ID(s);
- status/error interpretation and database transaction failure behavior;
- direct-chat versus group-chat guards and local event/UI consumers; and
- malformed-body behavior of the official model initializer.

The clean-room decoder therefore intentionally keeps `DELMEM`, `NEWMEM`,
`LEFT`, `CHGCHATST`, `CHGMETA`, and `CHGMCMETA` observable as `UnknownPacket`.
The synthetic characterization test protects that boundary until a complete
seven-layer dossier is available. This is a negative-evidence contract, not a
claim that the official client ignores those methods.

## Evidence trail

- Client: official macOS KakaoTalk 26.8.0 arm64, inspected 2026-09-30.
- Method: read-only Ghidra model metadata, selector/caller reports, and
  decompilation of the DELMEM packet handler and database callback; no live
  membership mutation was performed.
- Public transfer: only field names/types, ownership boundaries, and synthetic
  unknown-method behavior are recorded here. Private binary names, offsets,
  decompiler output, accounts, and message data are omitted.

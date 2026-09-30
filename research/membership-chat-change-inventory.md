# Membership and chat-change inventory

Status: first-party decoder identity contract plus bounded lifecycle evidence,
2026-09-30. This note authorizes only the typed DELMEM decoder described below;
it forbids member persistence mutation, UI effects, retries, and reconnects.

## Inventory

The official macOS client contains unsolicited push-notice models for member
and chat changes, including `DELMEM`, `NEWMEM`, `LEFT`, and chat-status/meta
changes. It also contains request/response paths for operations such as
`ADDMEM`, `GETMEM`, and `CHECKJOIN`. Presence in the client is not evidence
that a command's full parity contract is understood.

## Highest-value candidate: DELMEM

`DELMEM` has a dedicated unsolicited model with one model-owned field,
`chatLog`, whose type is a chat-log model. The relevant nested model metadata is
implementation-neutral but exact in type: `chatLog.chatId` and `chatLog.logId`
are signed int64 values; its feed contains a `leaver` member; and that member
contains signed int64 `userId`, signed int32 `userType`, and an optional string
nickname. The proven identity path for a deletion event is therefore
`chatLog.feed.leaver.userId`, paired with the chat and log IDs. Other chat-log
fields include a signed int32 raw/type value and optional message,
attachment, supplement, and extra data, but they are not needed to identify
the departed member.

The handler passes the notice to a manager delegate before any state consumer.
The traced manager callback schedules database-context work rather than
performing a network retry or an automatic reconnect.

Static evidence also shows a membership-oriented database consumer that builds
sets of user IDs, subtracts invitee IDs, compares the current user's ID and
member type, and removes a member by user ID. A separate omitted-history repair
path looks up a room by chat ID, enumerates stored members, and calls the
member-removal operation for selected IDs. These observations establish that
membership persistence exists, and the decoder identity path now matches the
member-removal input. The same evidence does not prove that the consumer is
used identically for every direct-chat, group-chat, or open-chat shape.

## Explicit gaps

The following layers remain unproven for `DELMEM`; this slice adds only a
typed decoder identity contract and no member mutation:

- optional/null behavior when `chatLog`, `feed`, or `leaver` is absent;
- status/error interpretation and database transaction failure behavior;
- direct-chat versus group-chat guards and local event/UI consumers; and
- malformed-body behavior of the official model initializer.

The clean-room decoder therefore intentionally keeps `NEWMEM`, `LEFT`,
`CHGCHATST`, `CHGMETA`, and `CHGMCMETA` observable as `UnknownPacket`.
`DELMEM` has a synthetic decoder test for the proven identity path, while the
stateful lifecycle remains unimplemented. The decoder fails closed for missing
or wrong nested structure as an implementation safety rule; that behavior is
not claimed as an observation of the official malformed-body path. This is not evidence that the
official client ignores any of these methods.

## Evidence trail

- Client: official macOS KakaoTalk 26.8.0 arm64, inspected 2026-09-30.
- Method: read-only Ghidra model metadata, selector/caller reports, and
  decompilation of the DELMEM packet handler and database callback; no live
  membership mutation was performed.
- Public transfer: only field names/types, ownership boundaries, and synthetic
  decoder identity behavior are recorded here. Private binary names, offsets,
  decompiler output, accounts, and message data are omitted.

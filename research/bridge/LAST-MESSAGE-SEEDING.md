# Fresh-login last-message seeding

Status date: 2026-10-13. Implemented with synthetic production tests; no
owned-account live acceptance yet.

## Problem

After a fresh login, Beeper showed no chat history. Regular groups got empty
rooms from discovery, direct chats got no room until a new message arrived,
and catch-up deliberately excludes chats this profile has never delivered.
Historical import exists only as the explicit `history-group` command.

## Official-client reference

Static Mac 26.8.0 tracing (SL-BIN-011, SL-BIN-014 and SL-BIN-015 in
[session-login evidence](../session-login/EVIDENCE.md)) shows that LOGINLIST
processing stores each chat's last chat log. It then creates a loss mark at
`lastChatLog.prevId + 1` instead of fetching older history. Later `SYNCMSG`
callbacks resolve loss marks. This trace does not establish what triggers the
fill, how the UI presents it, or how much history the server retains for a new
subdevice. It is a lead, not a parity claim.

## Behavior

At bootstrap, after group discovery and before catch-up and live subscription,
each direct chat and regular group (no link ID, not hidden by the account's
`chat_hide` room meta) that this profile has neither committed nor admitted
live gets its inventory `l` chat log delivered and committed. No request is
made for this:

- The message is decoded from the completed LOGINLIST inventory.
- A direct room that does not exist yet is created from the inventory's single
  display member (ID, nickname and, when present, image URL) rather than from
  CHATINFO, MEMLIST and MEMBER. The two participants are the complete roster.
  A nickname or image the inventory omits is left unchanged rather than
  cleared. Later chat-info refreshes remain source-backed.
- Regular groups already have rooms from discovery, which does request
  metadata, as it did before this change.
- When the inventory's unread count (`n`) is zero, the bridge marks the seeded
  message read for the account's own Matrix user.

Only events that become room messages are seeded. A membership or metadata
feed as the last log describes an earlier state and is not applied as a
current change. A chat whose last log cannot be decoded is skipped and logged.
Committing the seeded message makes later reconnects catch up from it. If its
`ll` is beyond `l`, the first catch-up requests that interval, as for any
committed chat. An undelivered seed ends the connection, as catch-up does,
because its queued commit must precede the chat's live commits.

## Open questions

- Owned-account acceptance on a fresh profile with existing direct and group
  chats.
- Whether the server retains history before a new subdevice's login, and how
  a zero-held `SYNCMSG` without a known start paginates. Both bound any later
  automatic backfill.
- Whether `n` always reflects the account's unread state.
- Read-watermark recovery requests CHATONROOM for every portal at each
  bootstrap, so more direct rooms mean more requests per reconnect.

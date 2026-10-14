# Reaction metadata sync eligibility and scheduling

Evidence date: 2026-10-13. Related report: [issue #355](https://github.com/frrad/mooo/issues/355).
Method: read-only Ghidra inspection of the authorized KakaoTalk macOS 26.8.0
arm64 binary, including ARM64 disassembly where decompilation lost a return
value. No app launch, runtime instrumentation, account access, traffic capture,
or public prior art. These are static findings, not executed parity fixtures.

## Room eligibility

The metadata coordinator checks `NTChatRoom.isChatlogMetaAvailable` before
creating an in-flight sync or building an HTTP request. The predicate is true
only for room enum values 0, 1, 3 and 4. The type-string converter and named
`isPlusChat`, `isPartnerChat` and `isDirectChat` predicates establish the mapping:

| Raw room type | Mac enum | Metadata sync eligible |
| --- | ---: | --- |
| `DirectChat` | 0 | yes |
| `MultiChat` | 1 | yes |
| `PlusChat` | 2 | no |
| `OD` | 3 | yes |
| `OM` | 4 | yes |
| `MemoChat` | 5 | no |
| `PartnerChat` | 6 | no |

The converter defaults an unrecognized string to enum 1. That fallback is an
official-client observation, not a recommendation to treat unknown bridge room
types as eligible. Message type 71 is not an input to this room predicate.

The decompiler represented this boolean method as returning void and omitted
the final comparison. Disassembly confirms explicit true results for 0/1/3
and an equality test against 4 for the remaining path. The omission must not be
interpreted as allowing every room type.

A second gate is independent of the room type. `LocoChatOnRoomResponse` has a
boolean `stopSyncLogMeta` property. The chat-on completion copies it into
`FCChattingController.stopSyncLogMeta`. The controller checks that property
before both metadata-sync dispatch sites. The response's constant name-mapping
dictionary maps wire key `dlm` to `stopSyncLogMeta`; its initializer delegates
ordinary mapped fields to the superclass. Default-on-absence, malformed-value
handling and superclass decoder failure behavior remain untraced.

## Triggers and request volume

The recovered dispatch chain belongs to a chat controller, not a login-time
loop over the full chat inventory:

1. `didChatOn` starts `startSyncMessage`. This installs one repeating `NSTimer`
   with a 10-second interval targeting `syncMessage:`; an existing timer is
   retained rather than creating a duplicate. `chatOff` calls `stopSyncMessage`,
   which invalidates and clears the timer.
2. `syncMessage:` checks source connection state, ongoing work and the current
   room. If ordinary message sync is unnecessary, the first sync cycle calls
   metadata sync with `stopIfSyncedRevision=false`, unless the stop flag is set.
3. When ordinary message sync is needed, its successful completion checks room
   status 1 and the stop flag, then invokes metadata sync. The first cycle uses
   `stopIfSyncedRevision=false`; subsequent cycles use true.
4. With `stopIfSyncedRevision=true`, the coordinator can return without HTTP
   when the stored sync revision and latest metadata revision agree. An initial
   zero cursor with a positive oldest local log ID can still require a fetch.
   No oldest message, an ineligible room, or disconnected source also avoids
   the request. An in-flight entry keyed by room identity coalesces repeat sync
   starts.

Thus a timer tick is not necessarily a metadata HTTP request, and the Mac does
not use mooo's all-portals reconnect sweep in this recovered chain. This does
not prove there are no other indirect dispatch paths in any client version.

## Request, response, persistence and consumers

The full coordinator and callback reports support the existing
[group reaction contract](GROUP-REACTIONS.md):

- The request is `GET /messaging/chats/{chatId}/chat-log/meta/sync-meta` with
  `cur`, `max`, and `cnt`. A zero sync cursor starts at the oldest local log ID.
  The latest stored metadata revision and a local metadata query determine
  `max` and `cnt`; the query caps the local count at 300. This is not proof of
  a server page-size limit.
- The response dictionary supplies `content` metadata rows and `last`. Rows
  carry chat ID, log ID, link ID, type, revision, content and extra. The success
  path applies newer revisions through the database block, raises stored
  metadata/sync revisions and notifies the chat consumer of changed rows.
  Mini rows can initiate a separate visible-message extras lookup.
- Paging can continue when `last` is false or absent. Previous response state
  and parameter/progress checks guard repeated continuation. The Mac has no
  recovered equivalent of mooo's ten-page-per-connection budget.
- The HTTP failure callback clears in-flight coordination state. It does not
  retry that failed HTTP request or advance the sync cursor. This is not a
  permanent disable: a later eligible trigger can request sync again.

Remaining chain gaps include response defaults/decoding failures for the stop
flag, database error propagation and durable transaction guarantees, every UI
consumer after update notification, and exhaustive indirect reconnect/window
restoration callers. No full runtime parity claim follows from this dossier.

## Consequences for mooo

At baseline `ad0258e5`, `resyncReactions` enumerates all portals with Matrix room
IDs for the login, checks source access, and sequentially syncs each one with a
usable cursor or first bridged message. It does not check room type, the
chat-entry stop flag, viewing state or revision equality. Every connection can
therefore make a request even for an unchanged or unsupported room.

For N eligible portals with usable cursors, the normal empty-page cost is N
HTTP requests per connection; the configured bound is up to 10N page requests,
plus reaction member/detail lookups for returned bridged targets. Each page has
a 30-second timeout. These are code-derived bounds, not measured latency or
backend cost. Empty portals can skip requests. The connector retains a failed
cursor and tries again on the next connection.

Static exclusion of `PlusChat` gives a concrete explanation for #355 if the
reported portal has that raw room type. The report does not establish the
portal's actual type or the server's reason for HTTP 400. A safe implementation
should consume authoritative room metadata, mirror the eligible type set, and
cover exclusions with a production-path regression before changing behavior.
Do not classify rooms from message type 71 or permanently disable them after
an arbitrary HTTP 400.

The larger scheduling policy remains a bridge decision: Matrix clients may not
expose the Mac's window-open lifecycle. Retaining eager offline recovery would
need explicit pacing, coalescing and account-wide budgets, while a demand-based
policy needs reliable room-view/history hooks and a documented recovery delay.
Neither policy should be described as exact Mac scheduling parity. This
research changes no production behavior and leaves #355 open.

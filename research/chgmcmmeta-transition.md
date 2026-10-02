# CHGMCMETA room transition contract

Status: implementation-neutral handoff, 2026-10-01.

This contract covers the stateful boundary after a decoded `CHGMCMETA` notice
has reached the room manager. The reviewed push-notice consumer compares the
wire `type` against the exact labels `name`, `favorite`, `imagePath`,
`chat_hide`, and `chat_category`; other labels are ignored.

## Inputs and state

The reducer receives a room snapshot and a decoded notice:

- `RoomExists` gates the entire transition. A missing room produces an
  unchanged state and no effects.
- The notice carries the decoder-proven `chatId`, signed 32-bit `revision`,
  opaque `type`, and the `content`, `imageUrl`, and `fullImageUrl` strings.
- The route is selected directly from the exact wire labels above. `favorite`
  and `chat_hide` interpret `content` as true only when it equals the exact
  string `true`; other content values select false.
- Room state contains name, favorite, image URLs, hidden flag, category, and
  pin value. The MCM revision is owned by the shared chat context, not by the
  room row; the reducer therefore receives and returns a `GlobalRevision`.
  The reducer returns an explicit `UnpinInAllFolders` effect. This names the
  observed operation without claiming that folder membership is deleted.

## Transition rules

For an existing room, apply the route’s field mutation from the notice before
the revision gate:

- `name` replaces the room name with `content`;
- `favorite` replaces the favorite flag using the `true` string predicate;
- `imagePath` replaces both image URL fields with `imageUrl` and
  `fullImageUrl` (including empty strings);
- `chat_hide` replaces the hidden flag using the `true` string predicate;
- `chat_category` replaces the category with `content`;
- any other type leaves all routed fields unchanged.

Then compare the incoming revision with the shared chat-context MCM revision.
A strictly higher revision replaces the global revision. Equal or lower
revisions do not change it. This revision decision does not undo a routed field
mutation: the observed callback invokes the field consumer before comparing and
advancing the separate global revision.

After routing, if the room is hidden, set the room pin to `-1` and emit `Unpin`
and `UnpinInAllFolders` effects. The latter targets the room identity; it does
not clear folder membership. The contract intentionally does not infer whether
hidden was caused by the current notice or was already present.

`Applied`, copied state ownership, and atomic commit are implementation-facing
decisions for the clean-room reducer, not claims about official return values.
The proposed reducer reports `Applied` when either a known route selected a
field assignment (even if the value is unchanged), the incoming revision
advanced, or hidden cleanup effects were selected.
Thus an existing hidden room can be `Applied` even for an unknown route with a
non-newer revision, because pin cleanup and both unpin effects are mutations.
Missing rooms remain no-op transitions. A caller should commit the returned
room/global state and effects together, but the official transaction boundary
is untraced.

## Failure and ordering boundary

The manager queues database-context work and does not expose a proven explicit
write-failure callback for this notice. Persistence, pin/folder writes, and
downstream notifications therefore remain outside the pure reducer. A caller
must commit the returned state and effects atomically or surface an explicit
failure; it must not report success merely because decoding succeeded.

No chat-type guard, persistence failure report, or completion/notification
policy is asserted here. The completion block is only room/hidden-gated in the
reviewed trace; its downstream consumer remains untraced.

## Provenance

Transfer review: SL-BIN-036, binary-analysis, 2026-10-01. Reviewed by the
reverse-contracts agent. Private Ghidra output and binary identifiers remain in
the owner-only lab and are not required to implement this contract.

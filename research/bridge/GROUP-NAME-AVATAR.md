# Regular-group name and avatar mapping

Evidence dates: 2026-10-09–10. The regular-group display projection has
executed official Mac 26.8.0 arm64 evidence and owned Android 26.8.2/Matrix
acceptance. Complete native transaction notifications, rollback and every
shared-metadata mutation remain gaps; no end-to-end parity claim is made.

## Official display selection

Independent class and method tracing identifies the regular room's personal
`chatName`, `imageUrl` and `fullImageUrl`, plus shared metadata type 2 (older
Kakao group information), type 3 (group nickname) and type 4 (group profile).
The corresponding getters feed `updateDisplayTitle`, `updateDisplayImageUrl`
and `updateDisplayFullImageUrl`.

A nonempty personal value wins for its respective display field. Without a
personal name, a regular room with multiple active members compares type 2
and type 3 revisions; the higher revision wins, and ties select type 3. Images
compare type 2 and type 4 revisions in the same way; ties select type 4. A
selected newer empty value clears the projected field rather than reviving an
older shared value. Thumbnail and full-size URLs are distinct fields. Common
native title fallback and single-member localization are outside this executed
subset.

The isolated signed lab copy has a distinct bundle identity, no official app
group and no network entitlement. The harness constructs only synthetic room
and metadata objects and executes the official display methods on the main
queue. It checks the official client's preference-file hashes before and after
and kills the isolated process. The five executed synthetic cases are in
[display.json](../fixtures/group-metadata/display.json). They contain no account
state, real URLs or proprietary implementation.

Private first-party reports: group-name-avatar-contract,
group-name-avatar-models, group-name-avatar-display and group-name-avatar-title.
The complete metadata update/persistence callback chain is being traced in
addition to these display consumers. Neither a request constructor nor the
executed getters establish lifecycle parity.

## Bridge mapping and failure handling

The bridge projection compares shared types 2/3/4 and applies
nonempty personal values first. Regular room metadata consumes that projection;
known empty avatar values request explicit Matrix removal. A clear-value
regression failed before this fix. The baseline connector also failed regressions
for a newer type-2 name and a newer shared avatar clear; the updated consuming
path passes them. Unknown avatar data still preserves the existing Matrix
avatar. Names fall back to supplied display nicknames when the selected display
projection is empty. Matrix receives the thumbnail URL, falling back to the
full-size URL when the thumbnail is empty; this is a bridge mapping decision.

A separate durable database checkpoint retains shared types 2/3/4 and admits
only strictly newer revisions for each stored type. Database errors and failed
readback prevent publication of the new projection. Regression tests exercise
restart with an older snapshot, failed writes, corrupt readback, contradictory
equal-revision inputs, oversized writes that would poison a later bounded read,
and preservation of independent portal access state.
Personal name and image notices trigger a fresh room snapshot instead of
applying the potentially delayed notice body. An actual framework-consumer test
injects failed Matrix name and avatar-clear writes, verifies their applied flags
remain false, and confirms a subsequent fresh resync retries both. A second framework test injects download and Matrix upload failures after the
real source projection: both keep previous media and leave the applied flag
false; a subsequent resync with the same source avatar succeeds, and replay
does not upload again. Concurrent framework application ordering outside the
normal serialized source event loop remains an explicit limitation.

The owned acceptance below covers live/offline personal names, shared-name
visibility when an override clears, avatar replacement/removal, encrypted
bidirectional traffic and restart deduplication. Shared editable metadata
permissions are not inferred from a request constructor.


## Shared profile content and update gate

Class metadata identifies `LocoChatMetaGroupProfileImageUrlInfo.imageUrl` and
`fullImageUrl`. Executed canonical synthetic inputs confirm these JSON keys,
empty-string clears, missing fields returning nil, an empty object yielding a
profile with nil fields, and empty content yielding no profile model. These
cases are in [profile-content.json](../fixtures/group-metadata/profile-content.json).
The bridge projects nil or missing URLs to empty display URLs once that shared
metadata type is known. It bounds JSON input and rejects duplicate keys, wrong
field types and conflicting equal revisions as explicit admission decisions;
complete official malformed-input permissiveness is not claimed.

Exact instructions for `updateWithChatMeta:` independently verify replacement
only when the incoming per-type revision is strictly newer than the existing
record; an absent record can be created. The metadata factory copies the
revision and maps type 2 to the older group-name/profile fields, type 3 to the
nickname, and type 4 to profile thumbnail/full URLs. The full refresh response,
persistent transaction/notification effects and failure chain are still being
traced. The neighboring-function decompiler output was not used as proof of
this revision gate.

The dirty-metadata path constructs a `LocoGetMetaRequest` with the chat ID and
an `SGInt32Array` of requested types. `LocoGetMetaResponse` exposes the chat ID
and `LocoChatMetas`. The request sender preserves a nil-packet failure; the
coordinator distinguishes successful responses before delivering completion.
The dirty callback skips database work for unsuccessful responses. Successful
responses enter a synchronous database block which first merges notice metadata
and then applies each returned metadata item through `updateWithChatMeta:`.
Additional type-specific downstream effects and the transaction's notification
and rollback behavior remain gaps. These are static first-party observations,
not executed response-lifecycle parity fixtures.


## Personal metadata read contract and owned omission regression

The Mac personal settings path uses `SETMCMETA`, independently of shared
`SETMETA`. Its name/image completion checks success before database work, then
updates the selected room's personal fields and advances the global personal
metadata revision only when newer. The push database block likewise mutates the
room before its global revision check. The global revision is not a per-room
notice freshness gate.

Owned B changed its personal room name using Android 26.8.2. The secondary
received `CHGMCMETA`, while a subsequent successful `CHATINFO` omitted `m`.
This caused the original refresh implementation to overwrite a personal name
with the shared title. A production-consumer regression reproduced that failure.

The official personal refresh sends `GETMCMETA` with an empty document. Its
response pairs `chatIds` and `metas` by index. The successful coordinator enters
a database block, enumerates those pairs and calls `updateWithMCMeta:` on known
rooms. Missing rooms are not updated. A nil packet or unsuccessful response
skips these updates. Executed fixtures verify empty request serialization and
both object and JSON-string metadata entries; owned B observed JSON strings.
See [personal-read.json](../fixtures/group-metadata/personal-read.json).

The bridge now reads this separate source when regular-room `CHATINFO` omits
personal fields. Unknown selected-room personal metadata cannot confirm a
clear in a personal-notice refresh. The decoder bounds input and rejects
mismatched arrays, duplicate IDs, invalid IDs and incompatible item types as
bridge admission rules. It makes no automatic retry or source mutation.
[personal-display.json](../fixtures/group-metadata/personal-display.json)
contains executed personal-field and display-getter results, including clears.
Its tests exercise the snapshot display boundary rather than modeling the
native notice handler.

Owned acceptance so far: exactly one selected encrypted portal converged to
B's personal name after an offline change and after a second live change,
without an ordinary message triggering discovery. A and C retained the shared
name and three-member roster. Android's personal-name editor disabled saving a
blank name; that attempted UI clear made no source change. A separately guarded,
selected-room lab action sent one traced `SETMCMETA` empty-name request while the
bridge was offline. The next normal startup restored the shared name, preserved
the personal avatar and retained all eleven existing message mappings.

A synthetic personal avatar set live reached Matrix. A second distinct synthetic
avatar saved offline replaced it on normal startup, preserving the shared name
and all existing message mappings. The Matrix media bytes matched the source
download hash, and both checker images were visually verified. The live native
“Use Participant Image” save then removed the Matrix avatar while preserving the
shared name and existing message mappings. Shared metadata changes, exact
encrypted traffic after these changes,
committed restart, owned outbound rejection and final validation/CI remain
outstanding. Actual framework dispatch tests already confirm explicit name and
avatar rejection without changing source or portal metadata.

Owned Android settings caveat: after the personal-name clear, the avatar editor
showed the shared title in its name field. Saving the photo settings also wrote
that title as the personal name. A separate `GETMCMETA` read confirmed the
nonempty name and empty image fields after “Use Participant Image”. The bridge
maps the resulting source fields; it does not assume a photo-only native UI
operation preserved an empty personal name.

One guarded owned shared-name `SETMETA` attempt returned an error without a
saved success response. A separate fresh read found the shared metadata unchanged
and the personal override intact. The action was not repeated. Live shared-name
mutation remains unverified; this observation does not establish a native
permission rule or make an ambiguous mutation safe to retry.

After the personal metadata changes, A and C each sent one message to the
selected group. Both arrived as distinct encrypted Matrix events with their
existing source IDs and ghosts and decrypted to exact text through retained SDK
keys. The SDK then sent one encrypted return message. All eleven pre-existing
message mappings remained unchanged, with three new mappings in A/C/SDK order.
All three native clients displayed each of those texts exactly once. A normal restart retained all fourteen mappings, the one binding, cleared avatar
and A/C/SDK event order. The SDK decrypted both original events again using
retained keys. The exact committed-build restart also retained all fourteen mappings and the
selected display state; both original events decrypted again with retained
SDK keys.

The owned Matrix name-edit check exposed an error-reporting defect: returning a
plain error made the framework classify unsupported metadata as retryable and
omit the configured user notice. A failing real-dispatch regression now requires
a certain failure, unsupported reason and explicit native-settings notice. The
handlers return that structured status before any source mutation. The framework
can restore the previous Matrix state when `bridge.revert_failed_state_changes`
is enabled. With that option disabled, rejection preserves source and portal
metadata but the Matrix-local edit remains visible.
An avatar-clear edit equal to the current empty avatar is skipped by the
framework; it does not verify the rejection path. The corrected owned check sent different name and avatar values and received
exact `m.notice` bodies replying to both edits. Source-backed portal fields and
all fourteen message mappings were unchanged. These framework notices were
unencrypted despite the room encryption setting; their text contains only the
unsupported-action instruction. Actual bridged traffic in the same room was
encrypted and SDK-decrypted as described above. The lab had automatic rollback
disabled; the two local test values were explicitly restored afterward.

A bounded Mac caller inventory found two `doSetMetaWithChatId:type:content:completion:`
call sites: `setNotice:chatId:completion:` and `setNoticePin:chatId:completion:`.
Their request types are 1 and 6 respectively. The regular `setChatName` path
uses `SETMCMETA` with the string type `name`. This static caller inventory does
not rule out dynamic or platform-specific shared-name setters. The earlier
synthetic type-3 `SETMETA` serializer result establishes wire construction only;
it does not establish regular-group mutation permission. The bridge's selected
mapping consumes shared titles and personal overrides but rejects outbound
Matrix edits instead of guessing a shared source mutation.

Normal source events and discovery are handled synchronously; membership and
group-create operations also hold the existing group gate through publication.
The durable display checkpoint covers stale per-type shared snapshots across
restart. Personal settings have no proven per-room revision gate: the bridge
reads current source fields rather than ordering notice bodies by the global
revision. Simultaneous external framework metadata application is not claimed
as covered by the checkpoint alone.

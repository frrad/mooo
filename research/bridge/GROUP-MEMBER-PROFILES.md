# Regular-group member identity and profiles

Evidence date: 2026-10-10. In progress; no complete lifecycle parity claim.

Mac 26.8.0 independently recovered the regular MEMBER request coordinator,
500-ID batching, sender, typed response callback, successful database block,
room membership updater and NTUser profile updater. Exact callback instructions
identify 0x1014348c4; the neighboring 0x10140a8c4 is not that callback and was not
used as evidence. The request sender preserves nil packets; unsuccessful
responses do not enter the success database block or fetch subsequent batches.
Success applies database work synchronously before continuing remaining IDs.
Database notifications, refresh notices and all downstream consumers are still
being traced.

An isolated signed copy with a separate bundle identity, no official app group
and no network entitlement executed synthetic MEMBER request/model inputs and
NTUser.updateWithLocoMember. Original preference hashes stayed unchanged and
the process was killed. The known user updater copies nickname and thumbnail/
full image fields, including empty or nil values, then recomputes display name.
The synthetic cleared nickname produced the native localized Unknown label.
See [executed fixture](../fixtures/group-profiles/member-runtime.json).

The bridge maps known MEMBER records to stable Kakao IDs. A confirmed empty
nickname clears the Matrix display name so Matrix can use its own fallback;
it does not fabricate a localized profile. Empty image fields explicitly remove
an old ghost avatar. With an empty thumbnail, the full-size image is used as an
explicit bridge mapping decision. A missing record produces no UserInfo and
preserves previous fields instead of confirming a clear.

A production-path failing regression showed a later MEMBER batch error discarding
an otherwise successful authoritative MEMLIST roster and earlier profiles. The
bridge now retains the roster and successful results for regular groups, leaves
missing profiles without fabricated fields, and emits a bounded warning without
profile values. This is bridge failure-isolation policy, not native batch-failure
parity. Existing direct-room error behavior is preserved.

A second failing production regression showed MEMBER accepting profile records
whose response chat ID differed from the selected room. The session now rejects
that batch before appending any of its records; previously completed batches
remain available. This is a bridge response-scoping guard, not a claim about
the native client's behavior on an inconsistent server response.

Owned acceptance below covers nickname/avatar convergence, stable ghosts,
encrypted A/B/C traffic and restart. Native background refresh triggers and
complete HTTP-profile contracts remain gaps; CI and merge are still pending.

Owned observation: A changed its own nickname using Android 26.8.2 while the
bridge was stopped. A separate original-B MEMBER read confirmed the exact new
name and stable source ID. Normal bridge startup then updated A's existing
Matrix ghost and its global Matrix display name without an ordinary message.
All ghost IDs and the total ghost count were unchanged; one selected portal and
all fourteen previous message mappings were preserved. This proves the tested
offline nickname convergence, not live profile-notice handling.

The short read-only packet capture saw only startup BLSYNC. No profile-specific
notice was observed in that window, which is insufficient evidence that none
exists. Mac recovered SYNCEVENT and SYNCACTION model fields, plus OpenChat link
profile notices; complete regular-room scope and refresh callers remain gaps.

The Mac SYNCEVENT transport handler forwards the typed notice to its delegate.
The coordinator publishes a notification containing the event fields. The
traced `openLinkSyncEvent:` consumer obtains an OpenChat reaction controller
and calls its reaction-animation updater with event type/count, author, log ID
and chat type. This consumer provides evidence for OpenChat reactions, not a
regular-member nickname/avatar refresh. Other notification consumers and the
regular-profile refresh trigger remain untraced; the bridge does not treat
generic SYNCEVENT as an authoritative profile update on this evidence.

Static Mac tracing distinguishes profile-view refresh from MEMBER. The profile
view controller dispatches other users to `updateProfileWithUser:chatId:completion:`
and self to `updateMyProfileWithNotify:completion:`. The former excludes nil
users, positive OpenChat link IDs and Plus Friends, chooses the user's access
permit (falling back to the chat-profile permit), and calls the Brewer other
profile API. Its request path is `/talk/profile25/other`, with `userId`, optional
`accessPermit`, and `chatId` only when positive.

Self refresh uses the account's main profile ID. The notify branch views
`/talk/profile25/me` with `profileId` and `lastSeenAt` from the account's
inticker-seen watermark; the other branch calls `/talk/profile25/me/refresh`
with `profileId`. These paths are statically recovered from the arm64 binary's
Swift string storage, not live requests. Response decoding, database callbacks,
authentication headers, profile-view side effects and background refresh callers
remain gaps. No new HTTP profile calls have been added on this partial evidence.

Both traced self-profile response callbacks first construct an error from the
Brewer status, response and transport error. On error they complete with that
error without entering profile persistence. On success they initialize the
profile model from the response dictionary, synchronously run a database block
that calls `updateWithMaldiveProfile:` and sets the profile-update timestamp to
the current time, then complete without error. The updater loads the model with
`loadWithMaldiveProfile:profileUpdate:` before saving it. Field-level loading,
database notifications and transaction failure semantics still need tracing.

One recovered model loader has a `profileUpdate` gate. When enabled, it changes
the display name only for a nonempty nickname, copies the status message,
maps profile-image thumbnail/medium URLs to thumbnail/full stored fields, and
checks profile revisions. Original and animated image URLs are assigned outside
that gate. This differs from MEMBER's nickname-copy path: an empty HTTP profile
nickname cannot be assumed to confirm a display-name clear. The second loader
uses `updateWithNickName:` for a nonempty nickname and copies additional user
attributes; their effects and the caller-selected gate values remain gaps.

An isolated Mac execution now verifies the NTUser loader with its update gate
enabled. The HTTP model reads `nickname`, unlike MEMBER's `nickName`; a key
probe confirmed the latter is ignored. A filled profile changes the existing
nickname and image URLs. Empty or omitted nicknames preserve the seeded nickname
and display name; empty image URLs become empty, and omitted image fields become
nil. This is successful model-loading behavior, not evidence that an unavailable
request confirms an avatar clear. See the
[executed HTTP model fixture](../fixtures/group-profiles/http-profile-runtime.json).
The harness did not save to the database or make an HTTP request.

Additional static push tracing has not established a regular-profile refresh
signal. The SPUSH dispatcher routes types 1, 2, 3, 4, 8–12, 16 and 18 to named
notification, keyword, emoticon, add-friend, Plus Friend keyboard, calendar,
Drawer user-info, team-chat and chat-folder handlers. The Drawer user-info
handler is not evidence for regular member profile refresh. HINT updates a
room's last chat message, mention/reply metadata, archive folder and dock badge,
fetching CHATINFO for an unknown room. Neither traced dispatcher justifies
mapping a generic push to an authoritative member nickname/avatar update.

The bridge now schedules profile refresh independently of messages. Once per
minute, its existing event pump selects one eligible group in rotating ID order.
Only a previously managed regular group or a durably bound Matrix-created
regular group qualifies; removed, pending, direct and unbound rooms are skipped.
The scheduler reuses fresh membership reconciliation with a five-second source
work deadline. It does not create portals or retry requests in that tick.
Successful partial MEMBER results update existing ghosts; unavailable profiles
preserve their previous fields. A source/membership failure retains the access
pause and reports a reconnect action. Shutdown admission and the existing owner
cleanup rules apply; a busy group operation defers polling to the next tick.

This one-room-per-minute schedule is bridge policy, not native notification
parity. With multiple eligible rooms, each room waits for its turn. Production
framework tests verify message-independent refresh, stable identity, unavailable
profile preservation, no repeated creation, and skipping removed/pending rooms
or a stopping owner. Owned acceptance below covers live polling, avatar replacement/clearing and
restart for the selected regular group.

Owned live observation: with the scheduler build connected on original B, A
changed its nickname through its official Android profile editor. The inner
confirmation and outer profile save each had a private receipt written before
the single tap. During the same bridge process, the existing ghost's stored name
and global Matrix profile converged to the exact new nickname without an ordinary
message or restart. All ghost IDs and the total ghost count remained unchanged;
the selected portal remained unique and all fourteen prior message mappings
were identical. No periodic profile-refresh failure was reported. This verifies
live nickname polling for the tested room.

Owned live avatar observation: A selected a synthetic blue/yellow checkerboard
through the official Android profile editor. Scoped media permission admitted
only that image, and selection, image confirmation and profile save each had a
private receipt before the single action. The same connected scheduler process
replaced the prior Matrix avatar without a message or restart. An early read
still showed the previous avatar and was not accepted as success. The converged
Matrix thumbnail showed the expected checkerboard colors. After clean shutdown,
a fresh original-B MEMBER read confirmed the source profile, and downloading its
thumbnail produced bytes identical to the Matrix download. The nickname, ghost
IDs/count and all fourteen message mappings stayed unchanged.

Owned offline avatar clear: while all bridge/profile owners were stopped, A
selected Default Image and saved its profile, with a private receipt before each
action. A fresh original-B MEMBER read confirmed both thumbnail and full image
fields empty and the nickname unchanged. Normal scheduler-build startup removed
the stored ghost avatar and global Matrix avatar without a message trigger.
Ghost IDs/count, portal uniqueness and all fourteen previous message mappings
remained unchanged. Subsequent A/C group messages arrived encrypted from the
correct ghosts and decrypted to their exact expected text with retained SDK keys.

After profile changes, A and C each sent one group text and the encrypted Matrix
SDK sent one return text. All three native participants displayed all three
texts exactly once in source order. New mappings retained the expected source
identities, and the fourteen earlier mappings were unchanged. Normal bridge
restart retained all seventeen mappings without duplicates; A/C ciphertext
still decrypted to exact expected text with the same SDK keys. This is observed
acceptance for one owned regular group, not scale or room-type parity.

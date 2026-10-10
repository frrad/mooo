# Regular-group read receipts and unread state

Evidence date: 2026-10-10. Scope: per-member read receipts in an encrypted
three-member regular group, the read side effects of catch-up and of the
watermark recovery request, and reads that happen while the bridge is offline.
Builds on [read-receipt policy](read-receipt-policy.md) and the
[SYNCMSG differential procedure](syncmsg-read-side-effect-procedure.md).

## Official-client contract (macOS 26.8.0, static)

Method: read-only static inspection of the authorized macOS 26.8.0 arm64
binary in the private Ghidra project; no public prior art. Static observations,
not executed fixtures.

- **Watermark sources.** Other members' watermarks come from `DECUNREAD`, the
  sender's watermark on `MSG` (unless `noSeen`), and the `CHATONROOM` response.
  LOGINLIST, LCHATLIST and CHATINFO carry only the account's own `s`
  (last seen, raise-only) and `n` (unread count).
- **`CHATONROOM`** (`-[BCLocoClient doChatOnRoomWithChatId:completion:]`)
  sends `chatId` (int64), `token` (int64, zero without a stored token) and
  `opt` (int32, zero outside open chats), always all three. Its top-level
  response is parsed as the room: `a` active member IDs with `w` watermarks
  paired positionally, `mi`, `l` (last log), `o` (token) and `f` (full). A full
  response replaces the watermark map; otherwise a strictly-greater merge
  applies. Persistence applies the room data without a status check.
- **Triggers.** Opening or attaching a chat window, `chatOn:` for open windows
  (including after reconnect when the room status is zero), sending into a
  room that is not on yet, calls and folder sync. Closed chats keep stale
  watermarks until next opened or a later push; missed `DECUNREAD` pushes are
  never replayed.
- **Read acknowledgement.** `SYNCMSG` is the only read acknowledgement; the Mac
  sends it while a chat is open (10-second timer gated on its own watermark),
  on chat-off, focus, mark-read and read-all. `cnt` is the number of locally
  stored messages after `cur`, so `cnt=0` is common; a successful response sets
  the local last-seen and own watermark to `max`. Entering a room advances the
  own watermark only locally.
- **Monotonic rules.** Every per-message source uses the strictly-greater
  merge; only a full `CHATONROOM`, member removal or the member-add default
  can lower or seed a watermark.

Recorded gaps: whether the server treats `cnt=0` as a read (static analysis
cannot tell), whether a plain network drop re-sends `CHATONROOM` for open
chats, the server rule for `f`, the integer width the server emits, the
meaning of `noSeen`, and NEWMEM/DELMEM effects.

## Owned observations

Method: native B and C were kept outside the chat; A's per-message unread
count was read from A's screen (it is not exposed in the accessibility tree).
Each source mutation, Matrix action, probe request and bridge start/stop wrote
a private EXCL 0600 receipt first. Only shapes and relative positions of
watermarks were recorded.

| Step | Observation |
|---|---|
| A sent X while the bridge was offline | A shows 2 unread |
| Bridge catch-up (`SYNCMSG cnt=0`) delivered X | still 2 |
| Matrix read receipt → `MarkRead` (`cnt=1`) | 1 |
| C opened the chat | a `DECUNREAD` became a Matrix receipt from C's ghost on X |
| Bridge offline; A sent Y; C opened the chat; restart (no recovery) | no Matrix receipt for C's read of Y |
| One `CHATONROOM` from the bridge profile | a full response with all three members' watermarks (C at Y); A's counts unchanged |
| A sent Z while that session stayed connected | A shows 2 unread for Z: no automatic read |
| Two later `CHATONROOM` requests and a catch-up of Z | the account's own watermark stayed below Z |

Catch-up's `cnt=0` request and `CHATONROOM` produced no visible read
acknowledgement, and repeated `CHATONROOM` requests did not advance the
account's own server watermark. One earlier advance of the account's own
server watermark to Y, between the second offline step and the first
`CHATONROOM`, has no identified cause; A's display did not change, which
suggests the server does not notify other members of every watermark change.
This remains an open observation, not an established effect of either request.

## Production changes and regressions

Each change was preceded by a failing production-path test.

- **Forward-only member receipts.** An older or repeated `DECUNREAD` moved a
  member's Matrix receipt backwards or re-sent it. The bridge now stores each
  member's highest bridged watermark per chat in its key-value store and
  ignores anything at or below it, including after a restart.
- **Offline read recovery.** Reads made while the bridge was away were lost.
  After catch-up and reaction resync on every connection, the bridge sends one
  `CHATONROOM` per bridged chat and passes each active member's watermark
  through the same forward-only path; the account's own watermark becomes the
  Matrix user's receipt as before. Failures are logged and never block the
  connection. `CHATONROOM` is sent only as this read-only snapshot request;
  the client sends no read acknowledgement with it.

Catch-up continues to use `cnt=0` and records no read acknowledgement
(checkpoint v5 policy). The Mac would acknowledge reading only while a chat is
open; the bridge acknowledges only on a Matrix read receipt.

### Deliberate deviations

- mooo sends `CHATONROOM` for every bridged chat on each connection; the Mac
  sends it when a chat window opens. mooo keeps no room token, so each request
  sends token zero and receives a full snapshot.
- A full snapshot never lowers a bridged receipt in mooo; the Mac's full
  response can lower a stored watermark.

## Owned encrypted acceptance

Beyond the observations above, a fresh build of this branch recovered C's
offline read of Y as a Matrix receipt from C's ghost on Y after restart, left
A's unread counts unchanged, and on the next restart bridged no further
receipts (all at or below the stored watermarks). Matrix receipts were read
with the tester device; read state with A's display.

### Acceptance gaps

- Without double puppeting, the account's own receipt is sent by its ghost.
- The cause of the one unexplained own-watermark advance is unresolved.
- Read recovery for chats with many members and the server's integer widths
  were not exercised beyond this group.

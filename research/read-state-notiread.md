# Read-state parity: inbound NOTIREAD and explicit read orchestration

Status: first-party static trace, macOS KakaoTalk 26.8.0, first reviewed
2026-09-30. The send gates, the `notiRead` source and the `serviceId` source
were re-traced and corrected on 2026-10-10. An owned live differential ran the
same day. The bridge decision is in
[read-receipt policy](bridge/read-receipt-policy.md#notiread-not-sent).

This dossier records behavior recovered from the authorized official Mac client
with Ghidra. It is implementation-neutral: it contains no decompiler output,
addresses, account values, message contents or proprietary assets.

## Summary

`NOTIREAD` is not a delivery acknowledgement. The Mac client sends it only
when all of these hold:

- a message from another member arrives as a live `MSG` push;
- that room's transient status is chat-on;
- no user ID holds the room's watermark.

In that case the message is read the moment it arrives, so `NOTIREAD` is the
read acknowledgement for it. A closed room or a watermark hold prevents that
send. The preview preference controls one window-activation hold/release path;
do not equate this gate with every key-focus change. A later explicit read
goes through `SYNCMSG` (see [below](#explicit-user-read-operations)).

## Send path

The LOCO manager's `MSG` push handler runs these steps in order:

1. It records the message log ID as the last token ID.
2. It asks the message-push delegate to accept the notice. The delegate
   returns a boolean, and the handler sends `NOTIREAD` only when that boolean
   is true.
3. It looks up the room by chat ID and link ID itself. The `notiRead` value is
   that room's flag, or true if no room is found.
4. It sends one `NOTIREAD` with no completion handler.

The delegate starts with a false result and does its work in one data-store
block:

1. **Room missing:** it fetches chat info, stores the message and shows a
   notification. The result stays false, so the handler sends no `NOTIREAD`.
2. **Room present:**
   - it un-hides a hidden room;
   - it builds and stores the message, copying the notice's `pushAlert` and
     `silence` flags;
   - it deletes the matching pending message when the echo is of its own send;
   - it updates the room's last message and hands the message to the
     notification manager, **before** any read decision.
3. **The gate:** the result is true only when both hold:
   - the room's transient status is 1, meaning the chat window is open;
   - no user ID is holding the room's watermark.
4. **If true:** it resets the room's new-message count to 0 and moves the own
   and author read watermarks to this log ID.
5. **If false:** it increments the unread count, subject to the usual
   hidden-log, `noSeen` and visibility checks, and moves only the author's
   watermark.
6. Before returning, it forces the result to false when the message's author
   is the logged-in user.

The room status and the watermark hold are both transient UI state:

- Opening a chat window sets the status to 1. Closing it ("chat-off") sets it
  back to 0. Both values reset to 0 whenever a room is loaded from storage or
  created.
- In the reviewed preview-enabled main-window resignation path, the client
  holds the watermark under its own user ID. Main-window activation schedules
  release. Releasing a nonzero hold clears it, updates the local own watermark,
  and synchronizes messages. See the exact activation contract below.

## Preview-dependent main-window read orchestration

Additional first-party tracing on 2026-10-14 resolves the ordinary Mac 26.8.0
window/controller path. Runtime class/selector metadata was inspected in the
isolated, network-disabled official binary; request behavior below is derived
from disassembly and callback analysis, not an executed owned UI flow.

- `FSChatWindowController.windowDidBecomeMain:` verifies that the notification
  belongs to its window. With `UseChatPreview` enabled and a chatting controller
  present, it cancels an earlier scheduled `unholdWatermark` for that controller
  and schedules a replacement after **0.1 seconds** with a nil argument.
- The matching `windowDidResignMain:` branch, with the same preview preference
  enabled, cancels pending release and calls `holdWatermark`.
- `windowDidBecomeKey:` and `windowDidResignKey:` enter different helpers that
  maintain controller focus and input state. The reviewed key-notification
  helpers do not contain that hold/release scheduling pair. Main-window and
  key-window notifications must remain distinct in this contract.
- `FCChattingController.holdWatermark` does nothing without a room or when an
  existing positive hold is present. Otherwise it stores the current user's
  ID in the room's transient hold field; it sends no acknowledgement itself.
- `unholdWatermark` does nothing without a room or with a zero hold. Otherwise
  it clears the hold, runs a synchronous data-store block updating the own
  member watermark to the room's `lastLogId`, and then calls `syncMessage:`
  with nil. The local mutation precedes the protocol coordinator; it is not
  evidence that a network read acknowledgement succeeded.
- `FSChatWindowController.dealloc` cancels the pending controller release.

The delayed selector invocation explains why a direct-call-only search found
no ordinary caller of `unholdWatermark`. Swift Objective-C thunks also make a
nearest preceding selector an unreliable owner for an instruction: the main
notification wrappers, helper targets and callback pointer were resolved
separately before assigning these paths.

This 100 ms delay is local UI orchestration, not a server request-spacing limit.
The immediate older-message display path remains a local database query, as
documented in [the history trace](bridge/HISTORY-STATIC.md#c-chat-open-local-paging-and-single-room-sync).
It does not itself call this release method. An active room can separately
process live pushes, so this does not promise that all concurrent read state
stays unchanged during a scroll.

Still unverified: the preference's default and every alternative activation
path, actual notification/timer ordering in an owned window, and native
failure recovery after the local watermark update. The regular sync
coordinator's request/response and persistence contract is documented
separately; no universal background-window read policy follows from this branch.

Push-alert and silence settings, message type, chat type and the hidden state
do not gate the send. The trace found no duplicate-log check before it; the
message builder's internals were not traced.

A raw scan of the executable finds exactly one caller of the send method and
one caller of the request initializer. Catch-up, `SYNCMSG`, `LOGINLIST`, chat
open, read-all and notification actions do not send `NOTIREAD`.

There is no delay, batching or per-room coalescing. If the carriage is
unavailable, the request fails before a packet is built. With no completion
handler, that failure is dropped silently and never retried.

## Request

`+method` returns `NOTIREAD`. The request model overrides neither its key
mapping nor its validation fields, so the body has five keys and keeps zero
and false values:

| key | BSON type | value |
| --- | --- | --- |
| `chatId` | int64 | room identifier |
| `li` | int64 | link identifier, 0 for non-open chats |
| `watermark` | int64 | the accepted message's log ID |
| `notiRead` | bool | the room's `notiRead` flag, true when absent |
| `serviceId` | int32 | the `MSG` notice's top-level `serviceId` |

The BSON encoder maps NSNumber types directly: `q` becomes int64, `i` int32
and `B` bool. The generic property-to-dictionary step lives in an untraced
superclass, so the key spelling is high-confidence rather than proven at the
serializer.

`serviceId` comes from the `MSG` notice, not from its `chatLog`. The notice's
key mapping renames only `li` to `linkId`, so the wire key is the top-level
`serviceId`. It defaults to 0 when absent (ivar zero-initialization).

## The room `notiRead` flag

- The room model stores it in its extra-information JSON under the key
  `notiRead`, and it defaults to true when absent.
- Its only writer is the room update from a server room object. It writes the
  flag only when the room object carries `notiRead`.
- That room object arrives in the `CHATONROOM`, `CREATE`, `JOINLINK` and
  `CREATELINK` responses and in the `SYNCCREATE` and `SYNCLINKCR` pushes.
  `LOGINLIST`, `LCHATLIST` and `CHATINFO` chat data does not carry it.
- Opening or closing a chat does not change it.
- The only other consumer found returns a per-message unread count of 0 when
  the flag is false. One reading is that the flag marks rooms without read
  counts, but this is a hypothesis.

## Response and failure boundary

The send passes no completion handler, so nothing reads the response: no
status, no `notiRead` write-back and no persistence. See
[`read-state-notiread-response.md`](read-state-notiread-response.md) for the
shared carriage layer.

## Explicit user read operations

Separate static traces of `markAsReadOfChatRoom:`, summary-and-read and
read-all show no `NOTIREAD` call and no separate mark-read LOCO method. These
operations go through the local `SYNCMSG` coordinator with the room's current
last-log or seen-log ceiling. In the reviewed paths, `CHATONROOM` is room
setup and `CHATOFF` is local teardown and sync cancellation; neither is a
read-acknowledgement wire command.

A status-zero `SYNCMSG` can advance server-side read state even when its
`chatLogs` is empty. A non-zero status or a transport failure is not a
successful read acknowledgement. Committing a message locally and persisting a
read watermark must stay separate transactions.

## Owned live differential (2026-10-10)

Setup: the regular three-person group with members A, B and C, all
maintainer-owned disposable accounts. The bridge ran as B's mooo secondary
device. B's Android phone stayed on its home screen, and C did not open the
chat. Twice, A sent one synthetic text from its native client.

The B phone's notifications were read by notification tag (the chat ID) and
counted only; no content was recorded.

| step | B phone notification for the group | A's unread count on the message |
| --- | --- | --- |
| A sends; mooo B online, sends no `NOTIREAD` | appeared within 5 s | 2 (B and C) |
| 60–80 s later, no Matrix receipt | still present | 2 |
| Matrix user reads → bridge `MarkRead` (`SYNCMSG cnt=1`) | gone within 5 s | 1 (C) |

Both trials gave the same result. The bridge reached `CONNECTED` with no
errors.

Observed facts:

- A connected secondary device that does not send `NOTIREAD` leaves the
  message unread and the phone notification in place. The Mac client behaves
  the same with no chat window open or focused.
- The explicit `SYNCMSG cnt=1` read clears the primary phone's notification
  for that chat and lowers the sender's unread count.

The 2026-09-30 direct-chat run, in which a live `MSG` appeared to clear the
sender's unread marker without `NOTIREAD`, did not reproduce in this group
run. Its mechanism remains unresolved.

## Remaining gaps

- The owning class of the delegate, and a second delegate implementation that
  was not disassembled.
- The full chat-on sequence, alternative release paths and owned UI acceptance
  of the preview-dependent activation/timer contract above.
- The superclass's key derivation for request and notice models.
- The database column that persists the room's extra information.
- Duplicate handling inside the message builder, and when the data-store
  context saves.
- The server meaning of `notiRead`, and whether `NOTIREAD` alone clears the
  primary phone's notification. The clean-room client has never sent it.

## Provenance

- Source: authorized official KakaoTalk Mac binary, version 26.8.0, analyzed
  read-only in the maintained Ghidra project.
- Method:
  - exact selector lookup;
  - annotated disassembly of the push handler, the delegate and its block;
  - accessor and setter tracing;
  - raw branch-and-link and selector-reference scans for every caller;
  - request and notice key-mapping lookup;
  - the BSON encoder's type mapping.
- Live: the owned A/B/C differential above, with the bridge built from main at
  `54d6716`.
- Confidence:
  - high for the gates, the single caller, the `notiRead` source and default,
    the `serviceId` source, the request types and the absent completion;
  - high but serializer-unproven for the key spelling;
  - observed for the live differential.

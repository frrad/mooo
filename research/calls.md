# Call records (type 51)

Research date: 2026-10-16. Message type 51 is named `Mvoip` by the official
message-type enum ([message-types.md](message-types.md)). It is the chat log
KakaoTalk writes for a one-to-one or group voice or video call. No public prior
art was consulted.

Fixtures:

- [`fixtures/calls/observed-records.json`](fixtures/calls/observed-records.json):
  `observed`, the chat logs real calls produced between owned accounts.
- [`fixtures/calls/executed-mac-text.json`](fixtures/calls/executed-mac-text.json):
  `executed`, the text the official Mac client's own code produced for 77
  synthetic attachments.

## Owned-account observation

Method: owned Android lab emulators A and B, KakaoTalk 26.8.2. A placed voice
and video calls to B in their direct chat. Each call was answered and ended,
left to ring out, cancelled by A while ringing, or declined by B. B then placed
one call to A and cancelled it. A private probe on B's mooo secondary profile
read the new chat logs with SYNCMSG. Raw records were reduced to their key set,
event type, author and duration, then deleted. B's own chat screen was read for
the text Android displays.

Observations (confidence high):

- Every call writes **two** type-51 chat logs, both authored by the **caller**:
  a start record when the call is placed and an outcome record when it ends.
  Neither record is authored by the callee.
- The attachment is a JSON object string. Observed keys: `type`, `duration`,
  `callId`, `csIP`, `csIP6`, `csPort`, `multiPort`, `cdrInfo`, `cdrToken`,
  `attemptTime`, `conTimeout`; outcome records add `recLog`.
- The chat log's `message` field holds **the same JSON as the attachment**. It
  is not display text. It carries call-server addresses and a call token, so it
  must never be shown or logged.
- `type` values seen: `invite`, `bye`, `noanswer`, `canceled`, `deny` for voice
  and `v_invite`, `v_bye`, `v_noanswer`, `v_canceled`, `v_deny` for video. A
  call that rang out ended after about 60 seconds with `noanswer`.
- `duration` is a JSON integer in whole seconds. It was non-zero only for `bye`
  and `v_bye`. The answered video call's 21 matched both phones' call timers.
  The answered voice call (80) ended when both emulators hung mid-call, so
  there was no on-screen timer to compare against.
- The text depends on who authored the record. B (not the author) saw "Missed
  Call" for A's `noanswer`, `canceled` and `deny` records. For B's own
  `canceled` record it saw "Cancelled".

Not observed: group calls (`cinvite`, `v_cinvite`, `add`, `v_add`, `transferred`,
`v_transferred`), `maintenance`, open chats, and calls answered on a Mac.

## Official Mac client (26.8.0)

Static method: read-only disassembly of the authorized arm64 binary, with
Objective-C selectors and constant strings resolved, and the English
`Localizable.strings` of the same bundle. Executed method: the isolated,
logged-out probe copy of that binary was spawned under Frida. For each
synthetic attachment the harness created the chat-log model, set its author
and attachment, ran its update method, and read back its display text. No
account, network, or preference state was used. The original preferences were
hash-checked unchanged.

### Model

`NTChatMVoip` parses the attachment through a generic JSON model whose
properties are the attachment keys: `callId`, `type`, `csIP`, `csIP6`, `csPort`,
`duration`, `apsAlert`, `member`. Its update method maps `type` by exact,
case-sensitive string comparison to an internal value:

| `type` | Meaning | Internal value |
| --- | --- | ---: |
| `maintenance` | service under maintenance | 1 |
| `invite` / `v_invite` | voice / video call started | 2 / 9 |
| `cinvite` / `v_cinvite` | group voice / video call started | 3 / 10 |
| `deny` / `v_deny` | callee declined | 4 / 11 |
| `noanswer` / `v_noanswer` | no answer | 5 / 12 |
| `canceled` / `v_canceled` | caller cancelled | 6 / 13 |
| `bye` / `v_bye` | call ended | 7 / 14 |
| `add` / `v_add` | members joined a group call | 8 / 15 |
| `transferred` / `v_transferred` | switched to a group call | 100 / 101 |

Anything else gives 0, and so do a missing, empty, non-object or unparseable
attachment and a missing `type`. `duration` goes through key-value coding: a
numeric string (`"80"`) and a fractional number (`80.9`) both become 80, and
a missing value becomes 0.

### Display text

The model has two texts: the chat bubble (`descriptionMessage`) and the
chat-list summary (`lastMessage`). The bubble relies on a voice or video icon
next to it ("Missed Call", "Cancelled", "End"). The summary names the medium
("Missed Voice Call"). Both depend on whether the record's author is the
viewing account, and the key also depends on whether the room is a group. In
English the group and direct strings are identical.

Summary text, executed (`authored by viewer` / `authored by someone else`):

| `type` | Viewer authored | Someone else authored |
| --- | --- | --- |
| `invite`, `cinvite` | Voice Call | Voice Call |
| `v_invite`, `v_cinvite` | Video Call | Video Call |
| `canceled` | Cancelled Voice Call | Missed Voice Call |
| `v_canceled` | Cancelled Video Call | Missed Video Call |
| `deny`, `noanswer` | No Answer on Voice Call | Missed Voice Call |
| `v_deny`, `v_noanswer` | No Answer on Video Call | Missed Video Call |
| `bye` | Voice Call `m:ss` | same |
| `v_bye` | Video Call `m:ss` | same |
| `add` | `<names>` joined the Voice Call | same |
| `v_add` | `<names>` joined the Video Call | same |
| `transferred` | Switch to Group Voice Call. | same |
| `v_transferred` | Switch to Group Video Call. | same |
| `maintenance` | The service is currently under maintenance. We apologize for any inconvenience. | same |
| anything else | Please answer the call on your mobile. | same |

Duration format: `m:ss` below one hour (`0:07`, `1:20`, `59:59`) and `h:mm:ss`
from one hour (`1:00:00`, `10:00:00`). Minutes are not zero-padded. A negative
duration skips both divisions, so -5 renders as `0:-5`. The names in an `add`
record come from the `member` array of user IDs. They are joined with ", ", and
a user the client has no record of renders as "(Unknown)". An empty array
leaves a leading space. A non-string `type` killed the isolated process.

### Replies

`NTChatMVoip` does not override the reply-availability predicate. The base
predicate is not type-specific, so the Mac offers Reply on a call record. What
it sends as the reply's `src_message` for a call record was not traced, and no
official reply to a call record was observed.

## Implementation

mooo decodes type 51 into `events.CallMessage`, which holds the recognized
event type, the duration, and the `member` IDs. It never keeps the `message`
field or the call-server fields. The bridge posts the official summary text as
an `m.notice` from the record's author at its send time. The bubble wording
needs the icon, so the bridge uses the summary instead. "Viewer" is the bridged
account, so the same record reads "Missed Voice Call" on the callee's bridge
and "Cancelled Voice Call" on the caller's. Names in `add` records come from
the bridge's stored member profiles, and the bridged account uses its login
name. Unknown users render as "(Unknown)", like the official client.

Deliberate deviations:

- A non-string `type` killed the official process. mooo treats it as an
  unrecognized event.
- An attachment that is not valid UTF-8 is treated as unrecognized. Its text
  is never shown.
- The bridge uses the Mac chat-list summary rather than the bubble text.

Matrix replies to a call record are refused before anything is sent, with a
message saying replies to KakaoTalk calls are not supported. The official reply
shape for a call record is untraced, so sending one would be a guess.

## Gaps

- Group calls, `add` names, `transferred` and `maintenance` are covered by the
  executed fixture only, not by an owned-account observation.
- The official reply shape for a call record (`src_type`, `src_message`) is
  untraced and unobserved.
- How the Mac picks `add` names (chat-specific nicknames first) was traced
  statically. The bridge uses its stored member nickname instead.
- Android's bubble text uses zero-padded minutes and a different "End voice
  call" layout. The bridge follows the Mac.

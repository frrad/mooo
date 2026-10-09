# Vote (14): inbound poll snapshots

Research date: 2026-10-09. Reference client: Android KakaoTalk 26.8.2 on owned
lab emulators. Methods: controlled owned A/B poll creation and native receiver
inspection, original secondary-session MSG capture, and scoped JADX 1.5.6 APK
inspection. No public prior art was used. Private captures and proprietary
output are outside the repository.

## Observed creation

The owned chat's More → Polls → Create New flow created a text poll titled
`Mooo-Synthetic-Poll` with `Alpha`, `Beta` and `Gamma`. Multi-select, anonymous
responses and adding options were disabled. A receipt was reserved before the
single DONE action. The received MSG had type 14.

Its attachment contained integer `voteId: 0`, `subtype: 1`, `title`, `version` and
`os`. The objects comprised a poll (`t: 9`, `st: 1`, `tt` title, `its` items
with `tt` option text) and a button (`t: 2`, `st: 4`, `url` pointing to the
Kakao chat-board post). The native receiver displayed the title and all three
options. Opening Vote Now displayed the same options in the official detail
view. The receiver submitted one controlled vote for Alpha through that view.
Confidence is high for the observed creation shape; this does not establish
updated counts or closure-message behavior.

The shared fixture preserves the zero vote-ID placeholder and replaces live
chat/post identifiers with synthetic values. Source text is synthetic. Button
URLs are sensitive navigation context;
they are not required to render a text snapshot.

## Static chain and gaps

`VoteChatLog.kt` (`com.kakao.talk.db.model.chatlog.d0`, classes10.dex) reads
title, subtype, vote ID or post ID, and post objects. With objects present it
uses the shared PostChatLog preview formatter; otherwise it formats creation,
cancellation and completion subtypes or falls back to the title.

`PostObject.kt` defines poll object discriminator 9, item text `tt`, optional
thumbnail `th`, and `ittpe` item type (default `text`). The shared preview
formatter dispatches by object type and distinguishes poll subtype and a
closing header. These are static leads for further fixtures, not verified
cancellation or completion support.

`PostEditActivity` creates a PostPostingService intent carrying the selected
chat and posting model. The service chooses create or edit based on an existing
post ID, executes the Moim request, parses successful responses into Post and
notifies listeners. Nonzero status uses the server error text or a generic
failure; exceptions notify failure and clear posting state. `MoimApi.kt`
(`ph90`, classes11.dex) builds a PostContentRequest, serializes the poll posting
model into `poll_content`, and supplies the selected chat to the create API.
The request map also contains `from_chatlog`, `link_id`, `content`,
`object_type`, `schedule_content`, `quiz_content`, `sticon`, `scrap` and
`notice`, with multipart media fields handled separately. The complete poll
serialization and endpoint interface, response model, durable database writes
and all callbacks/consumers remain untraced. Native detail fetching, voting,
results, edits, deletion and closure are separate interactive flows.

## Bridge behavior and acceptance

Render observed text-poll creation as a readable Matrix text snapshot
containing title and ordered options. Preserve the message's delivery identity.
Do not expose the chat-board URL, fetch thumbnails or imply that Matrix replies
cast Kakao votes. Unknown object forms, photo/date options and other lifecycle
subtypes need independent fixtures before support is claimed.

The decoder bounds attachments to 64 KiB, objects to 16, text options to 64,
title to 1024 bytes and each option to 4096 bytes. All nested JSON objects reject
duplicate keys, with depth/complexity limits. These are local safety limits,
not recovered official service maxima. Other object types, image/date options,
unverified lifecycle subtypes and inconsistent titles produce a delivery gap
while preserving the message cursor. Navigation buttons are not acted upon.

Two subsequent owned polls, one sent with the bridge stopped and one sent
while connected, passed SDK decryption as encrypted Matrix text with exact
titles and ordered options. The native receiver showed the live poll's distinct
title/options. A normal restart retained exactly those two rendered polls plus
the initial gap, and delivered a later text message once. The same SDK crypto
store decrypted the earlier poll after restart and the new text. This acceptance
covers creation snapshots; it does not establish interactive Matrix voting or
poll-result synchronization.

The first bridge attempt rejected the observed zero vote-ID placeholder and
produced an unavailable notice. The fixture initially replaced it with a
positive synthetic value, which hid that validation error. Preserve zero in the
regression fixture; new owned messages must verify the fix without deleting
already committed delivery state or claiming that the old notice was rendered
as a poll.

## Repeatable owned sender

`research/emu.sh send-poll a|b` takes private stdin JSON containing `peer`,
`title`, `options` and an absolute `receipt` in a private directory. It starts
in the already-open owned chat with an empty composer, navigates More → Polls
→ Create New, and enters a `Mooo-Synthetic-` ASCII title plus exactly three
distinct ASCII options. It requires the observed empty text-poll form and
disabled optional settings, rejects an already-visible matching title, checks
all entered fields and a stable DONE control, and reserves a receipt before
pressing DONE once. After the new poll appears, it returns to the owned chat.
The return waits for the known More screen and validates its peer header,
Events/Polls/Boards labels and unlabeled Back control. An immediate Android
Back key during that activity transition was observed to be lost; the guarded
return was tested against that state and waits rather than sending another key.

Permission, account-policy or ambiguous outcome screens stop the helper. A
reserved receipt blocks subsequent sends even if the outcome is unconfirmed.
Inspect the owned sender/receiver and delivery state before any new experiment;
do not delete the receipt to retry. No helper casts votes, closes polls, changes
optional settings or retries a restricted action.

## Calendar availability observation

The same owned sender's Events → Add Event flow accepted a synthetic title with
two owned invitees and reminders disabled, but SAVE was rejected by the
official client's user protection policy. No Schedule message was captured.
The action was not retried. This is a Schedule-emitter availability gap, not
evidence about Schedule (13) payloads or a decoder failure.

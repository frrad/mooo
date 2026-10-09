# Kakao profile-card inbound contract and acceptance

2026-10-08, owned Android KakaoTalk 26.8.2 A/B experiment. No public prior art
was consulted. A's previously verified B chat opened Contacts → Send KakaoTalk
Profile. Choose Profile offered My Profile and Friends. The owned My Profile
row was selected; exactly one checked entry and enabled OK were confirmed.
An exclusive owner-only external receipt was reserved before OK. A scoped B
secondary MSG capture observed type 17, without resending the message.

## Observed attachment

`userId` is an integer; `nickName`, `fullProfileImageUrl`, `profileImageUrl`,
`statusMessage`, and `accessPermit` are strings. The owned default profile's image
URLs and status were empty. The permit was a nonempty 36-character string and is
kept private. The public fixture substitutes a synthetic identity and permit.
Do not infer authorization behavior from the name or shape of that field.

## Scoped implementation and remaining trace

The profile picker puts labels in accessibility descriptions; exact text alone
misses them. Confirm the intended identity and recipient before submitting.
Trace the official profile-card model, serializer, share callback, persistence,
consumer/open-profile action and failures. The ContactChatLog name-preview getter
belongs to type 4 and must not be reused as evidence for type 17.

The bounded production decoder and readable native Matrix `m.text` rendering
are implemented. First owned catch-up delivery passed encrypted SDK decryption
with expected nickname and shared identity. A fresh shared-script delivery also passed encrypted SDK verification on
2026-10-09. Three independently submitted messages retained three identities
after normal restart. Profile opening,
friend addition, permit use and remote avatar fetching remain unvalidated. A
rendering implementation must avoid silently adding contacts or interpreting the
permit as a URL. The official receiver displayed the shared default profile and its View Profile
action opened the owned profile view. Matrix-side Kakao profile actions and the
full official persistence/callback/failure chain remain explicit gaps.

## Official model observations

Android `classes8.dex` source-file metadata maps `ProfileChatLog.kt` to
`com.kakao.talk.db.model.chatlog.v` and `ProfileAttachment.kt` to `p911`.
JADX 1.5.6 single-class inspection traces chat-log initialization: a nonblank
attachment string is parsed into the profile model; malformed JSON is caught and
the model remains unset. Its getter assumes initialization and fails when unset.
The preview is a localized KakaoTalk-profile label.

The attachment reader accepts `accountId`, `userId`, `nickName`, `statusMessage`,
`profileImageUrl`, `userType`, and `accessPermit` via optional JSON getters. Its
serializer emits those same fields. The observed share additionally carried
`fullProfileImageUrl`, which this reader does not consume. Missing accountId and
userType occur in the observed fixture; do not require them based solely on the
serializer. No open-profile authorization or permit semantics follows from this
reader. Official B displayed the received default-avatar card with nickname and
View Profile. Full callback/persistent-state/failure chains remain gaps.

The readable Matrix message includes nickname, status when present and shared
Kakao user ID. Avatar retrieval, View Profile and access-permit authorization are
explicitly separate gaps. The permit is neither retained in the event nor
forwarded to Matrix. Parser policy requires positive user ID, a nonempty nickname,
64 KiB attachment JSON, at most 64 unique fields, 1024-byte nickname and 16 KiB
status. Duplicate fields are rejected. These bounds are mooo policy.

## Lab continuity observation

The long-running bridge process remained alive after a logged bootstrap failure
and disconnects. A submitted fixture was initially absent from Matrix. A normal
restart with the original profile reconciled it once; it was not resent. A new
distinct fixture after connection recovery passed fresh live encrypted delivery.
The underlying bootstrap failure remains unresolved; process liveness alone is
insufficient proof of a connected bridge or successful live acceptance.

The later synthetic text arrived once in the bridge and official receiver after
restart. The first profile event remained decryptable with retained Matrix keys.
Exact committed-head validation and final primary-device readiness are repeated
before merge.

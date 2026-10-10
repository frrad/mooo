# Kakao message-type inventory

Research date: 2026-10-08. Source: authorized Android KakaoTalk 26.8.2 APK,
`classes10.dex`, runtime class `tg9`, Kotlin source metadata `ChatMessageType.kt`.
JADX 1.5.6 single-class inspection recovered enum constructor values and original
names from Kotlin metadata. DEX SHA-256:
`792d9793080799ac964af1992a11206b6a092f35b552bfa3f8d4254b2a8390b8`.
No public protocol implementation was consulted. Confidence is high for the
static identifier/value mapping; this is not executed or observed payload parity.
No decompiled source or proprietary binary is included in the repository.

The constants live in `internal/protocol/messagetype`. They use explicit `int32`
values and recovered names; only `UNDEFINED` becomes Go `Undefined`. Existing
`chat.TextType`, `chat.ReplyType` and `media.PhotoType` alias that single inventory.
Naming unsupported constants is intentional and does not enable their payloads.

| Value | Official name | Current inbound handling |
| ---: | --- | --- |
| -999999 | `UNDEFINED` | Envelope rejects nonpositive value; semantics unimplemented |
| -100 | `Category` | Envelope rejects nonpositive value; semantics unimplemented |
| -22 | `ChannelAiNoticeFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -21 | `OpenChatMarketWelcomeFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -20 | `OpenChatMarketWelcomeMessage` | Envelope rejects nonpositive value; semantics unimplemented |
| -19 | `PartnerECommerceNoticeFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -18 | `PartnerStoreNoticeFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -16 | `OpenLinkV1SeparatorFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -15 | `ChatLogStoreNoticeFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -14 | `ECommerceNoticeFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -13 | `OpenLinkIllegalBlind` | Envelope rejects nonpositive value; semantics unimplemented |
| -11 | `DeletedAll` | Envelope rejects nonpositive value; semantics unimplemented |
| -10 | `AlimtalkSpamFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -9 | `PNCFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -7 | `SecretChatInSecureFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -6 | `SecretChatWelcomeFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -5 | `LostChatLogsFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -4 | `SpamFeed` | Envelope rejects nonpositive value; semantics unimplemented |
| -3 | `LastRead` | Envelope rejects nonpositive value; semantics unimplemented |
| -2 | `KakaoLink` | Envelope rejects nonpositive value; semantics unimplemented |
| -1 | `TimeLine` | Envelope rejects nonpositive value; semantics unimplemented |
| 0 | `Feed` | Envelope rejects nonpositive value; semantics unimplemented |
| 1 | `Text` | Typed text and observed nonanimated Mini emoticons; animated Mini remains a gap |
| 2 | `Photo` | Typed message |
| 3 | `Video` | Typed direct-URL MP4; relay/high-quality forms remain gaps |
| 4 | `Contact` | Typed direct-URL vCard 3.0; other versions/resource forms remain gaps |
| 5 | `Audio` | Typed observed direct-URL M4A; legacy/relay forms remain gaps |
| 6 | `AnimatedEmoticon` | `UnsupportedMessage` notice |
| 7 | `DigitalItemGift` | `UnsupportedMessage` notice |
| 9 | `Link` | `UnsupportedMessage` notice |
| 10 | `OldLocation` | `UnsupportedMessage` notice |
| 11 | `Avatar` | `UnsupportedMessage` notice |
| 12 | `Sticker` | Typed message |
| 13 | `Schedule` | `UnsupportedMessage` notice |
| 14 | `Vote` | Typed observed text-poll creation snapshot; interactive voting and lifecycle changes remain gaps |
| 15 | `CJ20121212` | `UnsupportedMessage` notice |
| 16 | `Location` | Typed single location as native Matrix geo URI; OldLocation/live updates remain gaps |
| 17 | `Profile` | Typed readable identity/status; avatar and profile actions remain gaps |
| 18 | `File` | Typed direct-URL file; relay/cloud forms remain gaps |
| 20 | `AnimatedSticker` | Typed message |
| 21 | `Nudge` | `UnsupportedMessage` notice |
| 22 | `Spritecon` | `UnsupportedMessage` notice |
| 23 | `SharpSearch` | `UnsupportedMessage` notice |
| 24 | `Post` | Typed observed text-only board-post snapshot; rich/multimedia and board interactions remain gaps |
| 25 | `AnimatedStickerEx` | `UnsupportedMessage` notice |
| 26 | `Reply` | Typed message |
| 27 | `MultiPhoto` | Typed URL-based album; resource-only forms remain gaps |
| 28 | `LargeVideo` | `UnsupportedMessage` notice |
| 29 | `LargeFile` | `UnsupportedMessage` notice |
| 51 | `Mvoip` | `UnsupportedMessage` notice |
| 52 | `VoxRoom` | `UnsupportedMessage` notice |
| 71 | `Leverage` | `UnsupportedMessage` notice |
| 72 | `Alimtalk` | `UnsupportedMessage` notice |
| 73 | `PlusLeverage` | `UnsupportedMessage` notice |
| 81 | `Plus` | `UnsupportedMessage` notice |
| 82 | `PlusEvent` | `UnsupportedMessage` notice |
| 83 | `PlusViral` | `UnsupportedMessage` notice |
| 96 | `ScheduleForOpenLink` | `UnsupportedMessage` notice |
| 97 | `VoteForOpenLink` | `UnsupportedMessage` notice |
| 98 | `PostForOpenLink` | `UnsupportedMessage` notice |
| 100 | `Universal` | `UnsupportedMessage` notice |
| 101 | `UniversalVerified` | `UnsupportedMessage` notice |

## Flags and classification gaps

The same official enum declares `DELETED_ALL_CHAT_TYPE=0x4000`,
`OPENLINK_ILLEGAL_BLIND=0x8000` and `SECRET_CHAT_TYPE=0x10000000`. Its companion
checks flags only on positive integers, prioritizes deleted-all over illegal-blind
classification, and otherwise looks up exact enum values with UNDEFINED fallback.
Other helpers clear selected flags and classify MultiPhoto as Photo for a specific
consumer. These are static leads, not a general normalization contract.

A text or other ordinary base type with `DELETED_ALL_CHAT_TYPE` is a message
deleted for everyone; the server still returns its content, which mooo never
renders ([edits and deletions](bridge/GROUP-EDITS-DELETIONS.md)). mooo
preserves other raw positive flagged values as unsupported; it does not clear
flags and render blinded or secret-chat content as ordinary messages.
The existing MSG envelope accepts only positive types: enum feed/sentinel values
are documented but do not gain support in this naming change. Wire delivery,
persistence, rendering and failure behavior of those values remain untraced.
The separate LOCO feed/notification method contracts are not expanded here.

## Supported scope and remaining work

Text, Photo and Reply have dedicated typed paths. Sticker/AnimatedSticker have
[scoped inbound validation](stickers.md). Android `AnimatedEmoticon=6` corresponds
to the Mac sound-sticker accessor; the name alone does not establish that every
instance has audio. Spritecon and AnimatedStickerEx are separate unsupported
values; do not assign them the existing type-12/20 decoder without tracing their
attachments, resources and consumers. Opaque historical/product names such as
CJ20121212 and Leverage are retained without invented semantic descriptions.

Audio/files, contact/profile/location cards, resource-only multi-photo messages,
schedules/votes/posts and their OpenLink variants, call-related models and
business/universal templates require independent payload research and controlled
owned-account acceptance. Native outbound stickers also remain unsupported.
A named numeric type is never a claim of support for paid packs, audio playback,
secret chats or official-client parity.

[Type-27 acceptance and type-28/29 gates](multi-large-media.md) record the
owned emulator findings and remaining live-test limitations.

[Ordinary-video contract and acceptance](video.md) describe scoped type-3 support.

[Ordinary-file contract and acceptance](file.md) describe scoped type-18 support.

[Profile-card contract and acceptance](profile.md) describe scoped type-17 support.

[Location contract and acceptance](location.md) describe scoped type-16 support.

[Mini emoticon contract and acceptance](mini-emoticons.md) describe type-1
attachment placement and ordered encrypted text/image parts.

[SharpSearch availability audit](sharp-search.md) records the inspected normal
UI and static feature gate; no type-23 payload or support is claimed.

# Prior art: KakaoTalk secondary-device interoperability

- Survey date: 2026-09-20
- Scope: public technical material relevant to a macOS-style secondary-device
  client; bridge implementation is deliberately secondary.

## Current product behavior

Kakao officially distributes Windows and macOS clients. Its account safety guide
describes a four-digit verification code for PC/Mac sub-devices. Android tablet
support was introduced as another concurrent device class, and contemporary Play
Store metadata lists phones, tablets, Chromebooks, and Wear OS. The first target is
nevertheless the macOS secondary-device flow because it is designed to coexist with
the primary phone account.

Sources:

- [KakaoTalk account safety guide](https://talksafety.kakao.com/en/toolandguide/account)
- [Official KakaoTalk for macOS notices](https://pc.kakao.com/talk/notices/en?agent=mac)
- [KakaoTalk Google Play listing](https://play.google.com/store/apps/details?gl=KR&id=com.kakao.talk)
- [2021 Android tablet launch coverage](https://www.asiae.co.kr/en/article/2021011817194066607)

## Most relevant recent work

### Kakao Talk Is Making Me LOCO

Jusung Lee's 2026 write-up is the closest public match to this project's goal. It
describes HTTPS-based mobile authentication, LOCO chat transport over TCP with BSON
payloads, the destructive effect of direct Android login on the primary phone
session, and a distinct Mac secondary-device flow. It also records changes relative
to the discontinued `node-kakao` implementation: key rotation, handshake and
check-in changes, response schema changes, agent selection, and added device
registration behavior.

- [Kakao Talk Is Making Me LOCO](https://jusung.dev/posts/kakao-talk-is-making-me-local/)

Treat unpublished implementation claims as leads until reproduced against a
versioned client.

### OpenKakao

OpenKakao publishes recent static observations for Android 26.7.1 and Mac 26.7.0.
Reported leads include a stable 22-byte LOCO frame, BSON command payloads, booking
and ticket hosts, RSA plus AES-GCM session setup, and fields seen around `GETCONF`,
`CHECKIN`, and `LOGINLIST`. Its changelog also warns that repeated attempts against
unregistered secondary devices may cause login blocks, and that older assumed
registration endpoints no longer work.

- [Android 26.7.1 and Mac 26.7.0 static analysis](https://github.com/JungHoonGhae/openkakao-cli/blob/main/docs/research/android-apk-26.7.1.md)
- [OpenKakao protocol overview](https://openkakao.vercel.app/docs/protocol/overview/)
- [OpenKakao changelog](https://github.com/JungHoonGhae/openkakao-cli/blob/main/CHANGELOG.md)
- [OpenKakao authentication notes](https://openkakao.vercel.app/docs/getting-started/authentication)

These are useful static observations, not proof that a complete current login was
performed. In particular, do not brute-force login or replay stale registration
recipes.

## Implementations and archives

- [`storycraft/node-kakao`](https://github.com/storycraft/node-kakao) is the most
  substantial historical open-source LOCO implementation, but was discontinued in
  2021. Its [tablet login discussion](https://github.com/storycraft/node-kakao/discussions/152)
  documents an Android sub-device configuration and X-VC provider.
- [`KiwiTalk/KiwiTalk`](https://github.com/KiwiTalk/KiwiTalk) is an unofficial
  Rust/TypeScript/Tauri client and a useful architecture/protocol comparison.
- [`stulle123/kakaotalk_analysis`](https://github.com/stulle123/kakaotalk_analysis)
  records older Android static/dynamic work around sub-device and QR login, storage,
  certificate pinning, and application crypto.
- [`matrix-appservice-kakaotalk`](https://matrix.org/ecosystem/bridges/kakaotalk/)
  is an obsolete Matrix bridge built around mautrix-python and `node-kakao`.
- [`kuuko-re/kakaotalk-loco-client`](https://github.com/kuuko-re/kakaotalk-loco-client)
  claims a current implementation but is closed source; do not treat its README as
  independent technical evidence.
- Beeper's current bridge guidance favors Go and Matrix bridgev2, which supports
  this project's language choice once protocol work is mature:
  [Building a Beeper bridge](https://blog.beeper.com/2025/10/28/build-a-beeper-bridge/).

## Working conclusions

1. Target the official macOS flow; do not impersonate a primary Android login.
2. Use the Android client as a source of secondary-device approval behavior and
   static leads, not as the bridge session itself.
3. Perform exactly controlled login experiments with the disposable account.
   Record states and versioned evidence; avoid repeated speculative requests.
4. Start with offline macOS binary metadata and Android APK analysis, then observe
   an official login once the lab account exists.
5. Convert observations into a code-free state/protocol specification before Go
   implementation.
6. Revalidate every field and endpoint against current clients. Historical
   `node-kakao` behavior is a hypothesis generator, not a specification.

## Highest-value next experiment

Create the disposable account in an official Android environment, perform one
official Mac login, verify that the phone remains active and the Mac appears in
device management, then preserve sensitive artifacts outside Git for offline,
sanitized analysis.

# Prior art: KakaoTalk secondary-device interoperability

- Initial survey date: 2026-09-20
- GitHub refresh: 2026-09-29
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

The article calls its Go port of `node-kakao` **암소** (`amso`) and reports that it
was updated until a current Mac-agent session could log in and exchange messages.
No public source repository or standalone specification for 암소 was found during
this survey. It is therefore evidence that the approach worked for the author, not
an implementation dependency available to this project.

The article's historical protocol reference is Cai/0x90's 2012 Korean series,
[KakaoTalk LOCO protocol analysis](https://web.archive.org/web/20240325014628/https://www.bpak.org/blog/2012/12/kakaotalk-loco-%ED%94%84%EB%A1%9C%ED%86%A0%EC%BD%9C-%EB%B6%84%EC%84%9D-1/).
It explains the original move from HTTPS messaging to a custom TCP protocol and
the reverse-engineering approach used against an early Windows Phone client. Its
architecture is useful historical context; endpoints, authentication, crypto, and
field-level claims are too old to reuse without independent confirmation.

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

## 2026 GitHub implementation refresh

The 2026-09-29 refresh searched GitHub repository metadata, source trees, commit
history, dependency manifests, authentication paths, protocol implementations,
licenses, tests, and CI state. Revisions below are pinned so later repository
changes do not silently alter these conclusions.

### agent-messenger

[`agent-messenger/agent-messenger`](https://github.com/agent-messenger/agent-messenger)
at revision
[`c99c1af`](https://github.com/agent-messenger/agent-messenger/commit/c99c1af9a8cd0a31b862bb5c333ee1f96f9182a4)
contains the broadest recent public LOCO client found in this survey. Its
TypeScript implementation includes the 22-byte BSON envelope, RSA-OAEP plus
AES-128-GCM secure layer, booking/check-in/login sequence, PC and Android-tablet
profiles, chat and member operations, message history, real-time events, media,
and reconnect behavior. KakaoTalk support was present in its public history by
July 2026 and continued receiving fixes through September 2026.

Its default identity and registration path are Android 25.9.2 tablet-subdevice
emulation using the historical passcode endpoints. A PC profile exists, but this
is not evidence of the current Mac 26.8 QR registration flow. The repository's
KakaoTalk E2E tests skip when private test configuration is absent, and no public
test result inspected here proves a current first-time registration. The
[protocol notice](https://github.com/agent-messenger/agent-messenger/blob/c99c1af9a8cd0a31b862bb5c333ee1f96f9182a4/src/platforms/kakaotalk/protocol/NOTICE.md)
attributes its protocol knowledge to OpenKakao, `loco-wrapper`, `node-kakao`,
KakaoForge, and the original LOCO research. No repository `LICENSE` file or
package license declaration was found at the pinned revision, so its source is a
research comparison, not an implementation dependency.

### KakaoForge

[`play2fly/KakaoForge`](https://github.com/play2fly/KakaoForge) at revision
[`4b774ea`](https://github.com/play2fly/KakaoForge/commit/4b774ea40b1347280fadb685415436584093118b)
is a Node/TypeScript LOCO bot library published in February 2026. It implements an
Android 26.1.2 QR flow, the current-style AES-GCM secure layer, persistent LOCO
sessions, message and media operations, reactions, replies, and reconnect. Later
projects explicitly use it as a protocol reference.

This is Android-tablet rather than Mac registration, and the pinned profile
predates the clients under current study. Its
[custom license](https://github.com/play2fly/KakaoForge/blob/4b774ea40b1347280fadb685415436584093118b/LICENSE)
allows only non-commercial use, prohibits specified abusive use, and requires
attribution. Do not copy or depend on it without separately resolving license
compatibility.

### loco-wrapper

[`NetRiceCake/loco-wrapper`](https://github.com/NetRiceCake/loco-wrapper) at
revision
[`6d5f647`](https://github.com/NetRiceCake/loco-wrapper/commit/6d5f6477f64cc0e6baf6bb4a11480c6216ec7fab)
is a substantial Java client from late 2025. It reports Android 25.9.2 tablet
registration and implements GCM transport, `GETCONF`, `CHECKIN`, `LOGINLIST`,
rooms, members, text/media operations, and event handling. No license was found,
and no commits after December 2025 were present. Treat its behavior as a public
lead only.

### Go client and bridge implementations

[`sooids/mautrix-kakaotalk`](https://github.com/sooids/mautrix-kakaotalk) at
revision
[`29fecff`](https://github.com/sooids/mautrix-kakaotalk/commit/29fecfff013fad8edd4830cb4adf01491a527053)
is an AGPL-3.0 Go implementation combining a LOCO core with a Matrix bridgev2
connector. Its source includes the modern GCM handshake and framing, booking,
check-in, `LOGINLIST`, chat/media operations, reconnect, history synchronization,
and a degraded REST fallback.

It does not implement this project's desired enrollment model. Its documented
entrypoint extracts and reuses identity and credentials from the official Mac
client's `Cache.db`, defaults to a Mac 26.2.0 profile, and does not create a new
client-owned identity. The checkout is also not self-contained: `go.mod` replaces
`maunium.net/go/mautrix` with a missing sibling `../mautrix-go`, so
`go test ./...` cannot build the bridge from a standalone clone. On 2026-09-29,
the independently runnable `internal/kakao/...` tests mostly passed, but one media
MIME-normalization test failed; the repository had no GitHub Actions runs. These
facts make it valuable architecture and schema prior art, not evidence of a
working current bridge.

[`yms2772/kakao.go`](https://github.com/yms2772/kakao.go) at revision
[`9addd22`](https://github.com/yms2772/kakao.go/commit/9addd2282119df71fa0b0788104ea5af0658232f)
is an older standalone Go LOCO package. It uses the historical RSA key and
AES-CFB/type-2 secure layer and has been unchanged since 2022, so it is not a
current transport or authentication reference.

### Additional recent toolkit

[`yushosei/loco-protocol-kotlin`](https://github.com/yushosei/loco-protocol-kotlin)
at revision
[`7f7ce6b`](https://github.com/yushosei/loco-protocol-kotlin/commit/7f7ce6b580f47d4b28f616ca351f347d1a834e44)
is a small MIT-licensed 2026 Kotlin toolkit with both historical AES-CFB and
OpenKakao-style AES-GCM profiles, REST helpers, login packet construction, and a
CLI. Its history contained only two substantive commits, and its connection path
tries multiple historical/current profiles and parameter candidates. No public
evidence of a successful current first-time registration was found; use it as an
inventory of hypotheses rather than a verified client.

### Resulting gap

No inspected public repository combines all of the following:

- a current Mac-class secondary-device identity;
- first-time QR registration for a newly generated client identity;
- no import of an official KakaoTalk profile, credentials, or session state;
- a reproducible, versioned evidence trail for current protocol behavior;
- a self-contained Go protocol core suitable for a later Matrix/Beeper bridge;
- licensing compatible with a generally usable public, self-hostable project.

The public ecosystem strongly corroborates the packet envelope, BSON encoding,
RSA-OAEP/AES-GCM layer, three-stage bootstrap, and much of the command surface.
The remaining differentiator for this project is safe, current enrollment and
session establishment under a fresh Mac-class identity, followed by a clean Go
implementation with independently reproduced behavior.

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
7. Use `agent-messenger`, KakaoForge, `loco-wrapper`, and
   `sooids/mautrix-kakaotalk` for negative compatibility tests and architecture
   comparison only. Their Android emulation, credential-import assumptions,
   missing licenses, restrictive licenses, or unverified current behavior prevent
   treating any of them as the normative implementation.

The current macOS 26.8.0 analysis has independently reproduced two durable pieces
of this prior-art lineage: the fixed 22-byte packet envelope and BSON command
bodies. Modern secondary-device registration, secure-session details, and current
bootstrap field semantics remain the material delta.

## Highest-value next experiment

Create the disposable account in an official Android environment, perform one
official Mac login, verify that the phone remains active and the Mac appears in
device management, then preserve sensitive artifacts outside Git for offline,
sanitized analysis.

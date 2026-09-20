# Client inventory

## macOS

- Inventory date: 2026-09-20
- Application: KakaoTalk from the Mac App Store
- Bundle identifier: `com.kakao.KakaoTalkMac`
- Version: 26.8.0 (`CFBundleVersion` 2000)
- Architectures: arm64 and x86_64
- Executable SHA-256:
  `5dc8446b5d82acf9a58a2a94fc7384e032a6041585f1ad74938e24b65ac76319`
- Minimum macOS: 12.0
- Code-signing team: `L75WVXX68A`
- Relevant bundled components observed: Protobuf, OpenSSL, SQLCipher,
  CocoaAsyncSocket, Alamofire, AFNetworking, FMDB, and nanopb
- State at inventory: logged out; no account interaction performed
- Packaging: ordinary signed release binary; Mach-O encryption metadata and a
  packer were not observed
- Metadata: substantial Objective-C and Swift class, method, protocol, and
  reflection metadata remains available for offline analysis

The component list is an investigation lead, not evidence that any one component
implements KakaoTalk message transport or storage.

## Initial offline string survey

The signed 26.8.0 executable contains strings for `booking-loco.kakao.com`,
`ticket-loco.kakao.com`, `GETCONF`, `CHECKIN`, `LOGINLIST`, `X-VC`, and a passcode
registration sequence. It also contains route strings for passcode generation,
device registration, and cancellation under a Mac account path. This supports a
hypothesis that the installed Mac build contains a polling-based secondary-device
passcode flow and LOCO booking/check-in behavior; runtime or deeper control-flow
analysis is still needed to confirm it.

No application launch, login attempt, traffic capture, or account data access was
used for this survey. Extracted proprietary code, disassembly, internal names, and
raw string dumps are not stored in the repository.

## Initial analysis feasibility

The offline metadata exposes useful boundaries for account/device registration,
LOCO configuration and connection setup, protocol serialization, trust evaluation,
keychain access, and encrypted database initialization. These boundaries are leads
for behavioral analysis; they do not establish precise wire formats, key usage, or
endpoint selection on their own.

The logged-out application launched successfully for a tooling check. A Frida
attachment from the normal research user was rejected by macOS process-access
controls. No system security setting, application signature, entitlement, or
binary was modified. Static analysis is therefore the initial method, with dynamic
instrumentation deferred until it can be done in an explicitly prepared and
documented environment.

## Android tablet comparison

The inventoried Android 26.8.2 package switches into a first-class tablet
configuration at the Android large-screen resource boundary. The same package has
resource strings and model names consistent with distinct primary-tablet and
companion-tablet flows, device registration and email verification, and persistent
secondary-device login.

This makes Android useful for naming behavioral states and checking hypotheses,
but not the first implementation identity. The split package contains many DEX
files, obfuscated code, and native security and cryptographic components, so its
static analysis surface is materially noisier than the macOS binary. Exact server
policy and field semantics still require evidence; resource text and model names
alone are not wire-level proof.

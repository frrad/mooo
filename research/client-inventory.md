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

The component list is an investigation lead, not evidence that any one component
implements KakaoTalk message transport or storage.

## Initial offline string survey

The signed 26.8.0 executable contains strings for `booking-loco.kakao.com`,
`ticket-loco.kakao.com`, `GETCONF`, `CHECKIN`, `LOGINLIST`, `X-VC`, and a passcode
registration sequence. It also contains route strings for passcode generation,
device registration, and cancellation under a Mac account path. This independently
confirms that the installed Mac build contains a polling-based secondary-device
passcode flow and LOCO booking/check-in behavior.

No application launch, login attempt, traffic capture, or account data access was
used for this survey. Extracted proprietary code, disassembly, internal names, and
raw string dumps are not stored in the repository.

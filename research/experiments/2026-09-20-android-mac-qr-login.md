# Android-to-macOS QR login baseline

Date: 2026-09-20

## Scope

Test the official KakaoTalk QR flow from an owned disposable Android account to
the official macOS client. This note contains no account identifiers, live QR
tokens, verification codes, session material, or packet contents.

## Environment

- KakaoTalk Android 26.8.2 on an Android 15 arm64 emulator
- KakaoTalk macOS 26.8.0 on Apple silicon
- Fresh non-Korean lab account using a virtual phone number

## Method

1. Opened **QR Code Log In** in the official macOS client.
2. Captured the short-lived QR locally and transferred it to the owned Android
   emulator.
3. Used KakaoTalk Android's scanner **Album** option to select the capture.
4. Chose persistent PC verification and entered the four-digit code displayed by
   the Mac client.
5. Removed all QR captures from the host, emulator, and Android media index.

## Observations

- The Android client decoded the QR and identified the Mac login attempt.
- Persistent-device verification reached an Android success message.
- The Mac client then reported a generic KakaoTalk server connection failure and
  returned to its login screen.
- Android's **Manage Devices** screen showed no registered device afterward.
- A second fresh QR was decoded, but its final approval returned: “Logins are
  restricted from sub devices due to user protection measures.” No further
  login attempts were made.
- The Android string resource is `qr_login_blocked_user_message`; its Korean
  value is `이용자 보호조치로 서브디바이스 로그인이 제한되었습니다.`
- Static tracing shows device registration uses the implementation-level route
  `android/account/passcodeLogin/registerDevice`. Protocol status `-997`
  (`BLOCKED`, not an HTTP status) selects the dedicated blocked event and stops
  the registration flow. No client-side unblock path was observed.
- Candidate Kakao service hosts resolved and completed TCP/443 and certificate
  verification successfully. This rules out a basic host DNS/TLS outage but
  does not identify the endpoint used by the failed session exchange.
- Local macOS KakaoTalk logs are opaque/encrypted and yielded no plaintext error
  code or endpoint.

## Interpretation

The QR transport and mobile approval path worked. The observed blocker is an
account-level protection state, not an inability to present a QR image to an
emulator.

Kakao's official safety documentation says automated protection measures can be
triggered by a virtual overseas number or use from a PC emulator and explicitly
lists PC/Mac subdevice login as a restricted capability. Both listed
environmental signals apply here, so they are plausible triggers rather than
proven individual causes.

Kakao's public timing descriptions are not fully consistent: its current safety
guide says most false positives clear within several days, while its dedicated
restriction page says a few weeks. Both say the date is system-controlled and
can move earlier when no further unusual activity is detected.

Sources: [KakaoTalk automated detection and user-protection measures](https://talksafety.kakao.com/measure),
[KakaoTalk service restriction policy](https://kakao.com/kakaoTalkRestriction?lang=en)

## Next step

Leave the account idle and avoid repeated authentication attempts. Recheck once
after a conservative cooling-off period, then perform one controlled QR login
while collecting only sanitized connection metadata. If the restriction
persists, use Kakao's support/appeal path or change the lab's phone/device
provenance rather than trying to bypass the protection.

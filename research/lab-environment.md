# Lab environment

- Inventory date: 2026-09-20
- Host: Apple Silicon Mac, macOS 26.6.2
- Repository contains no lab credentials or sensitive artifacts.

## Android emulator

- AVD name: `mooo-lab`
- Android: 15
- Image: Google Play ARM64, API 35
- Device profile: Pixel 7
- Build fingerprint at creation:
  `google/sdk_gphone64_arm64/emu64a:15/AE3A.240806.036/12592187:user/release-keys`
- State: boots successfully; official Play Store is present; Google sign-in and
  KakaoTalk installation are pending interactive account setup.

The AVD itself lives in the user's Android configuration outside this repository.
Do not copy emulator userdata into Git.

## Baseline tools

- Android Studio 2026.1.4.8
- Android SDK command-line tools and emulator 37.1.11
- Android platform-tools 37.0.1
- Ghidra 12.1.3
- JADX 1.5.6
- Wireshark CLI 4.6.8
- mitmproxy 12.2.3
- Frida 17.18.0 / frida-tools 14.10.4

Packet-capture privileges and interception certificates have deliberately not been
installed. Add them only as part of a documented experiment with an explicit trust
and cleanup plan.

## Sensitive workspace

Raw analysis artifacts should use an external private directory, with a private
manifest mapping opaque experiment IDs to artifacts. The public repository ignores
common capture, database, key, package, and local lab paths, but `.gitignore` is not
a security boundary.

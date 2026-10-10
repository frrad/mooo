# Encrypted direct-room validation

Observed 2026-10-07 against owned disposable Android A/B accounts, KakaoTalk
26.8.2, local Synapse, production bridge source at `1d416d8`, and pinned
mautrix-go v0.31.0 with the pure-Go Olm backend. The existing B secondary profile,
bridge database and A/B portal were retained. The room creator enabled
`m.megolm.v1.aes-sha2` through the supported Matrix state API; bridge
`encryption.allow` was enabled. Default trust settings were retained.

## Results

- A → Matrix text: one mapped event, ciphertext on the homeserver, synthetic
  body absent from the raw event; the independent SDK client decrypted the
  exact expected text.
- Matrix → A text: SDK-encrypted room event, one positive Kakao message mapping,
  and exact synthetic text visible in A's official client.
- Matrix → A photo: encrypted room event and encrypted attachment, one mapped
  Kakao message and rendered synthetic image in A's official client. Independent
  SDK attachment download/decryption matched the original PNG SHA-256.
- A → Matrix photo: only the verified synthetic 64×64 PNG was granted through
  Android's limited-photo permission picker, then sent at Original quality.
  The resulting encrypted room event decrypted as `m.image`; its encrypted
  attachment decrypted to the exact original PNG bytes.
- Restart: stopped the normal bridge, sent one A text while offline, restarted
  with the same profile/database, and observed that message once. A restarted
  tester using its retained crypto database and device credentials decrypted it.
  A fresh encrypted Matrix text then reached A with one mapping and exact text.

The library owns Matrix cryptography, key/session storage, event encryption and
attachment encryption. This experiment validates the bridge's configuration and
integration with that library; it adds no cryptographic implementation.

The tester initially failed because JSON unmarshalling had not populated the
SDK's parsed encrypted-event content. Repeated tester password logins also
triggered local Synapse throttling. Both harness issues are covered by
regressions in `tools/matrix-lab`; see [the companion workflow](../matrix-lab.md).
The original message was retained and decrypted after the fix, without resend.

## Limits

This is local Synapse direct-room evidence, not a Beeper deployment test or a
claim of Kakao-side end-to-end encryption. The bridge handles plaintext between
Matrix and Kakao. Device verification, trust-policy transitions, key rotation,
missing-key recovery, encrypted replies/reactions, and group-room encryption
remain separate acceptance cases. Group tests still require a third owned
participant and remain pending at the maintainer's direction. No A secondary
login, new QR enrollment, or cloud backup/restore was attempted.

Private events, screenshots, mappings, runtime credentials and receipts remain
outside the repository. Public evidence describes only method and outcomes.

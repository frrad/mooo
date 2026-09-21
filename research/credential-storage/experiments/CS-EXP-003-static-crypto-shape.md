# CS-EXP-003 — static cryptographic shape

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: KakaoTalk for macOS 26.8.0, arm64
- Public artifact ID: CS-ART-MAC-ARM64-001
- Private artifact reference: CS-2/CS-3 KDF, cipher, and raw call-site notes
- Evidence class: static

## Question

Does the active candidate use the PBKDF2-based construction suggested by prior
art, and what cryptographic and envelope properties can be established by tracing
the confirmed helper pair?

## Authorization and safety boundary

The inspected executable is an authorized copy of the official client installed
on the maintainer's Mac. The analysis was offline and static. It did not launch the
client, read a stored value or real device identifier, access an account, contact a
server, or reproduce proprietary code.

Because the transformed value feeds automatic login, exact recipe parameters and
a runnable decryptor require the safeguards in the CS-5 publication decision. This
experiment records the static evidence that precedes clean-room transfer.

## Hypothesis

The active candidate uses the device UUID as input to PBKDF2 and stores an
authenticated encrypted envelope. Plausible alternatives were direct digest-based
key derivation and an unauthenticated block-cipher envelope.

## Method

Starting only at the helper pair reached by CS-EXP-002, trace the device identifier
to encoded bytes and the encryption/decryption operations through their concrete
CommonCrypto construction. Account for generation, splitting, length checks,
padding, base64 boundaries, and all integrity-related calls. Separately inspect the
binary's PBKDF2 helper and require a real caller edge before associating it with the
active path.

## Sanitized observation

The device identifier comes from the platform UUID property exposed by IOKit. The
active helper encodes that identifier and uses a direct cryptographic digest to
obtain key material. It does not call the separate PBKDF2 helper retained elsewhere
in the binary.

The cipher path uses padded AES in CBC mode with a newly generated IV. The stored
binary envelope is the IV followed by ciphertext; base64 conversion occurs outside
that envelope. The inverse path enforces a minimum envelope length, splits the same
two regions, and returns no value when decryption fails. No authentication tag,
separate MAC, associated data, or integrity verification was found in this path.

Exact encodings, lengths, and call arguments are recorded in the private evidence
set for synthetic confirmation and transfer review.

## Conclusion

The PBKDF2 and authenticated-envelope hypothesis is falsified with high confidence
for the active value in version 26.8.0. The direct-digest and unauthenticated
IV-plus-ciphertext alternatives are supported by complete static data flow.

CS-2 and CS-3 static recovery is complete. CS-EXP-004 subsequently confirmed the
derived bytes and round-trip behavior with independent invented-data
implementations; a safely isolated client-helper comparison remains.

## Cleanup

No application process, hook, preference domain, or temporary runtime artifact was
created. Raw static-analysis output remains in the private, ignored lab directory.

## Follow-up

Complete the remaining CS-4 client-helper comparison if it can be isolated safely,
then conduct the clean-room transfer authorized by CS-5.

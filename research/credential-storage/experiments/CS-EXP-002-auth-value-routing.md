# CS-EXP-002 — candidate authentication-value routing

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: KakaoTalk for macOS 26.8.0, arm64
- Public artifact ID: CS-ART-MAC-ARM64-001
- Private artifact reference: CS-1 call-graph notes and fixed-reference queries
- Evidence class: static

## Question

Which candidate preference key has an active encrypted read/write path in the
current client, what inputs surround its cryptographic transform, and does the
second historical candidate use the same recipe?

## Authorization and safety boundary

The inspected executable is an authorized copy of the official client installed
on the maintainer's Mac. The analysis was offline and static. It did not launch the
client, read a stored value or real device identifier, access an account, contact a
server, or reproduce proprietary code or the candidate constants.

## Hypothesis

At least one historical candidate is an active key for an encrypted value used by
authentication code. A deliberately tested alternative was that both candidates
share the same current recipe.

## Method

Recover optimized arm64 references to the complete constant objects, then follow
every caller through default-store access, byte/string conversion, cryptographic
helpers, and authentication consumers. Analyze read, write, automatic-login,
logout, and default-rebuild paths in both directions. Keep the fixed constant
contents and version-specific addresses private.

## Sanitized observation

One candidate selects an active value in the ordinary application defaults store.
Its read path retrieves a string, base64-decodes it to bytes, and decrypts those
bytes using the local device UUID as the cryptographic helper's second input. Its
write path encrypts input bytes using the device UUID, base64-encodes the result,
and stores that string under the same candidate key.

The automatic-login path performs the same retrieval, base64 decode, and decrypt
sequence, then converts the resulting bytes to hexadecimal text before passing
them onward. Logout and preference-reset paths remove the value.

This active path calls the same low-level encryption helpers as the generic secure
preference API, but does not pass through that API. The second historical candidate
is inspected or removed only by cleanup/reset logic in this version. No production
write or cryptographic path for it was found.

## Conclusion

The hypothesis is supported for the first candidate with high confidence. The
alternative that both candidates share the recipe is falsified for version 26.8.0:
the second candidate is cleanup-only. The result establishes the full routing
around the cryptographic helpers, but it does not yet establish how the device UUID
is encoded or transformed inside them, which KDF and cipher parameters are used,
or what the decrypted bytes represent.

CS-1 passes for the active candidate. The cleanup-only candidate is excluded from
CS-2 and CS-3 unless later evidence reveals a current producer.

## Cleanup

No application process, hook, preference domain, or temporary runtime artifact was
created. Raw static-analysis output remains in the private, ignored lab directory.

## Follow-up

CS-2 and CS-3 must begin at the confirmed device-UUID input and exact encryption
helper pair, recovering their byte encoding, KDF, cipher, and envelope data flow.

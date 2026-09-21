# macOS credential-storage investigation

This directory tracks the effort to determine how selected preference values are
transformed by recent KakaoTalk for macOS builds and what those values represent.
If they are encrypted, device-derived session material, a contingent outcome is a
versioned, implementation-neutral recipe that can be tested with synthetic data.
Reading a live value is a later validation step, not the analysis method.

## Scope

In scope:

- determine whether the candidate values use encryption, obfuscation, or another
  transform and whether device-derived input participates;
- if applicable, identify the key derivation, cipher mode, padding, IV/nonce,
  integrity mechanism, and stored envelope layout;
- document read, write, failure, and version-mismatch behavior;
- if the CS-5 publication gate passes, build synthetic test vectors and a guarded
  Go implementation;
- validate locally against a disposable, authorized profile when one is available.

Out of scope:

- extracting credentials from accounts or machines not owned by the operator;
- publishing live tokens, platform identifiers, ciphertexts, database contents, or
  other account-specific artifacts;
- contacting Kakao servers with an extracted credential during this investigation;
- recovering the SQLCipher message-database key, which is a separate problem;
- weakening SIP or other host-wide security controls for convenience.

## Documents

- `PLAN.md` is the detailed work breakdown and set of decision gates.
- `EVIDENCE.md` is the append-only public evidence and hypothesis ledger.
- `PUBLICATION.md` records the current CS-5 decision to withhold a turnkey recovery
  recipe while continuing synthetic and protocol research.
- `experiments/TEMPLATE.md` defines the required format for sanitized experiments.
- `experiments/CS-EXP-001-secure-preference-graph.md` establishes the secure
  wrapper's transform boundary and failure behavior.
- `experiments/CS-EXP-002-auth-value-routing.md` identifies the active candidate's
  device-UUID-bound read/write flow and excludes a cleanup-only legacy candidate.
- `experiments/CS-EXP-003-static-crypto-shape.md` falsifies the PBKDF2 and
  authenticated-envelope hypotheses without publishing the complete recipe.
- `experiments/CS-EXP-004-independent-round-trip.md` records agreement between Go
  and OpenSSL on an invented-only private vector.

Raw Ghidra output, offsets, decompiler text, local paths, preference values, and
runtime traces belong in the ignored `.lab/credential-storage/` companion folder.
Only reviewed behavioral conclusions move here under `../CLEANROOM.md`.

## Current status

The named preference strings, secure preference-access surface, platform-identifier
lookup, PBKDF2 helpers, and CommonCrypto calls described for macOS 26.4.1 remain in
the inventoried 26.8.0 binary. This makes the investigation a bounded data-flow and
crypto-parameter recovery task rather than an open-ended search.

Static tracing has established both the generic secure-setting transform boundary
and the active authentication-value path. The latter reads a base64 string from the
ordinary defaults store, decrypts the decoded bytes using the local device UUID,
and converts the result to hexadecimal text for automatic login; its write path is
the inverse. A second historical candidate is cleanup-only in version 26.8.0 and is
not assigned the active candidate's recipe.

The static cryptographic trace is now complete. It falsifies the prior PBKDF2 and
authenticated-envelope hypotheses: the active path uses direct digest-derived key
material and an unauthenticated IV-plus-AES-CBC-ciphertext envelope. Exact recipe
parameters remain private under the publication gate. The decoded data category is
not yet established by disposable-profile validation.

An invented-data Go implementation now agrees byte-for-byte with OpenSSL and
round-trips with strict padding checks. A safely isolated invocation of the actual
client helper remains the final CS-4 comparison.

Generic AES key/IV helper names cited by prior work are owned by backup and
session-transport components in the current runtime metadata. Their relevance to
preference encryption remains unproven. The investigation follows the actual
preference read/write path instead of joining nearby crypto clues by name.

Background:

- [OpenKakao credential-storage note, pinned revision](https://github.com/JungHoonGhae/openkakao-cli/blob/87743a438fa2eebf101f6df701790e9721fd6619/docs/research/credential-storage.md)
- [OpenKakao local-database investigation](https://github.com/JungHoonGhae/openkakao-cli/issues/38)

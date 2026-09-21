# Credential-recipe publication gate

Status: **approved for exact specification and guarded implementation**,
2026-09-20.

This is the CS-5 decision for the current evidence. The project exists to let
legitimate users operate their own KakaoTalk accounts through another client or
bridge. Reproducible storage and cryptographic documentation serves that goal even
when the protected value participates in authentication.

## What the evidence establishes

The encrypted defaults value is consumed by the automatic-login path after local
decryption and byte-to-text conversion. Its exact semantic field has not yet been
validated with a disposable profile, so implementations must treat it as sensitive
authentication material and must not log or transmit it implicitly.

Static analysis has recovered enough detail to write an implementation-neutral
recipe. Invented-data implementations agree on the recovered cryptographic shape;
an isolated client-helper comparison and disposable-profile validation remain.

## Legitimate interoperability uses

Potential authorized uses include:

- reproducing and testing client-compatible local storage behavior;
- explicitly importing an operator's own existing secondary-device state;
- bootstrapping or migrating a self-hosted bridge when the protocol requires it;
- validating a clean-room implementation against synthetic vectors.

The preferred product path remains independent secondary-device registration when
available. Publishing the local format does not require making it the bridge's
default login mechanism.

## Required safeguards

An exact public specification, synthetic vectors, and implementation may proceed
with these constraints:

- never publish live tokens, platform identifiers, ciphertexts, plaintexts,
  account data, or proprietary binary/decompiler output;
- require an explicit operator action and a user-selected profile or input file;
- use exact supported-version profiles and fail closed on mismatches;
- do not log recovered values, derived keys, device identifiers, or intermediate
  buffers;
- do not silently scan unrelated user profiles or perform bulk extraction;
- do not transmit recovered material to unrelated endpoints; using it in a
  documented Kakao authentication flow is allowed after explicit operator
  configuration;
- validate first with synthetic fixtures and then only with an authorized
  disposable profile;
- document that local decryption does not provide an independent integrity check
  and that recovered material may be reusable authentication state.

## Decision

The exact recipe may now pass through clean-room transfer review into a public,
versioned specification. Synthetic vectors and a guarded Go implementation are
also authorized. A convenience importer may be considered later if it preserves
the explicit-input, local-only, version-guarded model above.

This gate authorizes interoperability work; it does not authorize testing someone
else's account, machine, copied profile, or credentials.

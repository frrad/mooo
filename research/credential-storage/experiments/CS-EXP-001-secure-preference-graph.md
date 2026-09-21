# CS-EXP-001 — secure preference read/write graph

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: KakaoTalk for macOS 26.8.0, arm64
- Public artifact ID: CS-ART-MAC-ARM64-001
- Private artifact reference: CS-1 call-graph notes and raw static-query output
- Evidence class: static

## Question

Does the client's secure preference API introduce a reversible cryptographic
transform around the ordinary settings store, and what behavior is visible at its
read and write boundaries?

## Authorization and safety boundary

The inspected executable is an authorized copy of the official client installed
on the maintainer's Mac. The analysis was offline and static. It did not launch the
client, read a preference value, access an account or device identifier, contact a
server, or reproduce proprietary code.

## Hypothesis

The secure API encrypts bytes before sending them through the ordinary settings
persistence mechanism and decrypts stored bytes when reading them. A plausible
alternative was that the API merely selected a protected store or delegated the
transform to a different persistence layer.

## Method

Starting at the complete secure and ordinary preference API surfaces in Objective-C
metadata, follow the class-level read/write entry points, their synchronous storage
blocks, and the corresponding record updates in both directions. Compare the
secure and ordinary paths and account for every call that can transform the value.

The analysis intentionally did not infer relationships from nearby cryptographic
names. Candidate stored-value constants and their callers are a separate data-flow
question and are not treated as resolved by this experiment.

## Sanitized observation

Both write paths first require an available settings store and retrieve or create
the record selected by the caller's setting key. The ordinary path assigns the
input bytes to that record unchanged. The secure path first passes the bytes and a
second caller-supplied input through a dedicated encryption helper, then assigns
the helper's result through the same record-update path.

The secure read path retrieves an existing record without creating it, reads its
stored bytes, and passes those bytes plus the caller-supplied second input through
the matching decryption helper. It returns that result directly. At this wrapper
layer, an unavailable store, missing record, missing stored value, or failed
decryption all produce no returned value; no more specific failure is exposed.

No other transform-producing call occurs between the secure API boundary and the
record value in either direction.

## Conclusion

The hypothesis is supported with high confidence for the versioned binary. The
secure preference API adds an explicit, reversible encryption boundary around the
ordinary persistence path. The cipher, key derivation, envelope, and the mapping
of candidate setting constants to the two API arguments remain unresolved, so
this experiment does not establish a complete recovery recipe.

## Cleanup

No application process, hook, preference domain, or temporary runtime artifact was
created. Raw static-analysis output remains in the private, ignored lab directory.

## Follow-up

Complete CS-1 by recovering the candidate constants' callers and exact argument
positions. Then trace the confirmed encryption helpers for CS-2 and CS-3.

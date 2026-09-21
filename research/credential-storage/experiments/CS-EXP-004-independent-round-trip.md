# CS-EXP-004 — independent invented-data round trip

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: KakaoTalk for macOS 26.8.0 recipe under study
- Public artifact ID: CS-ART-MAC-ARM64-001
- Private artifact reference: CS-4 synthetic source, vector, and execution record
- Evidence class: synthetic

## Question

Do two independent implementations agree when the private CS-2/CS-3 specification
is applied to entirely invented inputs?

## Authorization and safety boundary

The experiment used an obviously invented platform identifier, plaintext, and
deterministic IV. It did not launch KakaoTalk, read any preference domain or device
identifier, access an account, contact a server, or modify an official application
file. The exact vector remains in the private evidence set until clean-room transfer
under the safeguards approved by CS-5.

## Hypothesis

A Go implementation and an independent OpenSSL invocation will produce identical
ciphertext for the same invented inputs, and the Go implementation will recover
the original plaintext after strict padding validation.

## Method

Encode the invented identifier and apply the private derivation and cipher
specification in a small Go reference. Pin the resulting envelope as a known vector,
then decrypt it and validate both its padding and original plaintext. Independently
run OpenSSL with the derived invented key and IV and compare its ciphertext bytes
with the ciphertext region produced by Go.

## Sanitized observation

The Go known-vector assertion passed, its decrypt operation recovered the complete
invented plaintext, and strict padding validation succeeded. OpenSSL produced the
same ciphertext bytes as Go. No live or account-derived bytes participated.

## Conclusion

The hypothesis is supported. Independent implementations agree on the static
interpretation and envelope byte ordering. Confidence is high for those properties.

This is a partial CS-4 result: neither implementation is the client itself. A pure
client-helper comparison with invented bytes remains the last synthetic confirmation
step if it can be isolated without reading or writing the official preference
domain.

## Cleanup

No application process, hook, preference domain, or temporary runtime artifact was
created. The private source and vector remain in the ignored lab directory.

## Follow-up

Design an isolated invented-only invocation of the client's helper, or document why
the independent CommonCrypto-equivalent confirmation is the safest stopping point.

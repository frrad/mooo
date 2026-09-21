# Credential-recipe publication gate

Status: **hold exact recipe and implementation**, 2026-09-20.

This is the CS-5 decision for the current evidence. It may be revisited when the
secondary-device protocol establishes a concrete interoperability need.

## What the evidence establishes

The encrypted defaults value is consumed by the automatic-login path after local
decryption and byte-to-text conversion. Its exact semantic field has not yet been
validated with a disposable profile, but it is plainly credential-adjacent and
must be treated as reusable authentication material until disproved.

Static analysis has recovered enough detail to implement local decryption. That
does not establish that moving the result off the Mac is necessary, safe, accepted
by Kakao's service, or useful to a Linux-hosted bridge.

## Usefulness and alternatives

The project's primary goal is an independently registered secondary device, not
session extraction from an existing Mac. Continuing the official QR/device
registration and LOCO authentication investigation can meet that goal without
turning local at-rest protection into a public recovery feature.

If local state later proves necessary, narrower designs must be considered first:

- an explicit Mac-side helper that never exports the recovered secret;
- an official registration flow that provisions the bridge independently;
- an operator-supplied, version-bound migration operation with no implicit home
  directory or defaults-store scanning.

## Abuse and portability

A complete recipe plus a general scanner would make it easier to recover automatic
login material from copied user profiles. The key input is tied to the originating
Mac, which limits naive portability but does not remove that risk when local device
metadata is also available. The current envelope has no independent integrity
check, so a public implementation would also need strict failure handling and
could not claim authenticated decryption.

## Decision

For now:

- publish versioned behavioral conclusions, falsified hypotheses, and synthetic
  methodology;
- retain exact constants, complete parameter tables, vectors, and runnable
  recovery code in the private lab;
- do not read or decrypt a live value merely to identify it;
- do not build a general defaults-store scanner or credential exporter;
- continue protocol registration and authentication research independently;
- revisit this gate only if a concrete bridge requirement cannot be met through a
  narrower flow.

This decision does not block invented-data confirmation. It blocks transfer of a
turnkey recovery recipe into the public implementation.

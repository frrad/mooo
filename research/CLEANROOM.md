# Provenance and contamination-control policy

This project uses a clean-room-inspired workflow to reduce accidental copying,
secret exposure, and provenance ambiguity. It is an engineering safeguard, not
legal advice or a claim that the project satisfies a legally definitive clean-room
standard.

## Lanes

### A: private analysis

Authorized researchers may study lawfully obtained clients, binaries, emulator
behavior, logs, and traffic. Raw binaries, disassembly, decompiler output, Ghidra
projects, captures, databases, screenshots, identifiers, tokens, and keys stay
outside the repository.

The output of this lane is an implementation-neutral behavioral specification:
states, field meanings, encodings, error behavior, preconditions, postconditions,
confidence, and sanitized provenance. It must not preserve proprietary expression
such as copied code, pseudocode, internal names, offsets, or assets.

### B: public implementation

Whenever practical, an implementation agent or contributor receives only approved
behavioral specifications, public documentation, and synthetic tests. Implementation
changes should cite spec or experiment IDs, not binary offsets or internal symbols.

An implementation contributor must not use leaked source code or proprietary code.
Someone exposed to such material should not implement the affected module.

### C: transfer review

Before material moves from private analysis into the public repository, review it
for copied expression, secrets, identifying data, private message content, and
unnecessary abuse-enabling details. Record:

- a non-sensitive artifact or experiment ID;
- date and source class (`black-box`, `public`, or `binary-analysis`);
- reviewer;
- whether redaction and expression review passed;
- the public specification sections informed by it.

Hashes of sensitive artifacts may be recorded in a private manifest, but sensitive
artifacts and identifying filenames must not be published.

## Agent isolation

Use fresh agent sessions and separate worktrees when separating analysis from
implementation. A target-visible analysis agent must not pass raw artifact paths,
decompiler output, or hidden conversation context to a spec-only implementation
agent. Agents share a host, so this is process isolation rather than a hard security
boundary.

## Public material

Suitable public material includes original Go code, behavioral specifications,
synthetic fixtures, generic configuration, sanitized experiments, and instructions
that require operators to supply their own official installation and account.

## Private material

Keep proprietary binaries and assets, decompiler/disassembly output, raw captures,
databases, screenshots, personal messages, account/device identifiers, phone
numbers, session credentials, certificates, private keys, QR payloads, and other
live cryptographic material outside the repository.

## Limitations

A strict clean room commonly uses genuinely independent people. A single maintainer
who both studies a binary and writes the implementation cannot claim that degree of
independence. The workflow still improves hygiene and makes later independent review
possible. Seek qualified counsel if legal exposure or commercial distribution makes
that assurance important.

## Background

- [17 U.S.C. § 1201(f), reverse engineering for interoperability](https://www.copyright.gov/title17/92chap12.html)
- [WIPO, Protecting Your Mobile App](https://www.wipo.int/edocs/pubdocs/en/wipo_pub_1071.pdf)
- [ReactOS intellectual-property guideline](https://reactos.org/intellectual-property-guideline/)

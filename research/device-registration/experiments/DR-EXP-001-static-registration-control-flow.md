# DR-EXP-001 — static registration control flow

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: KakaoTalk for macOS 26.8.0, arm64
- Public artifact ID: DR-ART-MAC-ARM64-001
- Private artifact reference: registration Ghidra query corpus and analysis report
- Evidence class: static

## Question

Which operations, timers, semantic results, and success handoffs make up current
macOS secondary-device registration, and which wire details remain unproven?

## Authorization and safety boundary

The inspected executable is an authorized copy of the official client installed
on the maintainer's Mac. Analysis was offline and logged out. It did not access an
account, read stored identifiers or credentials, decode a live QR value, submit a
request, or reproduce proprietary implementation text.

## Hypothesis

Passcode and QR are presentations over one approval lifecycle, with QR adding an
independent new-device authorization phase. A plausible alternative was that their
timers, cancellations, and success credentials were unrelated flows.

## Method

Inventory registration route literals and controller metadata, then trace their
timer, callback, cancellation, result, and success branches. Associate request
builders only where data flow or compiler-emitted literal shape supports it. Treat
generic login/account operations as unrelated unless a caller establishes use.

## Sanitized observation

Seven passcode/QR routes form the registration surface. Passcode keeps display
expiry and approval polling separate. QR keeps display expiry, approval polling,
and device-authorization expiry separate. Both clear transient state and cancel
active work on close or expiry.

QR response control flow distinguishes pending, unregistered-device authorization,
rejection, expiry, unsupported version, suspension, account restriction, invalid
response, unknown failure, and success. Success carries permanent/temporary
semantics and can hand permanent login to a restore-or-skip choice before normal
session login.

Candidate request builders contain login identifier, password, permanence, device,
UUID-like identity, and previous-identifier fields. Indirect dispatch prevented a
defensible recovery of exact schemas, numeric statuses, signing, QR encoding,
polling cadence, persistence, and revocation.

## Conclusion

The hypothesis is supported for the common lifecycle shape with high confidence.
The route family, independent timers, semantic result categories, cleanup rules,
and success modes passed transfer review. Candidate field associations are medium
confidence and are documented as non-implementable wire leads. The remaining gaps
stay explicit rather than being filled from historical clients.

## Cleanup

Ghidra ran read-only with analysis disabled. Raw queries and binary-specific notes
remain in ignored/private lab storage. No client process, network request, account
state, or tracked raw output was created.

## Follow-up

Trace the common HTTP encoder and indirect response models using invented-value
instrumentation where safe, then trace success into persistence and LOCO login.

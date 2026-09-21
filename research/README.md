# Research

This directory contains sanitized, source-backed research. It must never contain
credentials, tokens, phone numbers, account or device identifiers, private message
content, raw captures, decrypted databases, or proprietary binaries.

## Note format

Each experimental note should record:

- date and researcher;
- exact client/platform versions;
- question and hypothesis;
- method and authorization boundary;
- sanitized observation;
- confidence and alternative explanations;
- follow-up experiment;
- provenance or public source links.

Sensitive raw artifacts live outside the repository and are referred to only by a
non-sensitive experiment identifier.

## Current documents

- `prior-art.md` surveys public protocol and bridge work.
- `client-inventory.md` records sanitized client baselines.
- `reversing-strategy.md` selects the primary target and defines the first analysis
  work packages.
- `protocol-bootstrap.md` records the initial packet, serialization, and secure
  transport observations.
- `session-login/` tracks the vertical slice from completed registration through
  LOCO authentication and reconnect.
- `device-registration/` specifies secondary-device registration, including the
  recovered passcode and QR state machines, evidence, and open experiments.
- `credential-storage/` determines how candidate macOS preference values are
  transformed and, if applicable, specifies a versioned local recovery recipe.
- `CLEANROOM.md` defines provenance and contamination controls.

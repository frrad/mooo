# DR-EXP-004 — device name source and fallbacks

- Date: 2026-10-16
- Researcher: Claude (agent session), for the maintainer
- Client/platform version: KakaoTalk for macOS 26.8.0 (arm64 slice)
- Public artifact ID: DR-EXP-004; evidence DR-MAC-025 to DR-MAC-030, DR-OBS-031
- Private artifact reference: external Ghidra parity directory `device-name-2026-10-16`
- Evidence class: static (binary analysis), plus one earlier owned-account observation

## Question

Which macOS source, fallbacks and transforms produce the `device.name` that the
Mac client sends when registering as a secondary device, and which requests
carry it?

## Authorization and safety boundary

Static reading of the authorized, logged-out official Mac binary only. No client
launch, account use, network activity or instrumentation in this session. No
host name, computer name, device identifier or other lab value is published.

## Hypothesis

H1: the name is the host name with the domain removed (the assumption behind
PR frrad/mooo#413). H2: the name is the user-facing Computer Name. H3: the name
is a model or product string.

## Method

1. Started from the previously mapped passcode-generate and QR-generate request
   builders (DR-BIN-009) and followed the call that produces their full `device`
   object.
2. Disassembled that helper. Recorded its four keys, the selector each value
   comes from, and how a `nil` result is handled.
3. Resolved each selector's implementation in the main executable and
   disassembled it, identifying the system APIs and branch structure.
4. Listed every call site of the Computer Name selector and of the full-device
   helper to find all consumers.
5. Checked the LOGINLIST field table (`../session-login/PROTOCOL.md`) for any
   name field.
6. Compared with mooo's QR login, `authstate` and the open PR frrad/mooo#413 diff.

## Sanitized observation

- The full `device` object is built on every call from four class-level helpers:
  Computer Name, hashed UUID, OS version and model. A `nil` name, UUID or model
  becomes an empty string. The OS version is never `nil`.
- The Computer Name helper returns `SCDynamicStoreCopyComputerName(NULL, NULL)`
  when it is non-`NULL`. Otherwise it returns the model helper's result.
- The model helper returns `sysctlbyname("hw.model")` decoded as UTF-8, or `nil`
  when the reported size is zero.
- The OS-version helper formats `operatingSystemVersion` as three integers
  joined by dots.
- The helper output passes through a type conversion that leaves values
  unchanged. There is no truncation, filtering, prefix, suffix or localization.
- The full-device helper is called only by passcode generate and QR generate.
  Three other account routes (email login and two sign routes) read the same
  Computer Name helper directly into a flat `device_name` key.
- LOGINLIST has no name field.

## Conclusion

H2 is supported, and H1 and H3 are falsified for the primary source. The
confidence is high for the source order and consumers, because every link is a
direct call with literal keys. The confidence is medium for the never-taken
branches (empty-string fallbacks), because they are static only. DR-OBS-031
corroborates the present-Computer-Name case.

## Cleanup

No processes, hooks or client state were created. The private disassembly and
metadata extracts stay outside the repository.

## Follow-up

Write an executed lab harness that hooks the two system sources in the
instrumented debug copy and calls the full-device helper, so that the fixture
becomes `executed`. Implement the recommendations in `../DEVICE-NAME.md` in a
separate mooo change.

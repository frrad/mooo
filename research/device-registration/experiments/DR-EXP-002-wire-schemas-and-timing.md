# DR-EXP-002 — registration wire schemas and timing

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: KakaoTalk for macOS 26.8.0, arm64
- Public artifact ID: DR-ART-MAC-ARM64-001
- Private artifact reference: targeted registration-wire Ghidra report and queries
- Evidence class: static

## Question

Can each registration route be tied directly to its request fields, QR result
codes, payload handling, and polling rules without observing a live account?

## Authorization and safety boundary

The analysis used only an authorized local executable and read-only Ghidra project.
It did not launch the client, access an account, read credentials or device values,
decode a live QR value, or contact a server.

## Hypothesis

The route builders and controller response branches retain enough typed literals
to recover the operation-specific semantic contract. An alternative was that Swift
generic dispatch erased all associations below the UI state machine.

## Method

Starting from the seven established route literals, trace direct references into
their request builders and nested device construction. Trace QR generation and
poll callbacks into URL parsing, typed error data, numeric branches, and scheduler
calls. Require a direct local association rather than ordering or proximity.

## Sanitized observation

All seven routes were directly tied to the request field and device-object shapes
listed in `../PROTOCOL.md`. QR content is a server-provided opaque URL-like string;
the complete string is rendered while query parameter `id` is retained for later
poll/cancel requests.

QR codes `1`, `5`, `13`, `14`, `15`, `16`, `20`, and `29` map respectively to
unregistered-device authorization, suspension, unsupported version, pending,
rejection, expiry, restriction, and invalid response. Unknown codes fail closed.
Typed response data carries next-poll delay, device-auth passcode and lifetime,
and nested status/message data.

Countdowns tick every second. Passcode polling is clamped to at least three
seconds. QR polling starts at three seconds, later honors positive server delays,
and falls back to three seconds for non-positive delays. Device-authorization
delays below one second also become three seconds.

## Conclusion

The hypothesis is supported for route ownership, request keys/device shapes, QR
payload handling, numeric QR errors, and timer policy. HTTP verb and encoding,
shared headers/base URL/signing, full success schemas, check-key validation, and
passcode numeric statuses remain blocked by indirect generic dispatch.

## Cleanup

Raw decompilation, addresses, and binary-specific outputs remain in private lab
storage. No application or network state was created.

## Follow-up

Recover the common HTTP abstraction with invented-value instrumentation or another
static representation, and map passcode response models separately.

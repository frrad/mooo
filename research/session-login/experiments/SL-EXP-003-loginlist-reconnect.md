# SL-EXP-003 — LOGINLIST and reconnect

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: KakaoTalk for macOS 26.8.0, arm64
- Public artifact ID: SL-ART-MAC-ARM64-001
- Private artifact reference: targeted LOCO login/reconnect static-analysis report
- Evidence class: static

## Question

What exact request authenticates the carriage session, what state does it install,
and when does recovery reconnect versus terminate?

## Authorization and safety boundary

Offline static analysis used an authorized local executable and invented/control-
flow observations only. No client launch, token, identifier, endpoint, or traffic
was accessed.

## Hypothesis

The carriage authenticates with LOGINLIST using existing auth state and cursor
inputs, while recovery reruns bounded bootstrap/login and treats route changes and
security kickouts separately.

## Method

Trace the common login coordinator through request construction, BSON property
types, response predicates, state installation, endpoint cache, retry/failover,
recovery gates, change-server, kickout, and cleanup paths.

## Sanitized observation

The final command is `LOGINLIST` with the 17 typed fields in `../PROTOCOL.md`.
Current construction leaves `sKey` unset and places the prepared access-token
string unchanged in `oauthToken`. Status zero and `-305` are accepted login
success; `-310` is generic partial success and `-445` is login-blocked.

Successful login installs session, routing, voice, chat-list, revision, and cursor
state. Cached routing is validated and cleared after matching-endpoint failure.
Booking and ticket retry paths are bounded. Ordinary recovery preserves chat and
token cursors, rejects stale/concurrent/unusable-auth attempts, and reruns normal
login. Change-server clears routing; kickout logs out and may request database reset.

## Conclusion

The hypothesis is supported. Serializer omission rules, full status/reason labels,
response key remapping, exact recovery delays, and catch-up boundaries remain open.

## Cleanup

Raw Ghidra output remains in private lab storage. No runtime state changed.

## Follow-up

Run an invented-value serializer comparison, implement pure typed models and state
transitions, then defer live validation until account protection has aged out.

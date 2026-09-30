# Terminal push-notice wire/model boundary

Status: first-party static evidence and synthetic decoder contracts, 2026-09-30.
This note covers model construction only. It does not authorize or describe
automatic logout, database reset, reconnect, or server-address changes.

## Proven model shape

The official unsolicited-packet dispatcher recognizes `CHANGESVR` and
`KICKOUT`, constructs their typed push-notice models from the packet body, and
then calls the manager delegate. The decoder boundary is therefore earlier
than the lifecycle callbacks.

`CHANGESVR` has no model-owned fields beyond the common push-notice base. No
meaningful payload is consumed by the traced manager callback. A valid empty
BSON document is consequently a sufficient synthetic body for the typed
event; any later route-cache or session action belongs to orchestration, not
decoding.

`KICKOUT` adds one field, `reason`, represented by the official model as a
signed 32-bit integer. The model's zero-initialized value is the observable
default when the body omits that field. A synthetic reason value of `10` is
used in tests; it is not a claim that every reason has the same lifecycle
meaning. The decoder exposes the integer to downstream code without deciding
what it means.

The body is a BSON document. Invalid BSON for either recognized method is a
malformed event. A method outside the recognized set remains an
`UnknownPacket`, even when its bytes are not a valid BSON document. This keeps
unknown and malformed observations distinct and prevents a decoder from
silently turning an unrelated method into a lifecycle event. Static evidence
did not establish server-side coercion rules for a wrong BSON numeric width,
so this contract deliberately does not publish one.

## Evidence trail

- Client: official macOS KakaoTalk 26.8.0 arm64, inspected 2026-09-30.
- Method: read-only Ghidra Objective-C metadata/decompilation of the packet
  dispatcher, the two manager handlers, and the push-notice model metadata.
- Model observation: the CHANGESVR subclass has no own ivars or properties;
  the KICKOUT subclass has one signed 32-bit `reason` property.
- Chain observation: both models are passed to manager delegate callbacks;
  lifecycle consumers are downstream of that callback boundary.
- Transfer: `internal/protocol/events/terminal_test.go` uses only synthetic
  BSON and checks typed shape, absent-field default, malformed input, and
  unknown-method preservation.

Private binary names, addresses, decompiler output, and account-specific
artifacts are intentionally omitted.

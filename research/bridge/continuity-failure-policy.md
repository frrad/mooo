# Unidentifiable message continuity failures

This policy covers a `MSG` packet for which the client cannot establish one
unambiguous chat and log identity. It is a delivery-admission failure, not a
normal disconnect: the connector reports the stable
`kakao-unidentifiable-message` bridge error, finishes cleanup of the current
profile owner, and does not automatically reconnect. An operator must inspect
the preserved profile/checkpoint and choose the recovery action. The bridge
does not invent a cursor, skip the packet, or replay an outbound mutation.

The same classification applies while recovering missed events before live
subscription. Catch-up therefore cannot move on to live delivery after an
unidentifiable message. The operator should preserve the profile and
checkpoint, inspect client compatibility and logs for the source of the
identity break, then explicitly reconnect only after deciding how to recover;
the bridge currently provides no automatic repair or recovery UI. A message whose identity is validated but whose
content is deterministically unsupported follows the separate message-gap or
conversion-notice policy and can be committed through the normal framework
path. Malformed non-`MSG` packets remain diagnostics and do not interrupt
delivery by themselves.

The error text and bridge state intentionally omit packet contents, URLs,
tokens, and other wire data. Tests cover live delivery, catch-up, ordinary
non-message parsing, and cleanup timeout retry. Controlled live account
validation and an operator recovery UI remain open work; this document does
not claim Kakao parity beyond the source-derived identity checks.

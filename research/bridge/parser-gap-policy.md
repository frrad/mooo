# Parser gap policy

Status: implementation policy for the Kakao/Matrix bridge alpha.

The strict protocol decoder remains the source of typed events. Delivery uses a
separate boundary that may recover only a validated message envelope: a
positive `chatId`, positive `logId`, and positive message `type` with no
duplicate or conflicting identity fields. The delivery boundary never retains
malformed BSON, attachments, URLs, parser text, or other payload fragments.

When a message payload is malformed but its envelope identity is unambiguous,
the client emits a `MessageGap`. Live delivery and `CatchUp` use the same
boundary, so both paths preserve the message position. The connector converts
that event into a deterministic Matrix notice carrying source metadata and a
bounded `ConversionGap` category. The notice must be handled successfully
before `CommitEvent` advances the per-chat checkpoint; a queue or conversion
failure leaves the event replayable and blocks later commits in that chat.

If the envelope is missing, duplicated, conflicting, non-positive, or wrongly
typed, the client fails closed. It reports the decode error, stops admitting
later live packets, and interrupts the owned session when that callback is
available. It does not invent a cursor or advance durable state. Catch-up
returns the error without a synthetic gap, leaving the existing checkpoint for
operator-visible recovery. The operator recovery and bounded-gap UX for this
case remains an explicit follow-up.

Malformed non-message packets remain non-fatal decoder results and do not stop
delivery. Unknown message kinds with a valid envelope retain their existing
unsupported-message behavior. Sender and timestamp fields are optional
attribution: malformed or absent values become zero, while chat and log
identity remain mandatory for a recoverable gap.

This policy is source-independent implementation behavior. It does not claim
official-client parity for malformed payload handling, arbitrary message
subtypes, or server-side recovery semantics.

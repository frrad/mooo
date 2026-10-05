# Bridge conversion failure policy

Status: implementation policy for the Kakao/Matrix bridge alpha.

When a Kakao photo cannot be rendered because of a deterministic, validated
source condition, the connector creates a short Matrix notice as the converted
content of the original remote event. The framework therefore keeps the
original stable source message identity. Its persisted source metadata retains
the chat, log, author, and photo type, plus a bounded `ConversionGap` category.
The category is diagnostic state; it does not contain a URL or raw error text.
Once notice handling succeeds, the existing framework commit path may advance
the Kakao cursor. Notice delivery failure leaves the event uncommitted and
replayable.

The deterministic categories currently cover expired attachment metadata,
invalid advertised size or media type, unsafe source URL policy, checksum
mismatch after a complete bounded body, and bytes that are not a supported
image. A supported inbound image declares `image/jpeg`, `image/jpg`, or
`image/png`; the decoded bytes must agree with that declaration. Other image
types and malformed or mismatched bytes produce a notice instead of an
ambiguous retry.

Transport and infrastructure failures remain transient: cancellation, network
failure, timeouts, CDN unavailability, truncated bodies, and Matrix upload
failure do not advance the cursor. They are replayed by the normal framework
queue. This policy does not claim coverage for parser failures before a typed
photo event, other message subtypes, OpenChat-specific behavior, or live CDN
encoding and availability. Those remain explicit follow-up gaps.

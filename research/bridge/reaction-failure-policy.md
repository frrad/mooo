# Outbound reaction failure policy

An outbound reaction mutation is attempted once. The bridge never retries it
automatically, because a transport failure can occur after Kakao accepted the
mutation.

The connector exposes a stable, redacted error category alongside its existing
generic compatibility sentinel:

| Evidence | Category | Operator meaning |
| --- | --- | --- |
| Kakao returned an HTTP or protocol rejection | outcome unconfirmed (`ErrRejected`) | The response does not prove whether the requested state was applied; do not retry automatically. HTTP 500 and a nonzero Kakao status share this category. |
| Transport, cancellation, deadline, or malformed response | outcome unknown (`ErrTransport`, `ErrInvalidResponse`, or context identity when recognized) | The mutation result cannot be established; do not retry automatically. |
| Request validation failed before a valid mutation request | invalid request | No mutation was attempted when the protocol contract establishes local rejection. |

Raw backend errors are not surfaced because they may contain URLs, response
bodies, authorization material, or implementation-specific details. Safe
sentinel identities remain available for tests and redacted caller handling.

Reaction removal sends no lookup. Like the official client, which cancels from
its locally stored own pick, the bridge compares the redacted reaction with
this login's stored legacy selection on the message (its bridged legacy
reaction row). It sends the cancellation only when they match; a stored
selection that a Kakao push has since replaced makes the redaction stale, and
nothing is sent. Two different stored legacy selections are an inconsistent
store: removal returns `ErrLookupFailed` and sends nothing. Cancellation follows
the same single-attempt mutation policy and uses the mutation categories above.
Owned acceptance (2026-10-15, B bridge, A/B/C group): a Matrix redaction of the
bridge user's thumbs-up sent one cancel POST and no members read, and the
reaction disappeared from the stored rows.

The categories describe the observable result at the bridge boundary. They do
not claim that an HTTP rejection means Kakao's state is unchanged.

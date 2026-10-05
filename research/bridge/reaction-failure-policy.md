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

Reaction-removal first performs a read-only membership lookup. A lookup failure
returns a distinct `ErrLookupFailed` category and sends no cancellation
mutation. If the lookup confirms the authenticated user's reaction,
cancellation follows the same single-attempt mutation policy and uses the
mutation categories above.

The categories describe the observable result at the bridge boundary. They do
not claim that an HTTP rejection means Kakao's state is unchanged.

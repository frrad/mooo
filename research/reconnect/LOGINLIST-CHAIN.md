# Post-connect LOGINLIST carriage chain

Status: static source supplement, 2026-10-02. This document scopes ordering to
one LOGINLIST request path; it does not claim that this is the first keep-alive
schedule among concurrent session activity.

After the login status callback accepts its observed numeric status set, the
coordinator updates session/configuration progress and invokes the login-list
coordinator with its stored chat-ID and max-ID collections. The login-list
request path obtains the LOCO manager and enters the carriage-connect/request
sequence.

On a successful carriage completion, the client constructs the LOGINLIST
request from the session and configuration fields, writes manager status
`0x18`, and invokes the carriage request wrapper. That wrapper queues delayed
PING cancellation before its carriage-agent admission and transport dispatch.
For this path, the ordinary request completion queues delayed PING scheduling
before entering the LOGINLIST response callback. This proves the ordering
within the ordinary transport completion path even when the callback later
receives nil or an error; it does not establish when the queued work executes.

The carriage-agent admission boundary is narrow: when the current carriage
agent is absent and the manager status is not `0x17`, the wrapper reports an
error without entering the ordinary send path. A nonnil current agent bypasses
that status check. The behavior of attempting transport with a nil agent while
status is `0x17` remains untraced.

The LOGINLIST transport completion forwards a nil response as nil. For a
non-nil packet it creates a response wrapper. A login-success response enriches the response wrapper and settings state.
The wrapper receives carriage host and port from the manager carriage address;
the core settings object receives last-carriage host and port from that same
address; and the wrapper receives voice-service IPv4 host, IPv6 host, and port
from manager-owned voice-service values. Token and blind-token reads come from
the wrapper and their progress updates are additionally gated by response
success and end-of-list predicates.
The supplied completion receives the resulting wrapper after those effects.

Observed: login-status admission into LOGINLIST; request construction and
manager status write; cancellation before carriage dispatch; ordinary transport
completion scheduling before LOGINLIST callback delivery; nil forwarding;
non-nil response wrapping; login-success route updates; and success-plus-EOF
token advancement.

Implementation decisions: represent these effects as a pure ordered policy
trace with injected transport outcomes; keep route and token persistence
separate from request construction; preserve the transport completion's
schedule-before-callback ordering.

Untraced: actual queue execution time, first schedule among competing traffic,
response wrapper field mapping, exact error object contents, and the nil-agent
status-`0x17` transport outcome.

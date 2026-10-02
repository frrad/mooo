# LOGINLIST response callback boundary

Status: static source supplement, 2026-10-02. This continues the scoped
LOGINLIST carriage chain and describes callback ordering only.

For a non-nil transport packet, the callback first constructs a response
wrapper and asks it for the login-success predicate. It writes manager status `0x19` on the login-success branch and `0x18` on
the non-success branch.

On the login-success branch, the response wrapper receives carriage host and
port from the manager's current carriage address. The core settings object
receives last-carriage host and port from that same address. The response
wrapper then receives voice-service IPv4 host, IPv6 host, and port from
manager-owned voice-service values. These values are not extracted from the
server response packet. The non-success branch does not perform those route
copies.

After the route branch, independently of the login-success predicate, the
callback checks response success and end-of-list. Only when both predicates
hold does it invoke the token-progress sequence: set token-lock state to
`false`, read `wrapper.lastTokenId`, update the token, read
`wrapper.lastBlindToken`, and update the blind token. It then forwards the wrapper to the
supplied completion. A nil packet bypasses wrapper construction and forwards
nil directly.

Observed: wrapper-before-predicate ordering; status `0x19` on login success and
`0x18` otherwise;
wrapper/settings route-copy order on login success; independent
success-plus-EOF token branch and its ordering; nil forwarding; and completion delivery after these effects.

Implementation decisions: model route copies and token progress as ordered
policy effects; keep packet wrapper parsing separate from manager/settings
state; treat setter durability and downstream consumers as separate interfaces.

Untraced: response wrapper field mapping, setter persistence beyond direct
state assignment, downstream settings consumers,
and callback behavior if a setter or persistence operation fails.

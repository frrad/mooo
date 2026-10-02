# LOGINLIST response callback boundary

Status: static source supplement, 2026-10-02. This continues the scoped
LOGINLIST carriage chain and describes callback ordering only.

For a non-nil transport packet, the callback first constructs a response
wrapper and asks it for the login-success predicate. It writes an internal
manager status on both predicate branches. The exact numeric status arguments
were not recovered and remain intentionally unspecified.

On the login-success branch, route fields are copied from the manager's current
state into its configuration/settings target in this order: carriage address
host, carriage address port, carriage address host as the stored last host,
carriage address port as the stored last port, voice-service IPv4 host,
voice-service IPv6 host, and voice-service IPv4 port. These values are read from
manager-owned address objects; they are not fields extracted from the server
response packet. The non-success branch does not perform those route copies.

After the route branch, the callback checks response success and end-of-list.
Only when both predicates hold does it invoke the token-progress sequence:
set the token-lock state, read the current token, update the token, read the
blind token, and update the blind token. It then forwards the wrapper to the
supplied completion. A nil packet bypasses wrapper construction and forwards
nil directly.

Observed: wrapper-before-predicate ordering; status write on both branches;
manager-state route-copy order on login success; success-plus-EOF token branch
and its ordering; nil forwarding; and completion delivery after these effects.

Implementation decisions: model route copies and token progress as ordered
policy effects; keep packet wrapper parsing separate from manager/settings
state; treat setter durability and downstream consumers as separate interfaces.

Untraced: exact status numeric values, response wrapper field mapping, setter
persistence beyond direct state assignment, downstream settings consumers,
and callback behavior if a setter or persistence operation fails.

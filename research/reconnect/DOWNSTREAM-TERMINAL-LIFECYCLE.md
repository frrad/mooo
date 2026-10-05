# Downstream terminal lifecycle

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

The reviewed `logoutForChangeServer` body clears the cached carriage address,
checks for another ticket address, advances that cursor only when one exists,
and then sends `logout`. The downstream manager `logout` implementation
disconnects the ticket agent and then the carriage agent. This establishes the
route-change teardown ordering; it does not establish whether a new booking or
login is automatically started afterward.

The higher reset path `logoutWithResetDatabase:` removes three notification
observers, clears the registration request, deletes temporary media, cancels
core work, closes windows, conditionally clears calendar state, invokes the
core pre-logout reset hook, truncates/logout-cleans the database, and finally
clears the logging-out flag. The exact boolean-to-calendar/reset policy and
observer payloads remain gaps because the reviewed call chain does not expose
their implementations.

The `locoDidKickout:` consumer is a separate downstream chain. It requires a
logged-in, non-logging-out state, extracts the signed reason and optional error
metadata, derives the reset choice for reasons 1 and 10, invokes
`logoutWithResetDatabase:`, then directly queues its notification projection
on the main queue. The notification projection creates error type `0x24`,
always includes the reason, and includes each optional error field only when
its getter returns a non-null object. Observer behavior after posting remains
untraced.

## Provenance

- `logoutForChangeServer`: IMP `0x10151869c`.
- downstream manager `logout`: IMP `0x1015185d0`.
- `logoutWithResetDatabase:`: IMP `0x1013aba20`.
- `locoDidKickout:`: IMP `0x1013ac5e8`.
- raw receipts: private lab `cs1` disassembly at the corresponding IMP ranges;
  selector/call inventory is in the private `downstream-logout/report.txt`.

The synthetic order fixture is intentionally transport-, storage-, and UI-
neutral. It records only statically observed calls and keeps unresolved policy
choices explicit.

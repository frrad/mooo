# Downstream terminal lifecycle

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

The reviewed `logoutForChangeServer` body clears the cached carriage address,
checks for another ticket address, advances that cursor only when one exists,
and then sends `logout`. The downstream manager `logout` implementation
disconnects the ticket agent and then the carriage agent, in that order. This
establishes route-change teardown ordering; it does not establish whether a new
booking or login is automatically started afterward.

The higher `logoutWithResetDatabase:` path removes three notification observers,
clears the registration request, deletes temporary media, cancels core work,
closes windows, conditionally clears calendar state, invokes the core pre-logout
reset hook, truncates/logout-cleans the database, and finally clears the
logging-out flag. The exact boolean-to-calendar/reset policy, storage durability,
and observer payloads remain gaps because the reviewed call chain does not expose
their implementations.

The `locoDidKickout:` consumer is a separate downstream chain. Its raw guards
suppress all later work when `isLoggedIn` is false or `isLoggingOut` is true. For
an admitted event it reads the reason and three optional user-info fields,
unconditionally prepares the outer localized message, takes additional
missing-message and reason-zero fallback paths, and retains the URL independently, while including the optional label only
when both raw guard values are present. It then passes `reason == 1 || reason == 10` as the exact reset
boolean to `logoutWithResetDatabase:`, and queues a consumer wrapper on the
main queue. That wrapper queues a second main-queue block for the final alert projection.
That queue block (`0x1013ad254`) invokes a distinct projection body
(`0x1013ad34c`): it unconditionally localizes the final alert string,
creates an alert, and uses `beginSheetOnWindow:completionHandler:` when the core
window exists, otherwise `setHandler:` followed by `runModal`. The raw outer dispatch is at `0x1013ac920`; the wrapper dispatch is at
`0x1013ad2e0`. It is not the
manager notification block at `0x10141e388`; no type-36 notification or manager
post sequence is attributed to this consumer without evidence. The synthetic
fixture records each optional and window branch explicitly.

The final-alert raw receipts are `0x1013ad37c`/`0x1013ad3a0` for the bundle
lookup and localized alert string, `0x1013ad3f8`/`0x1013ad408`/`0x1013ad418`
for core, main-window-controller, and window lookup, `0x1013ad448` for the
window-result guard after `0x1013ad418`, `0x1013ad48c` for the sheet path, and
`0x1013ad4d4`/`0x1013ad4dc` for the handler/modal path.

No reviewed downstream caller proves an automatic booking or re-login after
these terminal paths. That behavior remains an explicit gap; this document does
not infer retries from the teardown call chain.

## Provenance

- `logoutForChangeServer`: IMP `0x10151869c`.
- downstream manager `logout`: IMP `0x1015185d0`.
- `logoutWithResetDatabase:`: IMP `0x1013aba20`.
- `locoDidKickout:`: IMP `0x1013ac5e8`.
- consumer queue block: `0x1013ad254` (outer dispatch `0x1013ac920`);
projection body: `0x1013ad34c` (inner dispatch `0x1013ad2e0`).
- reset comparison and call: raw `0x1013ac8ac`–`0x1013ac8bc` (`cmp`/`ccmp` reason 1/10, `cset w2`, then `logoutWithResetDatabase:`).
- raw receipts: private lab `cs1` disassembly and private Ghidra reports under
  `~/Library/Application Support/mooo-lab/ghidra/parity/`; no proprietary
  artifacts are committed.

The order fixture is transport-, storage-, and UI-neutral. It records only
statically observed calls and keeps unresolved policy choices explicit.

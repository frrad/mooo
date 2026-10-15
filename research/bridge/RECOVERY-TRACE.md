# Recovery trace: login statuses, more-settings and reachability

Status: research, 2026-10-15. This document closes three gaps that
[CONNECT-TRAFFIC](CONNECT-TRAFFIC.md) left open for findings #6 and #7. No
production code changed.

Reference client: official KakaoTalk for Mac 26.8.0 (build 2000), arm64.
Method: read-only static analysis with the private Ghidra project. Because
the read-only project disassembles only part of `__text`, raw byte scans over
the section searched for the status constants and selector references. Raw
output, addresses and pseudocode stay outside Git. All findings are static;
no live experiment was run for this document.

## Evidence

| ID | Finding | Kind | Confidence |
| --- | --- | --- | --- |
| RT-STATIC-001 | `LocoManager` treats LOGINLIST status 0 or -305 as success (manager status 25, route stored) and anything else as failure (status 24). It has no status-specific branches and hands the reply to `BCLocoClient` in both cases. | static | High |
| RT-STATIC-002 | The `doLoginListWithChatIds:maxIds:completion:` callback checks -950 first. It queues the request on `LPTokenManager` (`blockRequestOnSuccess:failure:`), which starts one shared `refreshToken`: an HTTP refresh-token renewal. On success it resends LOGINLIST at once with the same arguments and completion. There is no delay and no renewal counter. | static | High |
| RT-STATIC-003 | A failed renewal goes through `handleMaldiveError:failure:`. HTTP result -998 posts the LOCO kicked-out notification and the auth controller logs out. -999 posts the auth notice with code 3, and `onAuthNoti:` calls `logoutWithResetDatabase:NO`. Neither path calls the recovery completion. Any other renewal failure returns no response: the attempt fails and recovery retries after 10 s. | static | High |
| RT-STATIC-004 | -445 (login blocked) posts the LOCO kicked-out notification (error type 2); `locoDidKickout:` logs out. The recovery completion is not called. | static | High |
| RT-STATIC-005 | -305 counts as login success, but the LOGINLIST post-processing logs an LCHATLIST error and returns without calling the completion. Recovery stays marked active until the 600 s watchdog ends the episode, which logs out. | static | Medium: the effect in practice is unobserved |
| RT-STATIC-006 | Every other status, including -328 and -310, takes the generic failure branch: the attempt fails and recovery retries after the fixed 10 s. No comparison against -328 exists in `__text` and no -328 constant table exists in the data sections. | static | High; a switch with a different base offset is not fully excluded |
| RT-STATIC-007 | The more-settings request is an HTTP POST to `mac/account/more_settings.json` with `since` and `lastCalledAt`, sent through `LPTokenManager` (so -950 renewal applies). Its success handler stores `locoConfRevision` in persisted defaults unconditionally. It does not send GETCONF itself; the GETCONF gate (CT-STATIC-005) reads it at the next LOGINLIST. | static | High |
| RT-STATIC-008 | More-settings triggers: the login flow after the database is ready; a repeating 60 s timer (skipped while asleep) that fetches when more than 600 s have passed since its last update; and wake or network-reachable notices with the same 600 s rule. Timer and wake fetches wait a random 0–19 s first. Some UI callers are untraced. | static | High for the listed triggers |
| RT-STATIC-009 | `BCLocoClient` observes reachability with `clientNetworkReachabilityChanged:`. When logged in and not asleep, it cancels a pending delayed logout. Reachable: it calls `sleep` when the system monitor allows sleeping, otherwise `wakeup`. Unreachable: it schedules a connection logout after 10.0 s. | static | High |
| RT-STATIC-010 | `wakeup`, when the manager status is not 25, cancels and reschedules `recoverLocoLogin:` with the current cursor after 0.1 s. It does not call `resetAndRecoverLocoLogin`, so it neither logs out first nor advances the cursor: it continues the running episode and its watchdog. At status 25 it only updates the sleep status if needed. | static | High |
| RT-STATIC-011 | `resetAndRecoverLocoLogin` has exactly two callers: the manager-status delegate (25 → 26) and `clientSystemSleepStatusChanged:`. `recoverLocoLogin:` is scheduled only by `wakeup`, the reset routine, the 10 s reschedule, and a failed sleep-status update; `didRecoverLocoLogin:` only cancels it. No Swift caller exists. | static | Medium-high: a selector built from a string at runtime is not excluded |

## Consequences for the specs

### Finding #7 (reconnect schedule)

The maintainer chose full native parity (2026-10-15), implemented as
described in [CONNECT-TRAFFIC](CONNECT-TRAFFIC.md#implementation-2026-10-15-maintainer-decision-full-native-parity):

- **-328** is an ordinary failure with the 10 s cadence; the bridge's
  earlier 60–900 s ladder is gone.
- **-950** renews without a cap and resends LOGINLIST at once; the earlier
  one-renewal cap is gone. A failed renewal retries after 10 s.
- **Native logouts** (-445, renewal -998/-999) stop recovery at once.
- **-305** connects but leaves the episode open, so the watchdog fires.
- **Watchdog**: where native logs out locally, the bridge stops, reports
  `kakao-recovery-stopped`, and waits for the operator's reconnect command
  or a restart.
- **Reachability.** Native has a second trigger: regained reachability
  continues the running episode at once (0.1 s), and lost reachability logs
  out after 10 s. An attempt that returned early on an unreachable network
  waits for that trigger, a wake, a screen or idle change, or a failed
  sleep-status update; otherwise the watchdog ends the episode. The bridge has
  no reachability monitor. Its fixed 10 s retry stands in for both.

### Finding #6 (GETCONF and route cache)

No contradiction. The revision that gates GETCONF comes from the
more-settings request (RT-STATIC-007/008), which the bridge does not send. A
revision bump takes effect only at the next login even natively. The
bridge's substitute trigger (book again when the stored ticket pool is
exhausted) stays a documented deviation.

## Remaining gaps

- The sleep-status (SETST) callback reached when the login reply's PC status
  differs from the current one.
- The server-side meaning of -305, -445, -998 and -999.
- UI and HTTP-error callers of the more-settings request.
- Which entry reaches the persisted last-route branch at carriage connect.

# Connect-time traffic: catch-up, routing and recovery

Status: research, 2026-10-15. This document answers native-skew audit
findings #1, #6 and #7 ([NATIVE-SKEW-AUDIT](NATIVE-SKEW-AUDIT.md)) and gives an
implementation spec for each. No production code changed.

Reference client: official KakaoTalk for Mac 26.8.0 (build 2000), arm64,
read-only static analysis (instruction-level disassembly with selector-stub and
literal resolution, plus the private Ghidra project). Live evidence comes from
one owned-account run on 2026-10-15 using the clean-room Go client on the
existing authorized secondary (B) profile. Raw packets, identifiers and tooling
stay outside Git. The sanitized fixture is
[`fixtures/connect-traffic/mchatlogs-catchup-observed.json`](../fixtures/connect-traffic/mchatlogs-catchup-observed.json).

## Evidence

| ID | Finding | Kind | Confidence |
| --- | --- | --- | --- |
| CT-STATIC-001 | Recovery starts 0.1 s after `resetAndRecoverLocoLogin`; a failed login reschedules `recoverLocoLogin:` after a fixed 10.0 s; no growth, no jitter. | static | High: literal `double` operands of `performSelector:withObject:afterDelay:` |
| CT-STATIC-002 | Starting a recovery episode arms a 600 s watchdog. If no login succeeds within it, the episode ends as failed, the client posts its LOCO-disconnected notification, and the auth controller logs out without resetting the database. | static | High for the chain; UI after logout not traced |
| CT-STATIC-003 | `bgReconnItv` from GETCONF is stored and archived but has no other reader. | static | High: getter called only from the archiver; no other selector, ivar or key-string use |
| CT-STATIC-004 | The CHECKIN route is cached in memory with `cacheExpire` from the CHECKIN reply and the system uptime at CHECKIN success; it is expired when `uptime - checkTime >= cacheExpire`. A valid cached route skips CHECKIN. | static | High |
| CT-STATIC-005 | GETCONF is sent only when the stored GETCONF revision is zero or the more-settings `locoConfRevision` is newer. Its reply is persisted. | static | High for the gate; the more-settings request and triggers are in [RECOVERY-TRACE](RECOVERY-TRACE.md) RT-STATIC-007/008 |
| CT-STATIC-006 | Booking and ticket retries use equal-jitter backoff: `half = min(2^n / 2, cap) / 2`, delay `= half + U{0..floor(half)}` seconds. | static | High |
| CT-LIVE-001 | MCHATLOGS from the bridge's committed positions returned exactly the same logs, in the same order, as per-room `SYNCMSG cnt=0`, in one request instead of three. | observed | High for this run |
| CT-LIVE-002 | MCHATLOGS caps each response at 80 logs **in total across rooms**. Rooms are concatenated in ascending chat-ID order, each as one contiguous ascending run. The last room in a capped page may be cut. | observed | High for this run |
| CT-LIVE-003 | Every returned run starts with `prevId == since` when the room's history after `since` is available, and `prevId` chains within runs. A `since` below the server's window yields a first `prevId` that is not `since`. | observed | High for this run |
| CT-LIVE-004 | Rooms whose last log is below `minLogId` are silently absent. An up-to-date request returns an empty `eof=true`. MCHATLOGS has no upper bound and returns logs after the login ceiling. | observed | High for this run |
| CT-LIVE-005 | CHECKIN `cacheExpire` = 3600; GETCONF `revision` = 200; GETCONF `wifi.bgReconnItv` = 180. | observed | Values for this account and date |

## Finding #1: catch-up with MCHATLOGS only

### Native behavior (recap)

After the last LCHATLIST page, the client selects every local room with
`lastMChatLogId < lastLogId` and `lastLogId >= minLogId`, sorts them by signed
chat ID and sends `MCHATLOGS{chatIds, sinces}` for at most 100 rooms.
`eof=true` retires the whole batch. `eof=false` retires the selected rooms whose
ID is below the chat ID of the last returned log; that room and later rooms stay
pending with their advanced cursors. The loop is serial and has no sleep or
retry. See [HISTORY-STATIC](HISTORY-STATIC.md) §A. Single-room `SYNCMSG` is not
part of login; it runs from chat open and mark-as-read (§C).

### Live differential (2026-10-15)

Seven owned rooms (one direct chat, six groups; every member one of the three
owned accounts). With the bridge stopped, owned phone A sent three texts in one
group (R3) and phone C sent two in another (R6). After the probe logged in, A
sent one more text in R3. The probe then made raw requests on that single
session, two seconds apart, with no retry, no commit and no read acknowledgement.
Afterwards the real bridge (current `main`) caught up all six texts in a normal
connect; that run is not part of the evidence.

1. **Committed positions (the bridge's actual catch-up input).**
   `MCHATLOGS([R3, R6], [committed, committed])` returned R3 ×4 and R6 ×2,
   `eof=true`. The per-room `SYNCMSG(cur=committed, max=login ceiling, cnt=0)`
   requests the bridge sends today needed **three** requests (R3 ×4; R6 one log
   per page, twice) and returned exactly the same identities in the same order.
   Both methods returned R3's post-login text, past the login ceiling.
   `SYNCMSG` returned it despite `max`.
2. **Large backlog across rooms.** `MCHATLOGS` for all seven rooms with
   `since=0`, driven by the native retirement loop, took five pages:

   | Page | Rooms requested | Returned (room × logs) | eof |
   | --- | --- | --- | --- |
   | 1 | R1–R7 | R1 ×80 | false |
   | 2 | R1–R7 | R1 ×80 | false |
   | 3 | R1–R7 | R1 ×80 | false |
   | 4 | R1–R7 | R1 ×70, R3 ×10 | false |
   | 5 | R3–R7 | R6 ×2, R7 ×3 | true |

   R1 held 310 available logs. The 80-log cap is per response, not per room:
   page 4 finished R1 and filled the remaining 10 slots from the next room with
   data. R2, R4 and R5 never appeared. Their last log is below the login
   `minLogId`, so native selection would never request them. After page 4, R3
   was the boundary room and stayed pending. Page 5 returned nothing more for
   it, so its 10 logs had been complete. A cut can therefore be detected only
   by asking again.
3. **Contiguity.** All 337 returned logs formed per-room `prevId` chains. Each
   run that started after a cursor inside the available window had
   `first.prevId == since`, including R6, whose committed position was below
   `minLogId` but whose successor was available. Runs from `since=0` in rooms
   with pruned history started at a log whose `prevId` was an older, unreturned
   identity.
4. **No progress.** A single up-to-date room, and all seven rooms at their
   latest cursors, each returned an empty `eof=true`. No `eof=false` page was
   ever empty.

Compared with HST-LIVE-005/006 (2026-10-13), R1, the same direct chat, now
exposes 310 instead of 312 logs. The server window floor moves over time.

### Gap semantics

- `eof` describes the **batch**: no selected room has more after this page. It
  says nothing about any single room. With `eof=false`, only rooms strictly
  below the boundary room are known complete.
- A room absent from an `eof=true` response has nothing available after its
  `since`. Its cursor does not move. If its inventory ceiling is above `since`,
  that interval is unavailable, not delivered.
- Continuity inside the server window is shown by `prevId`. The first log of a
  room's run must have `prevId == since`. Within a run each `prevId` equals the
  previous `logId`. A mismatch means the server skipped identities: a gap.
- `since` is exclusive. No run contained the `since` log itself.
- MCHATLOGS has no `max`. Logs past the login ceiling are returned.

### Implementation spec

Replace the per-chat `SYNCMSG cnt=0` loop in the connector's `catchUp`
(`internal/bridge/connector/client.go`, `client.CatchUp` in
`internal/client/sync.go`) with one bulk pass. Keep its position before live
subscription and its ordering guarantees.

1. **Selection.** Take the rooms that `ResumeTargets` returns today: committed
   position, or first delivery start − 1, below the login ceiling. Read the
   login `minLogId`.
   - Ceiling `< minLogId`: send no request; native would not select the room.
     Record the gap (committed+1 … ceiling), post the existing gap notice and run
     the roster fallback, as for `ErrGapUnresolved` today.
   - Otherwise the room is pending, with `since` = committed position.
   - The bulk cursor is the delivery cursor. Native keeps a separate
     `lastMChatLogId`; the bridge has no use for a second cursor.
2. **Request.** Sort pending room IDs as signed int64, ascending. Send
   `MCHATLOGS{chatIds: first ≤100, sinces: their cursors}`. Pace through the
   existing history admission (one in flight, configured spacing). This is
   slower than native, which sends the next page from the completion callback;
   it is a deliberate, account-positive difference. One pass is one request per
   page, not one per room.
3. **Validate each response** (fail closed, protocol error):
   - status 0; every log's `chatId` is in the request;
   - each room appears as one contiguous run, and runs are in ascending chat-ID
     order;
   - inside a run, `logId` strictly increases and every `logId > since`. A log
     equal to `since` is skipped, as `CatchUp` does today.
4. **Contiguity per run.** Let `p` be the room's cursor before this page.
   - `first.prevId == p`: contiguous.
   - `first.prevId > p`: the server skipped (p, first.prevId]. Record that gap,
     queue the gap notice once per room, then continue delivering from `first`.
     This mirrors the native missing-predecessor marker.
   - `first.prevId < p`, a missing `prevId`, or a broken chain inside the run:
     treat as unresolved. Record the gap (p+1 … ceiling) for that room, send the
     notice and roster fallback, and retire the room. Do not guess.
5. **Delivery bound.** Deliver and commit only logs with `logId <= ceiling`, as
   `CatchUp` does now. Logs past the ceiling also arrive on the live stream; the
   next login covers anything missed. Advance the request cursor to the highest
   returned `logId` even past the ceiling, so the next page does not repeat it.
   Deliver in room order, then log order, through the per-chat delivery path
   used today. Since #389 that path holds a failed chat's remaining events for
   in-process retry instead of aborting; bulk catch-up must hand each room's
   remainder to that hold the same way, without affecting other rooms.
6. **Retirement.**
   - `eof=true`: retire all requested rooms.
   - `eof=false` with logs: retire requested rooms with ID < the last log's
     `chatId`. Keep the boundary room and later rooms with updated cursors.
   - A retired room whose cursor is still below its ceiling: record the gap
     (cursor+1 … ceiling), send the notice and run the roster fallback. This is
     the bulk form of today's "empty or non-progressing page".
7. **No progress.** `eof=false` with no logs, or a page that neither retires a
   room nor advances any cursor: stop. Record gaps for every pending room
   (cursor+1 … ceiling), send notices, and go live. Never loop. This was not
   observed; the native scheduler has the same hole (HISTORY-STATIC §A).
8. **Page cap.** Keep a hard cap on bulk pages per connect, 100 pages
   (8000 logs at the observed cap), matching today's per-room `maxCatchUpPages`
   but account-wide. On the cap, handle every pending room as in step 7, so a
   live commit can never skip unrecovered messages. Backfill can recover the
   recorded intervals later.
9. **Failures.** A transport error or nonzero status aborts bootstrap without
   committing, as today. Native continues to the room-metadata phase instead;
   the bridge keeps its abort because live commits must not pass unrecovered
   intervals. Record this deviation.
10. **Remove** per-chat `SYNCMSG` at connect. Keep `SYNCMSG` for explicit
    history and mark-read (findings #8 and #16).
11. **`startup_history`.** The opt-in `AcquireStartupHistory` already sends
    this request with the same retirement rule, into the history store.
    Implement the catch-up pass as the one bulk pass and let it feed both
    delivery and the history store, so a connect sends one MCHATLOGS chain, not
    two. Its current validation accepts `position == since`; align it with
    step 3.

Regression tests (production code, scripted backend), built from the fixture:

- the committed-position exchange: one MCHATLOGS, no SYNCMSG, six events in
  order, cursors at the ceilings;
- the five-page multi-room chain: boundary retention, the boundary room
  complete on the next page, absent floor rooms;
- `first.prevId > since` produces a recorded gap, a notice, and delivery of
  the remainder;
- a log past the ceiling advances the request cursor but is not delivered;
- empty `eof=false` stops with gaps recorded and no second request;
- the page cap records gaps for pending rooms.

### Implementation (2026-10-15)

`Client.CatchUpAll` (`internal/client/bulk_catchup.go`) implements steps 1–11;
the connector's `catchUp` delivers its rooms. Production-path tests replay the
fixture's committed-position exchange and its five-page chain from committed
positions, and cover the skipped predecessor, the broken chain, the empty
non-final page, the page cap, the floor room, failure without commits and the
shared startup-history chain. Single-room `SYNCMSG` remains for explicit history,
mark-read and a held chat's dropped interval (delivery holds, not connect).

Deliberate differences from native, beyond those the spec names (pacing through
history admission, abort on failure):

- **No send of the 100-room batch from the completion callback.** Pages are
  serial and paced, as step 2 says.
- **Gap notices.** One notice per partly recovered chat: before its recovered
  messages when the server skipped identities ahead of them, after them when
  only the tail is unrecovered. Any recorded gap also triggers the managed-group
  roster fallback, since the skipped interval may hold membership feeds.
- **A discontinuity past the ceiling** ends the room's chain without a gap: only
  identities after the login ceiling are in doubt, and the live stream and the
  next login cover them.
- **Startup history.** Rooms that need catch-up are requested from their delivery
  position, never from an older acquired cursor, and the acquisition budget
  (25 pages, one minute) only removes acquisition rooms from the chain.

### Read side effect

`SYNCMSG` catch-up can make messages appear read to other participants
([syncmsg read side effect](syncmsg-read-side-effect-procedure.md)). In
HST-LIVE-002/004, MCHATLOGS did not change the sender's unread count. Switching
therefore also removes a read side effect native login does not cause. This run
did not re-measure unread counts.

## Finding #6: GETCONF, CHECKIN and the route cache

### Native behavior (Mac 26.8.0)

The login entry (`sendLoginListRequestWithChatIds:maxIds:completion:` in
`LocoManager`) decides in this order:

1. **GETCONF gate.** Unless booking has just completed in this cycle
   (`hasBeenBooking`, consumed and cleared here), GETCONF is sent only when the
   stored `getConfRevision` is 0, or the more-settings `locoConfRevision` is
   greater than it. The GETCONF reply goes through `updateLocoConf:` into
   persistent user defaults (wifi, wwan, ticket, trailer, etc. and revision),
   and the ticket address pool comes from it. Ordinary reconnects and process
   restarts reuse the persisted config and send **no** GETCONF.
2. **Route cache.** If an in-memory carriage address exists and
   `isCarriageExpired` is false, the client sends LOGINLIST to it directly, with
   no CHECKIN. Otherwise it sends CHECKIN.
   - CHECKIN success stores `host:port`, copies the reply's `cacheExpire`
     (int seconds) into the manager config as a double, and records
     `carriageExpireCheckTime = systemUptime`.
   - `isCarriageExpired`: false when the check time is 0; otherwise
     `systemUptime - checkTime >= cacheExpire`. It uses monotonic time, so
     wall-clock jumps do not matter.
   - The in-memory address is cleared only by the CHANGESVR logout
     (`logoutForChangeServer`, which also advances the ticket cursor when more
     addresses remain) and by a full account clear. Ordinary logout and
     recovery keep it.
   - A separate **persisted** last route (`carriageLastHost/Port` in settings)
     is written on LOGINLIST success. It is cleared when a connect to exactly
     that endpoint fails. It is consulted when no in-memory address exists at
     carriage connect. Which entry reaches that branch was not fully traced.
3. **Retries.** Booking makes at most 3 attempts (`bookingRetryCount`). Ticket
   attempts move through the address pool; attempt `n` for the next address is
   `ticketAddressCursor + 1`. Both wait `jitteredBackoff(attempt: n, cap:)`
   (CT-STATIC-006). Booking's cap is 8 s and the ticket cap is 32 s
   ([protocol-bootstrap](../protocol-bootstrap.md)):

   | n | base = min(2^n/2, cap) | delay (s) |
   | --- | --- | --- |
   | 1 | 1 | 0.5 |
   | 2 | 2 | 1–2 |
   | 3 | 4 | 2–4 |
   | 4 | 8 | 4–8 |
   | 5 (ticket) | 16 | 8–16 |
   | 6+ (ticket) | 32 | 16–32 |

   The random part is a uniform integer, so the delays are whole seconds plus
   `half`.

Observed (CT-LIVE-005): `cacheExpire` = 3600 s and GETCONF `revision` = 200.

### Implementation spec

In `internal/client/session.go` `connectSession` and `login_sync.go` `checkin`:

1. **Persist GETCONF** in the profile: the revision plus the fields the client
   consumes (ticket hosts/ports, `wifi.fgPingItv`, `wifi.recvHeaderTimeout`).
   On connect, reuse the stored config and send GETCONF only when:
   - nothing is stored or it fails to parse (native: revision 0); or
   - every ticket address in the stored pool failed CHECKIN in this attempt.
     The stored pool may be stale, and the bridge has no more-settings revision
     to tell it so. This trigger is a deviation; native relies on the
     more-settings revision instead.
   Do not add an age-based refresh; native has none.
2. **Cache the carriage route** on the `Client` (process lifetime, like native
   memory): `host`, `port`, `cacheExpire` from the CHECKIN reply (fall back to
   0, meaning no reuse, when absent or ≤ 0), and the monotonic time of CHECKIN
   success. On every connect, while `elapsed < cacheExpire`, skip CHECKIN and
   dial the cached route.
3. **Invalidate** the cache on CHANGESVR (and advance to the next ticket
   address, as `manager-terminal-lifecycle.md` already specifies), on KICKOUT
   or logout-reset, and on expiry. Also clear it when the dial or secure
   handshake to the cached route fails, then fall through to CHECKIN in the
   same attempt. Native keeps the in-memory route until expiry, but clears its
   persisted route on that exact failure. Clearing avoids up to an hour of
   10-second retries against a dead host. Record it as a deviation.
4. **CHECKIN pacing.** Replace the current immediate host × port walk with a
   native pool walk: try address 0 at once, then wait `jitter(n, 32 s)` before
   address n. Stop when the pool is exhausted; the attempt fails as retryable
   and the reconnect schedule (finding #7) owns the next try. If GETCONF itself
   is needed and fails, retry it at most 3 times total with `jitter(n, 8 s)`.
5. Each reconnect inside the cache lifetime then sends only LOGINLIST and the
   login sequence. That removes two booking requests per reconnect and the
   CHECKIN burst during an outage.

Tests: production `connectSession` with scripted dialers. Cover a second
connect within the cache lifetime (no GETCONF/CHECKIN dial); expiry via an
injected clock; CHANGESVR invalidation; a cached-route dial failure falling
through to CHECKIN; jitter bounds with an injected random source; a profile
reopen reusing the persisted GETCONF.

## Finding #7: reconnect schedule

### Native behavior (Mac 26.8.0, `BCLocoClient`)

- **Entry.** `resetAndRecoverLocoLogin` logs out, then, if the stored user ID is
  positive and the access token nonempty, cancels any pending
  `recoverLocoLogin:` for the current cursor and schedules it after **0.1 s**.
  Callers: the manager-status delegate on the 25 → 26 transition
  ([GROUP-FAILURE-RECOVERY](GROUP-FAILURE-RECOVERY.md)), and
  `clientSystemSleepStatusChanged:` (system wake) when the network is reachable
  and the screen is not covered. No other caller exists
  ([RECOVERY-TRACE](RECOVERY-TRACE.md) RT-STATIC-011). Regained network
  reachability calls `wakeup`, which reschedules `recoverLocoLogin:` after
  0.1 s without logging out first (RT-STATIC-010). A PC-sleep-status (`doSetStWithPCStatus`) failure also schedules
  `recoverLocoLogin:` after 0.1 s.
- **Attempt** (`recoverLocoLogin:`). Ignore a stale cursor. On the first
  attempt of an episode, set the active flag and schedule
  `didRecoverLocoLogin:NO` after **600 s** (the watchdog). If the network is
  unreachable, auth is not logged in, or recovery is disabled, return **without
  rescheduling**; a later trigger must restart recovery. Otherwise set the
  disabled flag and call `login:completion:`.
- **Failure.** The login completion's failure branch clears the disabled flag
  and schedules `recoverLocoLogin:` again after **10.0 s**, with the same
  cursor. The delay is fixed: no exponent, no jitter, no attempt counter.
- **Success.** The completion cancels the watchdog and calls
  `didRecoverLocoLogin:YES`, which cancels pending attempts, increments the
  cursor (making stale callbacks inert) and clears both flags. It then
  notifies `locoClientDidRecover`. The next outage starts a fresh episode, so
  native "resets the schedule" on the first successful login, with no
  stability period.
- **Give-up.** If the watchdog fires (600 s after the episode's first attempt
  with no success), `didRecoverLocoLogin:NO` ends the episode the same way and
  then posts the LOCO-disconnected notification with an error of type 36. The
  auth controller's `locoDidDisconnect:` calls `logoutWithResetDatabase:NO`
  when it is logged in and not already logging out. Native stops trying after
  about 60 attempts in 10 minutes; the user or a later system event must act.
- **`bgReconnItv`.** GETCONF `wifi.bgReconnItv` (180 observed) is decoded,
  stored and archived. Nothing else reads it, so it does not drive reconnect
  timing on the Mac.
- **LOGINLIST statuses** ([RECOVERY-TRACE](RECOVERY-TRACE.md)). -328, -310
  and other unlisted statuses are ordinary failures with the 10 s retry.
  -950 renews the token over HTTP and resends LOGINLIST at once, with no
  renewal cap. -445 and renewal results -998/-999 log out. -305 counts as
  success but never completes recovery, so the watchdog logs out.

### Implementation (2026-10-15, maintainer decision: full native parity)

Implemented in `internal/bridge/connector` (`recovery_schedule.go`,
`lifecycle.go`) and `internal/client` (renewal loop). The details are in the
[reconnect design](../bridge-reconnect-design.md).

1. **Episode start** on session loss: first attempt after 0.1 s. The first
   connect is an episode's first attempt.
2. **Failed attempt**: every failure retries after a fixed 10 s, including
   LOGINLIST `-328`, `-310` and other status errors, and a transport failure
   during LOGINLIST ([RECOVERY-TRACE](RECOVERY-TRACE.md) RT-STATIC-006). `-950`
   renews and resends LOGINLIST at once with no renewal limit (RT-STATIC-002).
   A failed renewal retries after 10 s.
3. **Native logouts** stop recovery at once: LOGINLIST `-445` and renewal
   results `-998`/`-999` (RT-STATIC-003/004).
4. **Watchdog**: 600 s after the episode's first attempt with no completed
   login. Native logs out locally. The bridge's equivalent is to stop without
   touching credentials, report `kakao-recovery-stopped`, and wait for the
   operator's reconnect command or a restart. `-305` connects but does not
   complete the episode, so the watchdog fires (RT-STATIC-005).
5. **Reset**: a completed login ends the episode. No short-session guard and
   no persisted schedule; a restart starts a fresh episode.
6. Do **not** read `bgReconnItv`.

## Remaining gaps

- Which entry reaches the persisted last-route branch at carriage connect.
- The LOGINLIST status, more-settings and reachability gaps are closed in
  [RECOVERY-TRACE](RECOVERY-TRACE.md), which lists what remains there.
- MCHATLOGS: a cut that lands inside a non-first room (seen only for the first
  room), and an `eof=false` empty page (never observed). The spec handles both
  conservatively.
- This run used three rooms with small offline intervals plus a server-side
  backlog reached through `since=0`; it did not test thousands of offline
  messages across many rooms.

# Reconnect: what we know, what we must learn, what we must build

Status: work plan, 2026-09-30. This is the single entry point for reconnect.
It gathers what is already established, lists the open questions with the
method for answering each, and defines the implementation work and exit
criteria. Detailed evidence stays in the linked dossiers.

## Goal

The bridge keeps a Kakao session alive for days without operator
intervention. When the connection drops it reconnects on its own, catches up
missed messages, and never retries an ambiguous mutation. On terminal events
(forced logout, revoked device, expired credentials that cannot be renewed) it
stops and tells the operator instead of looping.

Today a dropped session is reported through bridge state and stays down until
someone restarts the bridge (see [`bridge/PLAN.md`](bridge/PLAN.md), phase B3).

## Established

| Topic | Finding | Source |
|---|---|---|
| Ownership | The generic carriage (connection) layer never reconnects or retries. A socket failure, receive timeout, or explicit disconnect fails every pending request exactly once and publishes a status change. Reconnect belongs to the owning manager. | [`carriage-callback-lifecycle.md`](carriage-callback-lifecycle.md) |
| Ordinary recovery | Recovery reruns the normal login sequence and preserves `chatIds`/`maxIds`, `lastTokenId`, and `lbk`. It is suppressed when authentication is unusable, the network is unreachable, recovery is disabled, another recovery is running, or the recovery generation is stale. A completed attempt advances the generation; a failure schedules another bounded attempt. | [`session-login/PROTOCOL.md`](session-login/PROTOCOL.md#reconnect-and-resume) |
| `CHANGESVR` | Terminal route change: clear the cached carriage route, advance to the next ticket address when there is one, and log out. A fresh login may follow; no request is retried. | [`manager-terminal-lifecycle.md`](manager-terminal-lifecycle.md) |
| `KICKOUT` | Ignored unless the session is logged in and not already logging out. Otherwise the manager logs out, and reasons 1 and 10 also reset the local database. Reason meanings are unproven. It must not enter a reconnect loop. | [`manager-terminal-lifecycle.md`](manager-terminal-lifecycle.md) |
| Expired token | `LOGINLIST` `-950` means an expired access token. Admit one renewal, then one fresh login; fail closed if either fails. Implemented in `client.Client`. | [`session-login/PROTOCOL.md`](session-login/PROTOCOL.md#reconnect-and-resume) |
| Secondary-device limit | `LOGINLIST` `-328` is a temporary secondary-device limit. Retrying immediately does not help. | [`session-login/EVIDENCE.md`](session-login/EVIDENCE.md) SL-LIVE-005 |
| Resume | A resumed login plus `SYNCMSG` catch-up with `cnt=0` recovers missed messages in previously committed chats. Live-validated through the bridge on a restart. | [`message-continuity.md`](message-continuity.md), SL-LIVE-013/014 |
| Existing code | `internal/protocol/sessionlogin` has a pure recovery reducer (generations, stale callbacks, terminal `ChangeServer`/`Kickout` actions, reason 1/10 reset effects, unauthenticated-KICKOUT no-op). `internal/protocol/events` decodes `CHANGESVR` and `KICKOUT` as typed events. Neither is wired into `client.Client` or the bridge. | code |

## What we must learn

Each item names how to answer it. Static items come first, per the
clean-room rule: trace the official client before any live experiment.

The static items are expanded into detailed sub-questions, with the required
answer format, in [`reconnect/STATIC-QUESTIONS.md`](reconnect/STATIC-QUESTIONS.md).

### Static analysis (Ghidra, Mac 26.8.0)

1. **Recovery trigger.** Which component observes a carriage disconnect and
   starts recovery, and under which conditions (socket error, receive
   timeout, network-reachability change, app wake, `CHANGESVR` logout)?
   Trace from the carriage status-change publication to the manager's
   recovery entry point.
2. **Backoff schedule.** The delay before the first retry, how it grows, any
   cap, any jitter, and whether the attempt count or delay resets after a
   stable session. The session-login plan lists "exact recovery delays" as
   unresolved.
3. **Retry budget and give-up.** Does recovery ever stop on its own? What
   happens when it is exhausted? The protocol notes say an upper-layer
   disconnect that exhausts recovery logs out without a database reset;
   confirm the threshold.
4. **Keep-alive.** Whether the client sends `PING` on a timer, the interval,
   the expected reply, and what a missed reply does. The clean-room client
   sends no keep-alive, so idle connections may be dropped by the server.
5. **Receive timeout.** The receive-header timeout value that triggers agent
   disconnect, and whether it applies while idle or only with requests
   pending.
6. **Status routing on reconnect.** Which `LOGINLIST` statuses during
   recovery are retryable (network, `-328`), which are renewable (`-950`),
   and which are terminal (revoked, unregistered, policy). Map each to
   retry, renew once, or stop.
7. **`CHANGESVR` follow-up.** After the change-server logout, what triggers
   the next login, and is it immediate or scheduled through the same
   backoff? How does the ticket-address cursor interact with booking
   (`GETCONF`/`CHECKIN`)?
8. **`KICKOUT` reasons.** The full set of reason codes the client
   distinguishes and what each does beyond reasons 1 and 10, and whether any
   reason should keep the device registered.
9. **Endpoint cache on failure.** When a failed connection invalidates the
   cached route versus reusing it on the next attempt.

### Controlled live experiments (owned disposable accounts only)

Run only after the static answers exist, and follow the lab rules:
exponential backoff between failed auth attempts, no rapid reconnects after
`-328`, synthetic content only.

1. **Idle survival.** Connect and stay idle with no keep-alive. Record how
   long the server keeps the session and how it ends (close, `CHANGESVR`,
   silence). Repeat with the keep-alive from item 4 once it is implemented.
2. **Network drop.** Cut the network briefly while connected, restore it,
   and confirm a single reconnect plus catch-up recovers messages sent
   during the outage without duplicates.
3. **Concurrent secondary login.** Establish whether another secondary
   device logging in displaces this session, and which event or status the
   displaced session sees.
4. **Revocation.** Remove the device from the primary (Android) device's
   settings and record what the connected session receives and what the
   next login returns. This is terminal; it requires re-registration, so
   schedule it last.

## What we must build

### Client (`internal/client`)

- A reconnect supervisor that owns the lifecycle the carriage layer does
  not: it observes session end, consults the `sessionlogin` recovery
  reducer, waits out the backoff, and establishes a new session with the
  same profile lease and checkpoint. `Client.Close` stays terminal; the
  supervisor either reuses the `Client` with a new session or opens a new
  one, but never runs two sessions for one profile.
- Keep-alive `PING` at the official interval, with the official receive
  timeout (static items 4 and 5).
- Wire `CHANGESVR` (clear route, fresh booking, reconnect) and `KICKOUT`
  (terminal; reset effects only for the proven reasons) through the reducer.
- A map from recovery `LOGINLIST` status to retry, renew once, or stop
  (static item 6).
- A guarantee that a request in flight when the session drops fails once and
  is never replayed on the new session.

### Bridge (`internal/bridge/connector`)

- Drive reconnect from the supervisor instead of reporting a permanent
  transient disconnect.
- Report bridge state throughout: `TRANSIENT_DISCONNECT` while retrying (with
  the next attempt time), `CONNECTED` after catch-up, `BAD_CREDENTIALS` on
  kickout, revocation, or failed renewal.
- Run the existing connect-time catch-up after every reconnect, before live
  events, exactly as on startup.
- Fail outbound Matrix messages cleanly while disconnected rather than
  queueing them for automatic resend.

## Decisions needed

- **Default backoff if static analysis does not settle it.** The fallback is
  the lab rule: next delay = 2 × time since first failure, with a cap to be
  chosen.
- **Reason 1/10 database reset.** The official client resets its local
  database. The bridge has no equivalent database; decide whether this means
  clearing the continuity checkpoint (forcing a fresh, non-resumed login) or
  only logging out.
- **Operator notification.** Whether terminal events also post to the
  bridge's management room, beyond bridge state.

## Exit criteria

- Static items 1–7 are answered in a dossier with provenance and
  confidence; items 8–9 are answered or recorded as explicit gaps.
- Synthetic tests cover: socket drop then reconnect then catch-up; backoff
  timing; `CHANGESVR` route clear and fresh booking; `KICKOUT` terminal
  without retry; `-950` renew once; `-328` backoff; stale-generation
  callbacks; no replay of an in-flight mutation; keep-alive timeout.
- Live experiments 1–3 pass through the bridge; experiment 4 is run or
  explicitly deferred.
- [`protocol-parity.md`](protocol-parity.md) rates the reconnect row at
  least "Substantial", and bridge plan phase B3's reconnect item is checked.

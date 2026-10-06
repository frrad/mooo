# Reconnect static-analysis questions

Status: open question set, 2026-09-30. Target: KakaoTalk for macOS 26.8.0
(arm64), the same authorized build used by the session-login and terminal
lifecycle dossiers.

This document expands the nine static items in [`../reconnect.md`](../reconnect.md)
into concrete questions, and fixes the form an answer must take before it
counts. It is the task list for the private analysis lane (lane A in
[`../CLEANROOM.md`](../CLEANROOM.md)). The answers become the input to the public
implementation lane.

## Why this directory

`research/reconnect/` follows the layout of `research/session-login/` and
`research/device-registration/`: a topic directory with its own evidence ledger,
specification, and conformance vectors. [`../reconnect.md`](../reconnect.md) stays
the plan and entry point. The directory holds the answers:

| File | Contents | Created when |
|---|---|---|
| `STATIC-QUESTIONS.md` | This question set and answer contract | now |
| `EVIDENCE.md` | Ledger rows `RC-BIN-NNN` (static) and `RC-LIVE-NNN` (live) in the same column format as [`../session-login/EVIDENCE.md`](../session-login/EVIDENCE.md) | first answer |
| `PROTOCOL.md` | Implementation-neutral reconnect specification, one section per answered question | first answer |
| `experiments/` | Sanitized experiment records from [`../experiments/template.md`](../experiments/template.md) | first live experiment |

Conformance vectors live beside the Go code that consumes them (see
[Answer format](#answer-format)), not in `research/`.

## What is already known (do not re-derive)

Carry these facts forward. A question below asks only what they leave open.

- The carriage agent never reconnects or retries. A socket failure, receive
  timeout, or explicit disconnect fails every pending callback once, clears both
  correlation maps, and publishes a status change (SL-BIN-024).
- Ordinary recovery reruns the normal login and preserves `chatIds`/`maxIds`,
  `lastTokenId`, and `lbk`. It is suppressed when authentication is unusable,
  the network is unreachable, recovery is disabled, another recovery is active,
  or the generation is stale. A completed attempt advances the generation; a
  failure schedules another bounded attempt (SL-BIN-007).
- Booking suppresses concurrent attempts and makes at most three attempts with
  jitter capped at 8 s. Ticket retries advance through the address pool with
  jitter capped at 32 s. Address-pool exhaustion terminates the attempt
  ([`../session-login/PROTOCOL.md`](../session-login/PROTOCOL.md#routing-cache-and-retry)).
- A cached carriage route is expiry-checked against system uptime and cleared
  when that exact endpoint fails (SL-BIN-007).
- `CHANGESVR` clears the route, advances the ticket-address cursor when another
  candidate exists, and logs out. `KICKOUT` is guarded by "logged in and not
  already logging out"; reasons 1 and 10 select database reset (SL-BIN-025).
- An upper-layer disconnect that exhausts manager recovery logs out without a
  database reset ([`../session-login/PROTOCOL.md`](../session-login/PROTOCOL.md#reconnect-and-resume)).
  The threshold is unconfirmed.
- `LOGINLIST` statuses `0` and `-305` are accepted, `-310` is partial and not a
  completed login, `-445` is login-blocked, and `-950` is expired token
  (SL-BIN-006, SL-LIVE-004/005). `-328` was observed live as a temporary
  secondary-device limit (SL-LIVE-005).
- The pure reducer in `internal/protocol/sessionlogin` models generations,
  stale callbacks, `ChangeServer`/`Kickout` terminal actions, reason 1/10 reset
  effects, and the unauthenticated-KICKOUT no-op. It has no timing, no
  trigger policy, and no status routing.

## Answer format

An answer has three parts. A question is closed only when all three have
landed, or when the answer is recorded as an explicit gap.

### 1. Evidence row

Add one or more `RC-BIN-NNN` rows to `research/reconnect/EVIDENCE.md`:
date, client version, evidence class, a one-sentence statement, confidence,
and status (what stays open). Put the method (the chain that was traced, in
behavioral terms) in the matching `PROTOCOL.md` section, not the row.

### 2. Specification section

Add a `PROTOCOL.md` section that states the behavior so someone who never saw
the binary can implement it:

- inputs, state, outputs, and ordering;
- exact constants and units (seconds or milliseconds, and which clock:
  wall, monotonic, or uptime);
- what happens on every branch, including failure and "no-op";
- which facts are observed, which are inferred, and which are unknown.

The clean-room rule applies. No decompiled code, pseudocode, internal
class, selector, or symbol names, or binary offsets. Name behavior, not
functions. A constant is fine; the name the binary gives it is not.

### 3. Conformance vectors (the tests)

Yes: every answer ships as tests, but as **data first**. The analysis lane
writes vectors: small, reviewable tables of inputs and expected outputs.
The implementation lane writes the Go code that has to satisfy them. This
matches the lane split in [`../CLEANROOM.md`](../CLEANROOM.md): the vector file
is the whole handoff, so the implementer never needs the analysis notes.

Two kinds of vector, chosen by what the answer is about:

**Policy vectors** for pure decisions (backoff delays, status routing, trigger
admission, `KICKOUT` reason handling, route-cache decisions). They live as
JSON under `research/fixtures/reconnect/` and are run
by a table-driven test against a pure function or the existing reducer. No
clock, no network. Example for question 6:

```json
{
  "question": "RC-Q6",
  "evidence": ["RC-BIN-006"],
  "cases": [
    {"name": "expired token renews once", "loginlist_status": -950, "renewals_used": 0, "want": "renew"},
    {"name": "second expiry stops",       "loginlist_status": -950, "renewals_used": 1, "want": "stop"},
    {"name": "device limit backs off",    "loginlist_status": -328, "want": "retry_backoff"},
    {"name": "unknown status fails closed", "loginlist_status": -99999, "want": "stop"}
  ]
}
```

Rules for policy vectors:

- Every case names the evidence row that justifies it.
- Include the edges: zero, first, last, one past the cap, overflow, stale
  generation, unknown input.
- Where the client adds jitter, give the exact range (`want_min_ms`,
  `want_max_ms`) and the distribution if known. Tests then pin the bounds
  with an injected random source, never a real one.
- An inferred value is marked `"confidence": "inferred"`. A case the
  analysis could not settle is left out and listed as a gap, not guessed.

**Timeline vectors** for behavior visible on the wire (keep-alive, receive
timeout, disconnect then reconnect, `CHANGESVR` then re-login). They are
scripts for the existing scripted backend
([`../testing.md`](../testing.md)): an ordered list of steps, each a
virtual-clock advance, an expected client frame, a server frame to inject,
or a connection event, followed by the expected client state. Example for
question 4:

```json
{
  "question": "RC-Q4",
  "evidence": ["RC-BIN-004"],
  "steps": [
    {"at_ms": 0,      "expect_state": "logged_in"},
    {"at_ms": 60000,  "expect_client": {"method": "PING", "body": {}}},
    {"at_ms": 60000,  "server_reply": {"method": "PING", "status": 0}},
    {"at_ms": 120000, "expect_client": {"method": "PING", "body": {}}},
    {"at_ms": 150000, "no_reply": true},
    {"at_ms": 150000, "expect_state": "disconnected", "expect_pending_failed": 1}
  ]
}
```

(The numbers in these examples are placeholders, not findings.) Timeline
vectors need a virtual clock injected into `internal/client`, which does not
exist yet. The first answer that needs one adds it.

Vectors land in the same PR as the evidence row and the specification
section. If the implementation is not ready, land the vectors with the pure
function that satisfies them, or leave the timeline vector unwired and add
it to the "must build" list in [`../reconnect.md`](../reconnect.md). Never
land a skipped or failing test to CI.

### Gaps

"Could not determine" is a valid answer if it states what was traced, where
the trail ended, and what would settle it (a deeper static pass, a live
experiment, or a decision). Record it as a row with status `Gap` and in the
"Decisions needed" section of [`../reconnect.md`](../reconnect.md) when the
implementation needs a default.

## The questions

Each question lists the sub-questions to answer, where the trace starts and
ends, what the answer must contain, and its vector form. Question IDs
(`RC-Q1` … `RC-Q9`) are stable; cite them in evidence rows and vectors.

### RC-Q1 — Recovery trigger

**Why it matters.** The supervisor must start recovery for exactly the
events the official client does. Reconnecting on the wrong event causes
loops (`KICKOUT`) or wasted `-328` attempts. Missing a trigger leaves the
bridge down.

**Trace.** Start at the carriage agent's status-change publication (the
same point where SL-BIN-024 ends) and follow every observer to the
manager's recovery admission. Then trace backwards from the admission to
every other caller.

**Sub-questions.**

1. Which observers subscribe to carriage status changes, and which one
   decides to start recovery?
2. For each disconnect cause the carriage can publish (socket read/write
   error, TLS/secure-layer failure, remote close, receive timeout, local
   explicit disconnect, handshake failure before `LOGINLIST`), does it
   start recovery, and is the cause visible to the decider?
3. Which non-carriage events also start recovery: network reachability
   becoming reachable, reachability changing interface (Wi-Fi to wired, VPN
   up/down), system wake from sleep, app becoming active or foreground, a
   user-initiated "reconnect", a failed request completion, or a login
   completion hook?
4. Is recovery started synchronously from the callback, or queued (which
   queue or run loop, and is there a delay before admission)?
5. A disconnect that happens *during* a recovery attempt: does it start a
   second recovery, is it absorbed by the running one, or does it fail the
   running one?
6. Does an explicit local logout, a `CHANGESVR` logout, or a `KICKOUT`
   logout publish a status change that the recovery observer then sees? If
   so, what stops it from reconnecting after a `KICKOUT`?
7. What is the "network unreachable" check exactly: a reachability flag, an
   interface query, or the last socket error? Is it re-evaluated right
   before each attempt or only at admission?
8. What is "recovery disabled"? Which component sets and clears it, and
   what user-visible state does it correspond to?
9. Is there a startup difference: does the first login after app launch go
   through the same admission as recovery?

**Answer contains.** A table: trigger → starts recovery (yes/no/conditional)
→ conditions → synchronous or queued → delay. A short state diagram of
admission if the table cannot express it.

**Vectors.** Policy: `trigger`, reducer state flags (`authenticated`,
`network_reachable`, `enabled`, `phase`, `logging_out`) → `admit` or the
suppression reason, which maps to an existing `ErrRecovery*` or a new one.
Timeline: socket close while idle → exactly one recovery attempt; socket
close during recovery → per sub-question 5.

### RC-Q2 — Backoff schedule

**Why it matters.** This is the open "exact recovery delays" item in
SL-BIN-007. Too fast and the server sees a reconnect storm or `-328`; too
slow and the bridge misses messages for no reason.

**Trace.** From the "failure schedules another bounded attempt" branch in
the recovery path, to the timer or dispatch-after that fires the next
attempt, and to every place that reads or writes the delay or attempt
counter.

**Sub-questions.**

1. The delay before the *first* attempt after a disconnect. Is it zero,
   fixed, or random?
2. The growth rule: constant, linear, exponential (base?), a lookup table,
   or something else. Give the delay for attempts 1 through the cap.
3. The cap, if any, and whether it applies before or after jitter.
4. Jitter: present or not, additive or multiplicative, range, and random
   source (uniform? integer seconds?).
5. Which clock and timer: monotonic, wall, or uptime? Does the timer
   survive or get cancelled by system sleep, and is it rescheduled on wake?
6. Reset: what resets the counter (successful `LOGINLIST`, a session that
   stays up for some duration, network reachability change, app
   foreground)? Is there a minimum-stable-time before reset?
7. Do different failure causes use different schedules (network error vs.
   `-328` vs. booking failure vs. ticket-pool exhaustion)?
8. Does a reachability-restored event cut a pending backoff short, or does
   it wait out the timer?
9. How does this schedule compose with the booking (3 attempts, 8 s jitter)
   and ticket (32 s jitter) retries inside one attempt? Is one recovery
   attempt = one full booking/ticket/carriage/`LOGINLIST` pass?

**Answer contains.** A function `delay(attempt, cause, rand) → duration`
written as a formula or table, the reset rule, the clock, and the sleep/wake
behavior.

**Vectors.** Policy: attempt number and cause → exact delay or
`[min, max]`, with an injected random source pinned at its min and max.
Cases for attempt 1, 2, the last uncapped attempt, the first capped attempt,
a large attempt number (overflow), and a reset followed by attempt 1 again.

### RC-Q3 — Retry budget and give-up

**Why it matters.** If the official client stops on its own, the bridge
must match that stop and report it, not retry forever. If it never stops,
the bridge needs its own policy (a decision, not a finding).

**Trace.** From the attempt counter found in RC-Q2 to every comparison
against a limit, and from there to the "exhausted" branch and its logout.

**Sub-questions.**

1. Is there a maximum attempt count, a maximum total elapsed time, or
   neither?
2. What exactly happens on exhaustion: logout without reset (as the
   protocol notes say), a user-visible "disconnected" state, a stop until
   the next trigger from RC-Q1, or a slower background retry?
3. After exhaustion, which trigger (reachability, wake, foreground, user
   action) starts recovery again, and does it start from attempt 1?
4. Are credentials, the route cache, or the resume cursors touched on
   exhaustion?
5. Is the budget per cause (network vs. server status) or shared?

**Answer contains.** The threshold with units, the exhaustion effects as an
ordered list, and the re-arm rule.

**Vectors.** Policy: reducer sequence of `N` failures → the effects after
failure `N-1` and failure `N`; a trigger after exhaustion → `admit` with
attempt reset (or not).

### RC-Q4 — Keep-alive

**Why it matters.** The clean-room client sends nothing while idle. If the
server or a NAT drops idle connections, the bridge disconnects on a timer
and relies on recovery. This is likely the most visible gap.

**Trace.** Look for a periodic timer owned by the carriage agent or the
manager that sends a request while logged in. Start from the method table
for outgoing carriage commands (a `PING`-like method, or any other
periodic command) and from timers created at login completion.

**Sub-questions.**

1. Does the client send a periodic keep-alive at all? Which method name,
   and what body (empty BSON document? fields and types)?
2. Interval, and whether it is fixed, jittered, or adaptive (for example
   different on Wi-Fi vs. cellular, foreground vs. background, or changed
   by a server-provided value in `GETCONF`/`CHECKIN`/`LOGINLIST`).
3. When does the timer start (after `LOGINLIST` success? after `LCHATLIST`
   EOF?) and stop (logout, disconnect, background)? Is it reset by other
   traffic, so a busy session sends no keep-alive?
4. Expected reply: same method, status field, any payload? What counts as
   success?
5. What happens on a missed or error reply: immediate disconnect, count
   several misses, or nothing (TCP decides)? Does a missed keep-alive go
   through the same disconnect path as a receive timeout?
6. Does the client also set socket-level keep-alive (TCP `SO_KEEPALIVE`
   options) on the carriage socket? Values?
7. Does the *server* send anything the client must answer (a server-side
   ping)?
8. Does the booking or ticket connection use keep-alive, or only the
   carriage?

**Answer contains.** Method, body schema with BSON types, interval rule,
start/stop/reset rule, reply rule, failure rule, and any socket options.

**Vectors.** Timeline: idle session → keep-alive at the exact interval;
reply → next one scheduled; no reply → disconnect per sub-question 5 with
pending callbacks failed once; other traffic → per sub-question 3. Policy:
the frame encoder for the keep-alive body (exact BSON bytes of a synthetic
request).

### RC-Q5 — Receive timeout

**Why it matters.** SL-BIN-023 shows that a receive timeout disconnects the
agent. Without the value and scope, the clean-room client either never
times out (a half-open socket hangs forever) or times out an idle, healthy
session.

**Trace.** From the carriage read loop's header read to the timeout
configuration and the branch that publishes the timeout disconnect.

**Sub-questions.**

1. The timeout value and units. Is it per header read, per full frame, or
   per request?
2. Does it apply while idle (no pending requests), or only while at least
   one request is pending? If only with requests pending, what detects a
   dead idle socket (RC-Q4)?
3. Is there a separate per-request timeout that fails one callback without
   disconnecting? Values per command (`LOGINLIST`, `SYNCMSG`, `WRITE`)?
4. Is there a body-read timeout distinct from the header timeout (a slow
   large frame)?
5. Connect and handshake timeouts for booking, ticket, and carriage
   (TCP connect, TLS, secure-layer handshake). The clean-room client uses
   10 s for TCP/TLS dial; confirm or correct.
6. What error does the timeout publish, and does RC-Q1 treat it the same
   as a socket error?

**Answer contains.** A table of every timeout: scope, value, units, clock,
and what happens when it fires.

**Vectors.** Timeline: pending request with no reply → timeout after the
exact value → disconnect, callback failed once, no replay; idle with no
pending request → per sub-question 2; a reply arriving one tick before the
deadline → success.

### RC-Q6 — Status routing on reconnect

**Why it matters.** During recovery, `LOGINLIST` (and booking/ticket) can
fail in many ways. Each must map to one of: retry with backoff, renew once,
or stop and tell the operator. Retrying a terminal status burns attempts
and may look abusive; stopping on a transient one leaves the bridge down.

**Trace.** From the `LOGINLIST` response handling in the recovery path (not
the first login; confirm whether they differ) to each branch on `status`,
and from booking/check-in failure handling to the recovery decision.

**Sub-questions.**

1. For every `LOGINLIST` status the client branches on, give the action:
   retry (which backoff from RC-Q2), renew token once, re-register device,
   logout without reset, logout with reset, or show an error and stop.
   Include at least `0`, `-305`, `-310`, `-328`, `-445`, `-950`, and every
   other constant compared against `status` in that path.
2. Unknown status: the default branch. Retry or stop?
3. Does recovery-time handling differ from first-login handling for any
   status?
4. Token renewal during recovery: is it the same one-renewal-then-one-login
   rule already implemented for `-950`? What if renewal itself fails with a
   network error vs. an auth error?
5. Do `LOGINLIST` error message/URL/label fields change the action (for
   example a URL that means "open this page", which a bridge should turn
   into a stop and an operator notice)?
6. HTTP-layer statuses on the renewal and booking paths (401, 5xx, and the
   shared JSON `status` envelope): which retry and which stop?
7. Network-class failures (DNS, connect refused, TLS failure, secure-layer
   handshake rejected): all retry? Any that stop?
8. Which of these outcomes are shown to the user, and with what severity?
   (That tells the bridge which bridge state to report.)

**Answer contains.** One table: source (booking, check-in, `LOGINLIST`,
renewal) × status → action → backoff class → user-visible outcome. The
default row for unknown values is required.

**Vectors.** Policy: exactly that table as vector cases (see the example in
[Answer format](#3-conformance-vectors-the-tests)), plus renewal-count and
unknown-status edges.

### RC-Q7 — `CHANGESVR` follow-up

**Why it matters.** `CHANGESVR` is terminal for the current session but is
not a reason to stop. The bridge must log in again, on a new route, without
looping if the server keeps sending it.

**Trace.** From the change-server logout (SL-BIN-025) forward: what the
logout publishes, who observes it, and what starts the next login. Then the
ticket-address cursor from where it is advanced to where booking/check-in
reads it.

**Sub-questions.**

1. What starts the next login after the change-server logout: the recovery
   observer from RC-Q1, a dedicated follow-up, or nothing until another
   trigger?
2. Is the next login immediate, delayed by a fixed amount, or scheduled
   through the RC-Q2 backoff? Does it count against the RC-Q3 budget?
3. Does the next login redo booking (`GETCONF`), redo check-in (`CHECKIN`)
   only, or connect straight to the next ticket address?
4. The ticket-address cursor: what list it indexes, where the list comes
   from (booking response? check-in response?), what "advance when there is
   a candidate" means at the end of the list (wrap, re-book, or stop), and
   when the cursor resets.
5. Are resume cursors preserved, so the next login is a resumed login with
   the same `chatIds`/`maxIds`, `lastTokenId`, and `lbk`?
6. Repeated `CHANGESVR`: is there any guard against a loop (count, time
   window)?
7. Requests in flight when `CHANGESVR` arrives: failed once like a socket
   drop, or handled differently?
8. Does `CHANGESVR` ever arrive before `LOGINLIST` completes, and is it
   handled the same way then?

**Answer contains.** The ordered effect list from receipt to the next
`LOGINLIST`, the cursor rule, and the loop guard (or its absence).

**Vectors.** Policy: reducer `ChangeServer` → effects including the new
"schedule login" effect and cursor advance; cursor at last candidate → the
end-of-list rule. Timeline: `CHANGESVR` while idle → route cleared, the
right booking/check-in steps, resumed `LOGINLIST` to the next address,
catch-up; `CHANGESVR` with a pending `WRITE` → the `WRITE` fails once and is
not replayed.

### RC-Q8 — `KICKOUT` reasons

**Why it matters.** Reasons 1 and 10 are known to reset the database. The
bridge needs to know which reasons mean "this device is gone" (re-register)
vs. "another login replaced you" vs. anything that might be safe to recover
from.

**Trace.** From the kickout consumer's reason read (SL-BIN-025) through the
logout-with-reset selection and on into UI: every comparison against the
reason, every string or alert chosen by it, and every post-logout action.

**Sub-questions.**

1. Every reason value the client compares against, and what each changes:
   reset, alert text key (not the text itself if it is a proprietary
   asset; a neutral description is enough), whether credentials are
   deleted, whether the device registration is cleared.
2. The default for an unknown reason.
3. Does any reason lead to an automatic login afterwards, or are they all
   terminal until user action?
4. Other keys in the `KICKOUT` user-info besides the reason (message, URL),
   and whether they change behavior.
5. Is the "logged in and not logging out" guard the only guard, or is
   there a generation check too?
6. Does a `KICKOUT` also clear the route cache or the ticket cursor?

**Answer contains.** A reason table: value → reset (y/n) → credentials
cleared (y/n) → registration cleared (y/n) → auto re-login (y/n) → neutral
description. Unknown default row required. Human meanings may stay
"unproven" (that needs RC-LIVE work); record them as such.

**Vectors.** Policy: reason → effects, for every traced value plus 0,
negative, and an unknown value.

### RC-Q9 — Endpoint cache on failure

**Why it matters.** Reusing a dead route wastes an attempt per retry;
dropping a good one forces an unnecessary booking round trip, which has its
own attempt limit.

**Trace.** Every write to the cached carriage route and the ticket cursor:
set on login success, cleared on matching-endpoint failure (SL-BIN-007),
cleared on `CHANGESVR`, and any other clear or expiry path.

**Sub-questions.**

1. Which failures clear the cached route: TCP connect failure, TLS or
   secure handshake failure, `LOGINLIST` error status (which ones), receive
   timeout, remote close after a successful login?
2. Which failures keep it, so the next attempt reuses it?
3. "Matching endpoint": is the comparison on host and port, host only, or
   a resolved IP?
4. Cache expiry value, units, and clock (uptime is known; the value is
   not). Is the expiry from the booking response or a constant?
5. Where is the cache stored (memory only, or persisted across app
   restarts)? If persisted, does the first login after launch use it?
6. After the cache is cleared, does the next attempt redo booking or only
   check-in?
7. How does this interact with the ticket-address cursor from RC-Q7? Are
   they one mechanism or two?

**Answer contains.** A table: failure kind → cache (keep, clear) → cursor
(keep, advance, reset) → next attempt starts at (carriage, check-in,
booking). Plus the expiry rule.

**Vectors.** Policy: extend the existing `EndpointCache` tests with each
failure kind → the decision; expiry exactly at and one tick past the
deadline.

## Order of work

RC-Q1, RC-Q2, and RC-Q3 share one trace (trigger → admission → schedule →
budget) and should be answered together. RC-Q4 and RC-Q5 share the carriage
read and timer code. RC-Q6 depends on RC-Q2 for its backoff classes. RC-Q7
and RC-Q9 share the route and cursor state. RC-Q8 is independent.

Suggested order, by bridge impact: RC-Q4/Q5 (idle drops), RC-Q1/Q2/Q3
(supervisor core), RC-Q6 (status routing), RC-Q7/Q9 (route), RC-Q8.

The exit criterion in [`../reconnect.md`](../reconnect.md) stands: RC-Q1
through RC-Q7 answered with provenance and confidence; RC-Q8 and RC-Q9
answered or recorded as explicit gaps.

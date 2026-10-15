# Bridge reconnect policy

This document records the connector policy implemented for bridge recovery. It
is a bridge continuity choice, not a claim about KakaoTalk official-client
parity.

The protocol client remains a single-session owner. The connector supervisor
owns generation, cancellation, recovery timers, and bridge state. When a live
stream ends, it first joins the event loop and calls `Shutdown` on the old
client. Only after that cleanup succeeds does it book a replacement client.
This ordering preserves the profile lease and prevents two sessions from
sharing a profile. An explicit disconnect cancels an active bootstrap and
pending timer, increments the generation, and waits for the cleanup owner.

Recovery from a retryable failure never gives up (2026-10-11). Since
2026-10-15 it follows the official Mac client's recovery episode
([CONNECT-TRAFFIC finding #7](bridge/CONNECT-TRAFFIC.md#finding-7-reconnect-schedule)).
A session loss starts an episode whose first attempt runs after 0.1 s. A
failed attempt retries after a fixed 10 s, with no growth and no jitter. A
successful login ends the episode, so the next loss starts again at 0.1 s.
The first connect is an episode's first attempt too, so a network outage at
startup retries every 10 s. A network failure during bootstrap (the GETCONF
and CHECKIN dials and requests, before LOGINLIST sends any credentials) is
retryable. Until 2026-10-13 the client hid its cause, so a DNS failure right
after a lab Mac woke from sleep stopped the bridge for good. A transport
failure during LOGINLIST itself is still classified only as a login failure
and is not retried.

No attempt runs at or after 600 s from the episode's first attempt, the
native watchdog. Native then ends the episode and logs out. The bridge
instead reports `UNKNOWN_ERROR` `kakao-recovery-stalled` once and keeps
retrying every 15 minutes (deviation: an unattended service has no user or
wake event to restart recovery).

A known `LOGINLIST -328` device limit keeps the bridge's own ladder,
independent of the 10 s cadence: the episode's consecutive `-328` replies
wait 60, 120, 240, 480 and 900 seconds, then 15 minutes with the stall
report. An ordinary failure in the same episode returns to the 10 s cadence
while the watchdog allows it.

Sessions that end within 60 s of their successful login, more than three
times in a row, start their episodes on the 15-minute tier (deviation: native
has no such guard). A session that lasts 60 s or longer resets the count. It
stops a server that repeatedly sends `CHANGESVR` or closes the session right
after login from causing a login every few seconds.

The schedule survives a process restart (2026-10-11). The login's metadata
records when each login attempt began, the episode's start, its failed
attempts and `-328` count, the short-session count, the slow tier, and the
time a scheduled retry may log in. On its first `Connect`, a freshly loaded
login continues a saved episode: it waits out a pending retry, or retries at
once if the retry came due while the process was down. Otherwise, if the
previous process's last login began less than 60 s before the restart, the
restart counts as a short session; after three of them in a row it waits
15 minutes from that login. It reports `TRANSIENT_DISCONNECT`
`kakao-reconnect-scheduled` while it waits. A supervisor that restarts a
crashing bridge therefore cannot log in on every restart. Later `Connect`
calls in the same process keep the in-memory schedule.

`GETCONF` `wifi.bgReconnItv` is not read: the Mac client stores it but never
uses it.

These do not retry: profile-open or lease failures, `ErrLogin`, credential
renewal failures, unknown `LOGINLIST` status errors, a second `-950`, and
source-side catch-up failures. Outbound Matrix sends fail while disconnected
and are never queued for implicit resend. An accepted write whose response is
lost remains ambiguous and is sent only once.

Catch-up still runs before live subscription. An event that cannot be
delivered holds its chat (below) instead of aborting bootstrap; a source-side
catch-up failure still aborts it and leaves the checkpoint unchanged. An
unrecoverable bounded gap remains represented by the existing gap notice
policy.

### Delivery holds (2026-10-14)

A failed delivery is not a failed Kakao session. Matrix, an inbound media
download, the `GETMSGS` read that completes an edit feed, and the roster read
of a NEWMEM/DELMEM notice are dependencies of delivery; no traced official
path turns their failure into logout or recovery
([native skew audit](bridge/NATIVE-SKEW-AUDIT.md) finding 2). Until this change
a failed delivery ended the session, and catch-up replayed the same event after
a full relogin, so a persistent failure logged in again on every step of the
schedule above.

The event pump now holds the chat instead (`delivery_hold.go`). The failed
event and the chat's later events stay uncommitted and are buffered in source
order (at most 1024; later positioned events are dropped and read again by
catch-up when the chat resumes). Other chats keep flowing: commits are per
chat, so no chat's cursor passes an undelivered event. A catch-up or seeded
event that fails holds its chat the same way, and the session goes live.

- **Retry:** the held chat is retried in the live session after
  delays of 5, 10, 20, 40 and 60 s (the reconnect ladder before 2026-10-15),
  with no relogin.
- **Cap:** after those five retries the chat is paused. It is not retried on a
  timer again, and the bridge reports `UNKNOWN_ERROR`
  `kakao-chat-delivery-paused`.
- **Source access:** a chat whose group is paused for unconverged membership
  (or whose source access was removed) is not retried on a timer at all. A
  failed membership refresh already schedules its own membership retry; the
  held chat resumes when that retry converges the roster.
- **Resume:** a paused chat resumes when its membership converges, when the
  operator sends `reconnect` (which, while connected, retries the paused chats
  in the live session instead of logging in), or at the next session, whose
  catch-up replays the uncommitted interval.
- **Transport:** a genuine Kakao transport failure still ends the session,
  because the source stream closes. An event that belongs to no chat (for
  example a terminal notice) cannot be held and still ends the session when it
  fails.

KICKOUT is terminal and preserves the continuity store. The official client
logs out on KICKOUT, so the bridge saves the notice and its reason on the
login's metadata. It reports `BAD_CREDENTIALS` `kakao-kicked-out` with the
reason, and refuses to reconnect, including after a restart, until the login
is removed and a new one is created. Without this, a restart would log in
again with the same credentials and could displace whichever device caused
the kick. The state message follows the official logout alert, including the
server's explanation text and help link when present
([KICKOUT notification](reconnect/KICKOUT-NOTIFICATION.md#logout-alert)).

The once-a-minute group profile refresh ends the session when its source
request fails on the connection: the session closed, the transport failed, or
no reply arrived within its 30 second timeout (2026-10-13). The bridge then
reports `kakao-disconnected` and recovers on the schedule above, and recovery's
group discovery refreshes the paused group. Before this, a network outage was
reported as `kakao-group-membership-pending`, which asks the operator to repair
Matrix rooms, and the dead connection went unnoticed: each refresh request
cancels the keepalive ping and re-arms it for a full interval, so with managed
groups the ping never fired. In an owned Docker run with all Kakao traffic
dropped, the bridge still reported itself connected four minutes into the
outage. Since 2026-10-13 the client also implements the official client's
receive-header timeout: a request that gets no reply within GETCONF's
`recvHeaderTimeout` (20 seconds observed) closes the carriage, whichever
request it was, and the bridge recovers as for any dropped connection.

`!reconcile-group` and `!complete-group-invitations` use the live session
when there is one. Otherwise they open a detached profile client and log in,
because an unresolved or partially invited group creation fails every
bootstrap until it is reconciled, so reconnecting first cannot help. Since
2026-10-14 that detached login passes the same gates as reconnecting: it is
refused after KICKOUT and while a retry is scheduled (in this process, or
restored from the login's metadata after a restart), since that retry may be
waiting out `-328`. The login is recorded in the persisted schedule like any
other ([native skew audit](bridge/NATIVE-SKEW-AUDIT.md) finding 3).

CHANGESVR performs old-session cleanup and books a fresh session. It does not
replay outbound mutations.

The current source dossiers establish lifecycle ordering and callback behavior
but do not establish official reconnect delays, retry budgets, or all status
code meanings. Those values are therefore explicit bridge policy in this
implementation and should not be cited as protocol observations. Keepalive
watchdogs and a durable bounded-gap/poison-message policy remain separate
follow-up work.

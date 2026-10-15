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

Recovery from a retryable failure never gives up (2026-10-11). The first
five attempts after an outage follow a fast schedule: ordinary failures wait
5, 10, 20, 40 and 60 seconds, and a known `LOGINLIST -328` rate limit waits
60, 120, 240, 480 and 900 seconds. After that, the bridge reports
`UNKNOWN_ERROR` `kakao-recovery-stalled` once and keeps retrying every 15
minutes. The first connect retries on the same schedule, so a network outage
at startup no longer leaves the bridge down until a restart. A network failure
during bootstrap (the GETCONF and CHECKIN dials and requests, before LOGINLIST
sends any credentials) is retryable. Until 2026-10-13 the client hid its cause,
so a DNS failure right after a lab Mac woke from sleep stopped the bridge for
good. A transport failure during LOGINLIST itself is still classified only as
a login failure and is not retried.

The schedule restarts only after a session has stayed connected for five
minutes. A session that ends sooner, for example by repeated `CHANGESVR` or a
close right after login, keeps advancing it, so the bridge cannot log in every
few seconds forever.

The schedule survives a process restart (2026-10-11). The login's metadata
records when each login attempt began, its attempt number, and the time a
scheduled retry may log in. On its first `Connect`, a freshly loaded login
waits out a pending retry. If the previous process logged in less than five
minutes before it ended, the restart counts as a short session: the attempt
number advances and the bridge waits for that attempt's delay, measured
from the previous login. It reports `TRANSIENT_DISCONNECT`
`kakao-reconnect-scheduled` while it waits. A supervisor that restarts a
crashing bridge therefore follows the same 5 s to 15 minute schedule
instead of logging in on every restart. A restart more than five minutes
after the last login connects at once. Later `Connect` calls in the same
process keep the in-memory schedule.

These do not retry: profile-open or lease failures, `ErrLogin`, credential
renewal failures, unknown `LOGINLIST` status errors, a second `-950`, and
source-side catch-up failures. Outbound Matrix sends fail while disconnected
and are never queued for implicit resend. An accepted write whose response is
lost remains ambiguous and is sent only once.

Catch-up still runs before live subscription. A conversion or commit failure
aborts bootstrap, leaves the checkpoint unchanged, and is not replayed in a
tight recovery loop. An unrecoverable bounded gap remains represented by the
existing gap notice policy.

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

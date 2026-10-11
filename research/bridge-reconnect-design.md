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
at startup no longer leaves the bridge down until a restart.

The schedule restarts only after a session has stayed connected for five
minutes. A session that ends sooner, for example by repeated `CHANGESVR` or a
close right after login, keeps advancing it, so the bridge cannot log in every
few seconds forever.

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

CHANGESVR performs old-session cleanup and books a fresh session. It does not
replay outbound mutations.

The current source dossiers establish lifecycle ordering and callback behavior
but do not establish official reconnect delays, retry budgets, or all status
code meanings. Those values are therefore explicit bridge policy in this
implementation and should not be cited as protocol observations. Keepalive
watchdogs and a durable bounded-gap/poison-message policy remain separate
follow-up work.

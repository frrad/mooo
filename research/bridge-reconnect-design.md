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

Recovery is bounded to five attempts per outage. Ordinary attempts wait 5, 10,
20, 40, and 60 seconds. A known `LOGINLIST -328` rate limit uses 60, 120, 240,
480, and 900 seconds. Profile-open/lease failures, `ErrLogin`, credential
renewal failures, unknown `LOGINLIST` status errors, and a second `-950` do not
start a loop. Outbound Matrix sends fail while disconnected and are never
queued for implicit resend; an accepted write whose response is lost remains
ambiguous and is sent only once.

Catch-up still runs before live subscription. A conversion or commit failure
aborts bootstrap, leaves the checkpoint unchanged, and is not replayed in a
tight recovery loop. An unrecoverable bounded gap remains represented by the
existing gap notice policy. KICKOUT is terminal for the bridge session and
preserves the continuity store. CHANGESVR performs old-session cleanup and
books a fresh session; it does not replay outbound mutations.

The current source dossiers establish lifecycle ordering and callback behavior
but do not establish official reconnect delays, retry budgets, or all status
code meanings. Those values are therefore explicit bridge policy in this
implementation and should not be cited as protocol observations. Keepalive
watchdogs and a durable bounded-gap/poison-message policy remain separate
follow-up work.

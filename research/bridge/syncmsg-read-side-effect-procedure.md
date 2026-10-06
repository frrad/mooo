# SYNCMSG read-side-effect differential

This is a proposed controlled experiment, not a live validation result. It
resolves whether `SYNCMSG` with `cnt=0` changes Kakao's server-side unread/read
state while the client records a local read watermark. Run only with two
maintainer-owned disposable accounts and preserve captures outside the
repository.

The source contract is already bounded: catch-up sends `chatId`, `cur`, `max`,
and `cnt=0`; the one-message read helper sends the same interval with `cnt=1`;
the client persists its local watermark only after a successful response. The
remaining question is the server-side effect of the `cnt=0` form and whether a
restart changes the observation.

Begin each trial with a fresh LOGINLIST/session baseline and an exact captured
`cnt=0` request. Existing private probes that use the normal page size are not
evidence for this differential and must not be substituted for it.

For each trial, record only sanitized timestamps, chat/log positions, request
shape, response status, and the before/after unread indicator. Do not record
message text, account identifiers, tokens, or raw packets.

1. Prepare two disposable clients in the same owned chat. Leave one unread
   message at a known log position and do not open it in the official client.
2. Use a staged client/session probe with a pause after LOGINLIST and before
   SYNCMSG. Do not assume a fresh bridge process can pause there: production
   CatchUp follows LOGINLIST immediately. Have sender A record recipient B's
   unread/receipt indicator before releasing the request, while B's official
   client stays outside the chat. Capture the exact request and confirm `cnt=0`,
   then release once and stop after the response/local checkpoint write.
3. Observe the same indicator after the request without opening B's target.
   Repeat with a new unread message and log position for every trial and
   control, so each has its own pre-release baseline.
4. Repeat with the explicit `MarkRead`/`cnt=1` helper as the positive control,
   verifying that the exact `cnt=1` request was sent. Use a distinct new target
   because a watermark persisted by the `cnt=0` trial would suppress a later
   `MarkRead`; an accepted valid empty interval is a separate no-op control, not
   an invented request shape.
5. Compare the server indicator, local watermark, response status, and replay
   behavior across trials. A server change without local watermark change is a
   distinct outcome from a local-only change.

If a read mutation response is ambiguous or fails, do not automatically replay
it; record the outcome for operator review. Restart/resume behavior is a
separate phase after the first differential and uses a fresh target.

Synthetic evidence must cover request fields, `cnt=0` paging, response failure,
local watermark commit ordering, restart suppression of an already persisted
watermark, and no-progress detection. It cannot establish the server-side read
effect. Until the controlled differential is complete, opt-in backfill remains
a disabled acceptance gap. Read receipts no longer depend on it: since
checkpoint v5, catch-up does not record a read acknowledgement
([policy](read-receipt-policy.md)).

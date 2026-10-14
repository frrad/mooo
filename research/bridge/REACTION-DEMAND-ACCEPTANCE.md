# Bounded reaction demand: owned acceptance

Date: 2026-10-14. Implementation: PR #358, rebased onto `3013b2ba`.
This is observed clean-room bridge behavior, not executed official-client parity.
The scheduling/type rationale and remaining Mac trace gaps are in
[reaction scheduling](REACTION-SYNC-SCHEDULING.md).

## Method and scope

Used an existing disposable account's approved secondary profile (Mac-class
26.8.0), its owned Android primary (26.8.2), and the existing local Synapse
appservice with the existing encrypted Matrix tester device. Before source
mutations, fresh inventory and membership checks established a regular group
with exactly three owned participants. No new enrollment, session import,
backup/restore, or primary personal account was used.

Ran the normal bridge executable against its existing database/profile and sent
`!kakao sync-reactions` through the actual encrypted Matrix command handler.
A private build overlay wrapped the existing HTTP client without replacing
endpoints or changing scheduling, decoding, delivery, or persistence. It counted
metadata requests/statuses and recorded connection readiness. A one-shot fault
returned an error before forwarding one metadata read. It did not alter source
mutations or authentication. Private receipts, logs and native UI captures stayed
outside tracked files; this document contains no account or event identifiers.

A controlled target text was sent once through encrypted Matrix and mapped to
the source group. Native Matrix event inspection confirmed ciphertext on the
server, and the tester decrypted the exact text. Source reaction mutations were
reserved once, issued only while the bridge was stopped, and verified through
fresh server reaction membership.

## Observations

| Check | Observed result |
| --- | --- |
| Normal reconnect | Connection reached `CONNECTED` with an observed real client constructor and zero metadata HTTP requests before explicit demand. Repeated restarts did not sweep populated portals. |
| Offline addition | One owned legacy heart added while disconnected was recovered by the per-room command with one successful metadata request. The durable reaction row and Matrix relation had the expected target, emoji and sender. |
| Repeat after cooldown | One empty successful metadata read reused the same reaction event and durable cursor. No duplicate reaction appeared. |
| Immediate repeated command | Actual cooldown reply; zero additional metadata requests. |
| Offline removal | One owned cancellation while disconnected left the existing Matrix reaction untouched through reconnect. Explicit demand made one successful metadata request, advanced the cursor and removed the reaction row. Native Matrix event inspection confirmed the original reaction event was redacted. |
| Business exclusion | Actual command in a real owned `PlusChat` returned disabled/unsupported and made zero metadata HTTP requests despite a positive test cursor. |
| One-shot read failure | One metadata read was refused before the wire; the cursor and reaction rows remained unchanged. |
| Explicit retry after reconnect | One successful empty metadata read preserved the removed-state cursor and zero reaction rows, with the actual command completion reply. |

The completed supported-room demand checks made four successful metadata HTTP
requests in total; the injected failure added one pre-wire denial. Interrupted
attempts are retained in private logs and are not counted as successful checks.

## Business negative-control setup

The account initially had no business chat. A temporary subscription to a
verified public business channel created a real `PlusChat` in fresh source
inventory. The normal inventory seeder only seeds direct/regular-group portals,
so the operator initialized a local portal stub bound to that actual source ID
and a synthetic positive reaction cursor. The standard `create-portal` handler
then retrieved source chat information and created the real Matrix portal. The
positive cursor ensures an empty room cannot trivially avoid a metadata request.
This is command eligibility acceptance, not natural inbound business-message
portal-creation acceptance.

The created business Matrix room was unencrypted and initially required an
operator invitation for the tester. The encrypted helper refused before sending;
the native Matrix API then sent the actual per-room command once in a fresh
normal connection. The command returned the unsupported-room response and
preserved the synthetic cursor with zero metadata requests. No text was sent to
the source channel. The temporary subscription was removed through the primary
client, the synthetic cursor was deleted, and all bridge processes started for
this run were stopped. The created local test room remains available for lab
inspection; transient native screenshots and UI dumps were removed.

## Limits and interrupted attempts

Standard Matrix appservices provide no viewing callback. First-view timing,
heartbeat expiry, revision admission, room-switch cancellation and disconnect
cancellation have production-path synthetic coverage; this run does not provide
native window-viewing or Beeper acceptance. A real eligible room with `dlm=true`
was not available; its exclusion and malformed-value handling remain static and
synthetic evidence. Missing `dlm` compatibility is an implementation decision.

The existing periodic group-profile refresh sometimes exceeded its five-second
context and disconnected the bridge. Some early repeat/retry attempts were
interrupted by that behavior. Fresh normal connections allowed the completed
checks above; this acceptance does not establish general connection stability.
A premature target command failed before source sending, and was not replayed.
An initial pre-command reaction-row audit used an incorrect message-ID prefix;
it is excluded from the evidence. The removal check used the actual production
message-ID format and directly established the before/after row state.

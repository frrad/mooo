# Three-flow history acceptance: evidence in progress

Checkpoint: 2026-10-14. **The complete acceptance is not established.** Cloud
backup and restore are excluded. SQLite remains the selected lab database; no
PostgreSQL migration was performed. This document collects observations for the
eventual combined acceptance report, without treating separately passing slices
as a successful clean-login → room-opening → backward-scroll experiment.

## Official Mac login failure

The isolated Mac 26.8.0 client completed verification in the retained experiment
but did not reach successful history acquisition. The cause is still unverified.
The changed bundle identity, signing and app-group configuration are hypotheses,
not demonstrated causes. Retained system logs contain sixteen HTTP completions
with error `-1000`, also represented by sixteen load-failure records, before
verification. Their timing does not tie them to the later session failure.
Encrypted application logs have not been decrypted; no plaintext explanation is
available from them.

Read-only analysis of the authorized Mac binary on 2026-10-14 separates these
otherwise easily conflated paths:

| Path | Recovered condition/effect | Evidentiary limit |
| --- | --- | --- |
| Login-screen network dialog, callback `0x100b201bc` | Checks `auth.isLoggedIn == false` and `sysMon.isNetworkUnreachable`; selects the `Login_Modal_NetworkConnectFail` localization keys. | Its triggering caller has not been established; the failed lab attempt has not been attributed to this callback. |
| System-monitor reachability getters | `isNetworkReachable` at `0x101652290` returns the low status bit; `isNetworkUnreachable` at `0x1016522a8` inverts the reachable result. | This is system-monitor state, not an observed LOCO response or login error code. |
| System-monitor status transition, `0x1016530b0` | A changed reachability value updates status, invokes the network-status delegate, and starts or stops the idle timer. | No controlled execution of this transition was captured during the failed login. The login-screen notification handler is distinct from the dialog callback. |
| Auto-login error callback, `0x1013ae8ac` | Distinguishes `NSURLErrorDomain` from `LPErrorDomain`; the former selects the common network-error dialog and calls `logoutWithResetDatabase:false`. `LPErrorDomain` code 2 selects the kickout dialog. | Recovered static behavior, not proof of which error occurred in the fresh verification experiment. Remaining domain/code branches and downstream logout persistence require separate tracing. |

The next controlled failure capture needs authentication stage/result, the
system-monitor status transition, the actual error domain/code delivered to the
relevant callback, and whether LOGINLIST/history acquisition began. Record only
stage/method/status/timing in publishable traces. Device/session/authentication
values, private messages, captures and official logs remain outside Git. Do not
retry a policy-restricted account merely because its earliest retry time passed.

## Controlled Android opening and backward viewing

On 2026-10-14, already signed-in owned Android primary clients running KakaoTalk
26.8.2 were used in one regular group with three verified owned participants.
The room was selected using a unique synthetic message preview and participant
count, rather than its locally assigned name. No cloud restore was performed.

An already-read opening showed the latest control and a previously cached text.
Twelve paced backward gestures exposed two older synthetic text controls once
each, in the order of their correlated SDK source identities. The source maximum,
read watermark and unread count did not change in that already-read case.

A separate controlled unread experiment used one new synthetic text sent by an
owned primary client while the receiving primary was outside the room. Fresh SDK
login-boundary inventory snapshots established this sequence:

| Step | Unread count | Source maximum | Read watermark |
| --- | --- | --- | --- |
| Before the new control | 0 | Previous maximum | Previous watermark |
| After the control | 1 | Advanced to the control | Unchanged |
| After one zero-held-count history acquisition of the new interval | 1 | Unchanged | Unchanged |
| After visibly opening the receiving primary's room | 0 | Unchanged | Advanced exactly to the control |
| After a confirmed close/reopen | 0 | Unchanged | Unchanged |

The acquisition returned the exact controlled text and source identity. These
observations distinguish history acquisition from viewing/read effects in this
Android case. They do not establish the phone's read packet, timing or persistent
callback chain, nor Mac secondary-device parity. Native source identities were
correlated through unique synthetic bodies rather than extracted from the phone
database. Native wire pagination and storage-query boundaries remain untraced.

The Android client displayed older controls that a later secondary-device request
could not acquire below the observed source floor. That difference does not
establish a retention duration, purely local native retrieval, or use of an
incorrect bridge endpoint.

## Current source acquisition and encrypted SQLite delivery

One selected regular group was acquired using the production SDK on an existing
authorized secondary device with an empty local history cache. Original-profile
ownership was held throughout acquisition; version, current regular-room metadata
and exactly three owned members were checked before history dispatch. The clone
used the original account's scheduler directory. This is not fresh device
enrollment or a successful official clean login.

One bounded startup bulk page returned three records and no pending room.
Reading the acquired window required no SYNCMSG. An initial helper failed during
reopen because it omitted the shared scheduler directory while retaining the
original profile lease. Acquisition was not repeated: a separate corrected
process returned the same complete page with HTTP and LOCO dialers denied, zero
network attempts, and unchanged read/delivery continuity. A subsequent normal
restart verified current inventory and the owned roster, then acquired zero bulk
pages with zero SYNCMSG and explicit read requests. That restart also preserved
the original profile hash and read/delivery continuity.

The same acquired window fed production bridge conversion, `FetchMessages`,
encrypted batch delivery and the actual backward executor against the local
SQLite Synapse 1.78.0 lab backend. The backend's restricted historical-insertion
module persists through Synapse's event machinery and exposes the endpoint used
by the Matrix SDK. Client `/messages` pagination is the actual homeserver API.
Rooms are exactly allowlisted, version 1 and nonfederated. Local signing under
the SDK's deterministic event-ID domain is a lab accommodation; it establishes
no federation compatibility or remote-domain signing authority. This experiment
does not demonstrate capability on ordinary Synapse or Beeper deployments.

The newest record was delivered first. The next historical batch was accepted by
the homeserver, but its real HTTP response was drained and replaced with a lost
response error. The bridge retained a real SQL backfill task and did not confirm
the pending message mapping. A new process after homeserver restart replayed the
same deterministic event identity and delivered the final older record.

Actual encrypted `/messages` returned pages of **1, 1, 1, 0** using a token saved
before restart. All three identities were unique and in expected backward source
order; a fresh complete pagination returned the same identities and timestamps.
The durable bridge database contained exactly the corresponding three unique
source/part and Matrix-event mappings. The new unread control from the Android
experiment was included in this acquired and delivered window.

The same normal recipient device and SDK crypto store decrypted all three
records. Bodies, message types and timestamps matched production live conversion.
No crypto store reset, key import, changed trust setting or forced shared-session
flag was used. A durable SQLite to-device watermark in the private lab module
prevents native stream-counter rewind after an empty-inbox server restart; its
independent restart/key-delivery evidence must remain distinct from bridge source
behavior.

Delivery and repeated satisfied backward execution made zero source history,
dependency or resource calls and zero explicit read/commit actions. These phases
use a verified cached inventory adapter, the production executor and durable SQL
task loading, rather than the complete bridge worker and a real client scroll
trigger. They cover three single-part records, not every supported content class,
a current successful missing-prefix acquisition, competing rooms or equal source
timestamps. Earlier synthetic equal-timestamp and owned five-record experiments
are separate evidence and do not expand this run's scope.

## Cache gap, bounded acquisition and startup cursor correction

A separate owned run began with only the newest of the same three source records
in its cache. Production cached delivery, queued slow-fetch deferral and reopening
that satisfied suffix made zero source calls. One real zero-held-count SYNCMSG
acquired the oldest record as a short page. The test adapter permitted only one
source request, so further acquisition stopped before delivery with the middle
record still missing. The acquired prefix survived in the durable store; cached
read results at that point described the connected prefix, not the total number
of records in all disconnected pages. This is not a new below-floor observation.

The investigation exposed a production startup bug: a disconnected suffix could
raise the acquired bulk cursor to the latest identity, causing startup to report
no pending work despite missing preceding coverage. A failing production-path
regression first reproduced the skip both with reconstructed page state and with
an older bulk journal. Startup now derives its cursor from the same connected
durable coverage used by the history reader. It preserves the existing journal
validation and does not use read/delivery watermarks as acquisition progress.
Regressions also exercise budget exhaustion, restart at the unresolved gap, joining
the recovered prefix to a cached suffix, identity/order/content deduplication and
zero acquisition on a satisfied restart. These tests establish the implementation
invariant; they are not executed official-coordinator parity fixtures.

After the correction, one explicitly selected startup bulk page resumed from the
acquired oldest identity. Its two raw records were the missing middle record and
the already cached newest record. An offline audit checked those exact identities
and their authors against the three owned participants. The overlapping newest
record remained deduplicated. The cache became complete with three identities;
the original profile hash and local read/delivery continuity stayed unchanged.
This was an explicit SDK startup acquisition following budget exhaustion, not an
automatic bulk fallback added to backward paging. The bulk request has no upper
cutoff that would exclude the cached suffix; the returned overlap is documented.

Production encrypted backward delivery of the recovered two-record prefix then
lost a genuinely accepted HTTP response. After a homeserver restart, a fresh
process loaded the retained SQL task and confirmed the same deterministic IDs.
Real client pagination using its saved token returned **1, 1, 1, 0**; the normal
recipient decrypted all three records against production live conversion.
Satisfied repeated paging made zero source calls. This closes a real cache-gap
acquisition/delivery recovery case on the restricted SQLite lab endpoint, while
the complete client-driven sequence, native Mac reference and competing-request
acceptance remain open.

## Gates still open

- Diagnose the actual Mac failure and establish official clean-login visible
  history, pagination, persistence, dependencies and read/failure behavior.
- Trace the native opening/viewing signal before adding any bridge interface;
  Android primary opening alone does not justify a Mac secondary read policy.
- Complete actual client-driven bridge opening and backward paging, including
  successful cache-gap acquisition, interruption and the full worker lifecycle.
- Verify declared content classes and retryable dependencies against observed or
  executed official evidence, including media, feeds, edits and deletions.
- Complete competing startup, room, paging and dependency pacing/recovery
  acceptance across profiles and processes.
- Run the complete three-flow sequence and interruption/restart repetition;
  publish the sanitized reproducible fixtures/traces and remaining deviations.

Private evidence is retained in owner-only lab storage. No account identifiers,
message bodies, credentials, raw official code or private captures are included
here. The isolated historical homeserver and all experiment helpers were stopped
after these runs; the existing unrelated local homeserver was preserved.

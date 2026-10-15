# Native behavior skew audit

Status: static audit, 2026-10-14. Bridge code at `d1bbe08b` (PR #377) was
compared with the established official macOS KakaoTalk 26.8.0 (build 2000)
behavior recorded in `research/`. No Go code changed. No new login, live
experiment or binary query was run for this audit. Every native claim below
cites an existing dossier. Where none exists, the row is marked untraced.

## Scope and method

1. Every Kakao-side request the bridge can send was inventoried from source in
   `internal/client`, `internal/continuity`, `internal/protocol` and
   `internal/bridge/connector`. For each request the inventory lists every
   trigger: bootstrap, timer, reconnect, push handler, Matrix event, room
   command, backfill, media, keepalive, retry and pacing.
2. Each request and trigger was compared with the native trigger evidence.
3. **Excluded:** items already planned in PLAN.md "Native metadata triggers
   (2026-10-14)" and being implemented elsewhere: the minute profile tick, the
   15-minute freshness windows, the startup CHATONROOM sweep, startup MEMLIST,
   unknown-author MEMBER, GETMCMETA revision gating and friend sync. The
   startup announcement refresh (`announcements.go:96`, GETMOMETA when the
   Boards cache is older than 15 minutes) falls under the planned GETMOMETA
   change and is not repeated here.

Classification:

- **M**: matches native, with evidence.
- **B**: bridge-only policy. Native has no such trigger.
- **O**: native behavior the bridge omits.
- **U**: untraced. No native evidence.

Risk:

- **acct**: account safety or request rate. The pattern could look abnormal to
  Kakao: extra polling, bursts, repeated logins, or request types or shapes
  native never sends.
- **corr**: correctness.
- **cos**: cosmetic.

## Findings, ranked

| # | Bridge action | Bridge trigger | Native trigger and evidence | Class | Risk | Recommended fix |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `SYNCMSG cnt=0`, sent per chat, up to 100 pages each (`client.go:322-348`, `client/sync.go:252`) | Every connect and **every reconnect**: one request per chat whose committed position is behind the inventory | After the last LCHATLIST page the client sends bulk `MCHATLOGS` for rooms with `lastMChatLogId < lastLogId`, at most 100 rooms per request ([HISTORY-STATIC](HISTORY-STATIC.md) §A, HST-STATIC-001/002). Single-room `SYNCMSG` runs only from an open chat window, mark-as-read or exact-message retrieval (§C). The bridge's MCHATLOGS path exists, but only behind opt-in `startup_history`, and it runs *in addition to* the per-room catch-up | B | **acct**: N requests where native sends ⌈N/100⌉, with a request type that native never sends at login | Make bulk MCHATLOGS the connect catch-up path, and drop per-chat SYNCMSG at connect. Needs an owned differential on gap and no-progress handling, because MCHATLOGS signals eof per batch, not per room |
| 2 | Full relogin (GETCONF, CHECKIN, LOGINLIST, LCHATLIST, catch-up and the startup sweeps) | `handleEvent` returning false ends the session (`lifecycle.go:427-433`). This happens when: a NEWMEM/DELMEM membership refresh fails (`group_membership.go:140-157`); a transient inbound media download fails (`convert.go:26-32`); `GETMSGS` fails for a SYNCMODMSG (`edits.go:87-103`). Replay through catch-up repeats the same event, so a persistent failure loops on the reconnect schedule (5 s up to 15 min, forever) | No traced native path turns a failed metadata read or media download into logout or recovery. Recovery starts only on the carriage status transition 25→26 ([GROUP-FAILURE-RECOVERY](GROUP-FAILURE-RECOVERY.md)). A failed DELMEM/NEWMEM consumer runs database work only ([membership inventory](../membership-chat-change-inventory.md)) | B | **acct**: repeated full logins, the same class as issue #372 | Separate a delivery or dependency failure from the source session. Hold the event and retry delivery in-process, behind a cap, and keep relogin for transport failures only. Add a regression for each of the three triggers |
| 3 | A second LOGIN from a detached profile client (`group_reconcile.go:100-115`, `lifecycle.go:812`) | `!reconcile-group` / `!complete-group-invitations` when the main session is down | Native never runs a second login next to recovery. Bridge policy waits out `-328` and blocks reconnect after KICKOUT ([reconnect policy](../bridge-reconnect-design.md)) | B | **acct**: `openDetached` checks only `stopping`, `connecting` and `cleanup`. It does not check the pending retry time, the `-328` backoff or the KICKOUT record, so an operator command can log in during a rate-limit wait or after a kick | Refuse when there is no live session, and tell the operator to reconnect. Otherwise route through the supervisor's `Connect` gates. Add a regression for the post-KICKOUT and pending-retry cases |
| 4 | HTTP `GET …/bubble/reactions/{log}/members`, plus `POST /emoticon/chat/rx/log-details` for Mini reactions (`reactions.go:318-393`) | **Every** `CHGLOGMETA` reaction push in every bridged room. Also once per changed row in viewing resync | Native stores only the aggregate and the viewer's own pick. It fetches members and Mini details on demand from the detail view ([GROUP-REACTIONS](GROUP-REACTIONS.md) "Per-member state"). A push merges the aggregate. After an own reaction, native refetches through sync-meta, not `/members` | B | **acct**: one authenticated HTTP read per reaction anywhere in the account, of a request type native sends only from a detail view | Record this as a deliberate deviation with an account budget, because Matrix needs per-actor reactions. Or derive the actor where the push allows it: the own pick from the aggregate, and a single-actor delta when the count changes by one. Coalesce bursts per message |
| 5 | HTTP `GET …/members` before the cancel `POST` (`reactions.go:244-254`) | Matrix reaction redaction | Native cancels from the locally stored own pick ([GROUP-REACTIONS](GROUP-REACTIONS.md)). No pre-read is traced | B | acct (low volume) | Use the stored own pick, or the bridged reaction record, and send only the cancel |
| 6 | GETCONF and CHECKIN on every reconnect. CHECKIN tries every host × port with no delay (`client/session.go:271,295`, `login_sync.go:29-59`) | Every `Connect` | Native books only when no live or cached route exists. A cached host and port is tried first, cleared on its own failure, and expires by uptime. Booking makes at most 3 attempts with jitter capped at 8 s. Ticket attempts walk the address pool with jitter capped at 32 s ([session-login PROTOCOL](../session-login/PROTOCOL.md#routing-cache-and-retry), [protocol-bootstrap](../protocol-bootstrap.md)) | B | **acct**: two extra booking requests per reconnect, and a CHECKIN burst during an outage | Cache the carriage route with native expiry and invalidation. Add native-style jitter between ticket attempts. The cache-expiry interval needs tracing first |
| 7 | Reconnect schedule: 5/10/20/40/60 s, then 15 min forever; the `-328` ladder; a 5-min stable reset (`lifecycle.go:22-37`) | Session end | `recoverLocoLogin:` reschedules itself after a failed login and skips the attempt while the network is unreachable or auth is unusable. The delay argument and any cap are **untraced** ([GROUP-FAILURE-RECOVERY](GROUP-FAILURE-RECOVERY.md); [reconnect.md](../reconnect.md) static item 2) | U | acct | Trace the floating-point delay at the reschedule block and the `bgReconnItv` consumer, then adopt them. Until then, document the current values as policy (already done in the reconnect design) |
| 8 | `SYNCMSG [wm-1, wm] cnt=1`, with no pacing and no coalescing (`client/read_state.go:14-32`, `read_receipts.go:127-163`) | Every Matrix read receipt that advances the watermark. An active Matrix client sends one per incoming message | Explicit mark-as-read goes through the single-room coordinator: `cur=lastSyncLogId`, `max` and `cnt` from the locally held messages (cap 300), and it is skipped when already synced ([HISTORY-STATIC](HISTORY-STATIC.md) §C; [message-continuity](../message-continuity.md) "cnt declares held messages"). A live message in an open, focused window gets `NOTIREAD`, not `SYNCMSG` ([read-state-notiread](../read-state-notiread.md)) | B | **acct**: a one-message window shape native never builds, sent at message rate | Debounce receipts per chat (for example, the latest within a few seconds). Trace whether the native-shaped request (`cur` = last synced position, `cnt` = held count) is safe for the bridge's store, and switch with an owned differential. Consider `NOTIREAD` for live messages while a viewing episode is active |
| 9 | Room avatar CDN download (`metadata_refresh.go:399-407, 457-465`; `avatar.go:16-35`) | Every `refreshRoomInfo` and `refreshBoards` task. `Reupload` downloads before comparing the hash, and avatars are never cached (`immutable=false`) | Native room images come from login GETMETA, gated on revision, and are displayed from a local image cache. Download triggers are untraced, but no native timer exists ([METADATA-TRIGGERS](METADATA-TRIGGERS.md)) | B | **acct**: repeated CDN reads of an unchanged image. CDN, not LOCO | Skip the download when the avatar ID (the URL hash) equals the portal's stored avatar ID. These files belong to the metadata-trigger session, so land this after it |
| 10 | `CHATINFO` from `refreshRoomInfo` (`metadata_cache.go:117-118`) | **Every** `GetChatInfo` served from the cache, with no freshness gate. This includes CHGCHATST/CHGMETA resyncs and framework resyncs | Native `CHATINFO`: NEWMEM, SYNCJOIN, MSG or HINT for an unknown room, notification click, deep links, VoIP and scheduled-message UI ([METADATA-TRIGGERS](METADATA-TRIGGERS.md)). It is not sent on a cached room read | B | acct | Enqueue room info only on a native trigger: CHGCHATST/CHGMETA, which the bridge treats as resync-worthy, or an unknown room. Coordinate with the metadata-trigger session |
| 11 | `CHATINFO` + `MEMLIST` (+ cached `MEMBER`) (`group_membership.go:140-158`, `discovery.go:55-76`) | Every NEWMEM and DELMEM in a managed group | NEWMEM: `CHATINFO` only when needed, then `MEMBER` for invitees, with the feed's invitees applied directly. DELMEM: removes the leaver from the stored roster, with no request ([membership inventory](../membership-chat-change-inventory.md), [chat-metadata](../chat-metadata.md)). No MEMLIST outside member-list views | B | acct, corr (failures trigger finding 2) | **Resolved 2026-10-14.** Feeds apply to the stored verified roster. NEWMEM: `MEMBER` for every invitee (native does not skip known ones). DELMEM: no request. A full roster read remains for groups without a verified roster and for self invites ([GROUP-MEMBERSHIP](GROUP-MEMBERSHIP.md) "Feed application") |
| 12 | `CHATINFO` + `MEMLIST` before `ADDMEM`, then an async roster refresh (`group_matrix_invite.go:36-111`) | A Matrix invite of a Kakao ghost | `ADDMEM` from the invitation picker. On success the client processes the returned chat log and refreshes only the selected members ([MATRIX-GROUP-CREATION](MATRIX-GROUP-CREATION.md) "Invitation chain") | B | acct (low volume) | **Resolved 2026-10-15.** A verified group is checked against its stored roster and chat data; the confirmed reply's invitation log is applied to the roster (the inviting session receives no NEWMEM for its own invite), then `MEMBER` for the invitee ([GROUP-SETTINGS-PERMISSIONS](GROUP-SETTINGS-PERMISSIONS.md)) |
| 13 | `CHATINFO` + `MEMLIST` + `MEMBER` after `CREATE` (`group_create.go:315-382`) | Matrix group creation, and the startup create-resume sweep | `CREATE` success upserts the room from the response's `chatRoom` and opens it ([MATRIX-GROUP-CREATION](MATRIX-GROUP-CREATION.md)). Opening means `CHATONROOM` | B | acct (low volume) | **Resolved 2026-10-15.** Binding uses the reply's `chatRoom` (the full-data room model: active member IDs, member profiles, metas) with no further read; resume and reconcile keep `CHATINFO`+`MEMLIST` ([MATRIX-GROUP-CREATION](MATRIX-GROUP-CREATION.md) "Connector behavior") |
| 14 | `CHATINFO` before `CHATONROOM` in each viewing episode (`reaction_resync.go:128`) | Matrix viewing heartbeat and `!sync-reactions` | Chat open sends `CHATONROOM`. The room type is already local from login chat data ([METADATA-TRIGGERS](METADATA-TRIGGERS.md) CHATONROOM row) | B | acct (low) | Take the room type from the LCHATLIST inventory or the stored portal, and send only `CHATONROOM` |
| 15 | Retries with no attempt limit: failed metadata tasks back off from 2 min to 1 h (`metadata_refresh.go:36, 54-60`); the paused-group membership retry likewise (`:467-518`) | Task failure | The native coordinator failure clears in-flight state and does not retry. The next native trigger refetches ([REACTION-SYNC-SCHEDULING](REACTION-SYNC-SCHEDULING.md) failure callback; [METADATA-TRIGGERS](METADATA-TRIGGERS.md)) | B | acct (low; spaced) | After the trigger rework, retry only on the next native trigger, plus a capped number of backoff attempts for membership the bridge needs to function. PLAN already keeps the 2-min reservation |
| 16 | `SYNCMSG cnt=0` for older history (`history_pages.go:154-173`); `!history-group` also re-reads membership and `CHATINFO` on every page (`group_history.go:145-184`) | Framework backward backfill with `AllowSlowFetch`; an operator command | Native older scrolling is local-only, at 50 messages per read. No remote reverse page exists ([HISTORY-STATIC](HISTORY-STATIC.md) §C) | B | acct (operator-gated, paced) | Keep it opt-in and documented as a deviation. Read membership once per command, not once per page |
| 17 | 2-second account pacing gate on all read-only LOCO and HTTP dependencies, **including live inbound media** and `CHATONROOM` (`client/history_admission.go:29-46`; `convert.go:375`) | Every paced read | No native pacing gate is traced. Native sends these immediately | B | corr (latency); acct-positive | Keep it: it lowers burst risk. Consider exempting live inbound media, or giving it its own lane, so a photo burst does not queue behind metadata reads. Do not cite it as Kakao parity |
| 18 | `GETMETA` is never sent | — | Login completion sends GETMETA for rooms whose chat data `mmr` exceeds the stored revision ([METADATA-TRIGGERS](METADATA-TRIGGERS.md)) | O | corr: offline room name or image changes reach the bridge only through CHATINFO side paths | Trace the GETMETA request, response and consumer chain. Then replace the CHATINFO room-info refreshes in findings 10 and 13 with a revision-gated GETMETA at login |
| 19 | Matrix leave of the bridge user is not bridged (`group_permissions.go:63-65`) | Matrix self-leave | Native has a leave action (request untraced) | O/U | corr | Trace the native leave request first. Until then, keep it unbridged and explicit |
| 20 | Typing is not sent | Matrix typing | Native receives `SYNCACTION` as typing ([METADATA-TRIGGERS](METADATA-TRIGGERS.md)). Outbound typing is untraced | O/U | cos | Trace outbound typing before adding it. It adds traffic, so it is optional |
| 21 | Inbound media and member-avatar downloads happen immediately on bridging | Each inbound media `MSG`; ghost avatar changes | Native thumbnail and download triggers are untraced (view-triggered download is noted as open in [HISTORY-STATIC](HISTORY-STATIC.md) §D) | U | acct (CDN) | Unavoidable for a bridge, which must re-upload to Matrix. Record it as a deliberate deviation. Trace native auto-download to know how far the bridge differs |

## Matches (no change)

| Bridge action | Evidence |
| --- | --- |
| LOGINLIST at connect and recovery; LCHATLIST pages until the end (cap 20) | [METADATA-TRIGGERS](METADATA-TRIGGERS.md) |
| `PING` idle timer: interval from `wifi.fgPingItv` (180 s observed, 180 s fallback below 1), cancelled by each request and re-armed after each completion; a failed PING closes the session | [PING-SCHEDULER](../reconnect/PING-SCHEDULER.md), RC-LIVE-001 in [reconnect EVIDENCE](../reconnect/EVIDENCE.md). `bgPingItv` (600) is unused; whether the Mac uses it while idle is untraced |
| Receive-header timeout from `recvHeaderTimeout` closes the carriage | [reconnect design](../bridge-reconnect-design.md) |
| `MCHATLOGS` request shape, 100-room batches, eof handling (when enabled) | [HISTORY-STARTUP-IMPLEMENTATION](HISTORY-STARTUP-IMPLEMENTATION.md) |
| KICKOUT is terminal; CHANGESVR logs out and books again | [manager terminal lifecycle](../manager-terminal-lifecycle.md) |
| Single-attempt WRITE, SHIP/MSHIP, POST, MODIFYMSG, DELETEMSG, CREATE, ADDMEM, reaction POST; no outbound retry | [protocol-parity](../protocol-parity.md) text/photo rows; [MATRIX-GROUP-CREATION](MATRIX-GROUP-CREATION.md) |
| `NOTIREAD` not sent | Matches native with no chat window open ([read-receipt policy](read-receipt-policy.md)) |
| QR poll: 3 s first delay, then server-directed `nextRequestIntervalInSeconds` | [device-registration PROTOCOL](../device-registration/PROTOCOL.md) |
| Reaction sync-meta only on a viewing episode, with a 10 s first tick, `dlm` respected and the PlusChat/Memo/Partner exclusion | [REACTION-SYNC-SCHEDULING](REACTION-SYNC-SCHEDULING.md). The single-lane 10-page budget is a deliberate difference |
| SHIP redirect → Kage prepare → chunk upload → WRITEMEDIA | [SHIP-REDIRECT](SHIP-REDIRECT.md) |

## Evidence gaps that block fixes

- Native recovery delay and cap (finding 7). This is a floating-point argument
  at the reschedule block, so it needs instruction-level ABI reading.
- Carriage route cache expiry interval and its owner (finding 6).
- The GETMETA chain (finding 18).
- Native outbound typing, leave and media auto-download triggers (findings 19–21).
- Whether a native-shaped mark-read `SYNCMSG` is safe for the bridge's store
  (finding 8). This needs an owned differential.
- MCHATLOGS gap semantics when used as the only catch-up (finding 1). This
  needs an owned offline-message differential.
- No idle official Mac capture exists to confirm the overall background request
  mix. See [METADATA-TRIGGERS](METADATA-TRIGGERS.md) "Gaps".

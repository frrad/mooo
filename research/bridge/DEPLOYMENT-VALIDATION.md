# Deployment validation

## Standard Matrix: startup and persistence smoke

Observed on 2026-10-04 against an operator-owned disposable Synapse instance,
using the proposed PR #170 Docker image built from revision `09a9d46`.
The image contains the merged QR and photo implementation. All generated
configuration, registration, database, and logs remained outside the repository.
No Kakao credentials or profile were copied into this installation.

- Linux arm64 image built successfully with Go 1.27.1, cgo SQLite, and `goolm`.
- Runtime help and private configuration/registration generation succeeded.
  The default image UID/GID is 10001; generated sensitive files were mode 0600
  and profile storage was mode 0700.
- A bind-mounted fresh installation ran with the host directory owner's numeric
  UID/GID, as supported by the deployment instructions. Startup created a
  persistent SQLite database and connected to the disposable homeserver.
- An authenticated `POST /_matrix/app/v1/ping` returned HTTP 200 before and
  after a clean container stop/start with the same data directory.
- The smoke container was stopped and removed afterward; private persistent
  data remains in the lab for subsequent acceptance work.

This proves container startup, appservice reachability, and basic restart with
persistent storage. It does not establish Kakao enrollment, encrypted room
messaging, long-running recovery, or the complete standard-Matrix acceptance
criteria. The prior native-binary text/catch-up experiment is recorded separately
in [the bridge plan](PLAN.md).

## Crypto startup and persistence smoke

On 2026-10-05 (UTC), source revision
`3c50c5415657063c59141448a4cea270266f1a33` was built with the pure-Go
`goolm` backend and started against the same stopped, operator-owned bridge
database and profile used by the preceding Docker resume. An external config
enabled end-to-bridge crypto while leaving default room encryption disabled.
The local Synapse/appservice ping returned HTTP 200; `CryptoHelper` upgraded or
reopened its crypto schema, reused the existing bot device, loaded the goolm
Olm account, and the bridge reached `CONNECTED`.

The database retained 1 login, 1 portal, and 8 messages, plus 13 persisted
`crypto_*` tables. SIGINT produced a clean `CryptoHelper.Stop` and bridge
shutdown. No room state, encrypted-room event, message, media, reaction, QR,
or phone UI action was performed. This validates crypto initialization and
persistence only; it does not establish Matrix room E2EE or encrypted
Kakao-to-Matrix messaging.

## Existing mapped portal: outbound encrypted text

On 2026-10-05 (UTC), bridge source revision
`1bf6c2dec6b96f61b4b56b8b61218142fa86bda7` was built with the pure-Go
`goolm` backend and resumed against the same operator-owned profile and
persistent bridge database used by the preceding validation. The existing
mapped direct portal was configured for `m.megolm.v1.aes-sha2` by the
configured appservice bot. A bounded companion client synchronized the mapped
room, encrypted one fresh text fixture, and sent one event. The homeserver
returned an encrypted event; the bridge decrypted it successfully and
persisted the corresponding outbound message with source type `1`, positive
chat/log metadata, and the database message count advancing from 8 to 9. The
one encrypted Matrix-to-Kakao text write was acknowledged with positive source
IDs and persisted under the same Matrix event ID.

The bridge and companion both stopped cleanly. A read-only check of the owned
B Android AVD showed Kakao `MainActivity`, the owned profile on `Friends`, the
existing self-chat in `ChatRoomHolderActivity`, and the exact fixture text in
that chat. This validates one existing-portal encrypted text path through
Megolm decryption, bridge persistence, Kakao write acknowledgement, and
phone-side display. It does not claim inbound encrypted Kakao-to-Matrix text,
encrypted media, replies, reactions, groups, or restart/recovery acceptance.

## Fresh QR enrollment: failed acceptance attempt

On 2026-10-04, a fresh container installation with an empty profile directory
received the bridge-native QR through its Matrix management room. The owned
Android 26.8.2 client imported that image through its QR scanner's album route
and displayed “You cannot use this QR code.” No device approval or credential
persistence followed. The bridge container was stopped and transient QR and
screen captures were removed from the lab devices.

This does not establish a rendering defect. Previous successful clean-room
registration and the official macOS presentation path both preserve the
server-issued payload unchanged. A second fresh-profile attempt also failed. Offline decoding recovered the
exact server-issued payload. Android source tracing identifies the observed
scanner modal as the QR-info endpoint's `GENERAL_NOT_FOUND` branch: the scan
reached lookup, but the server did not resolve the challenge. The underlying
generation, check-key, or account-policy cause remains unresolved. Those earlier failures remain unexplained. A subsequent 2026-10-07 fresh
bridge-native enrollment on owned B passed with the default renderer and
then resumed after restart; see [the enrollment record](QR-ENROLLMENT.md).
Owned A reached approval but reported an explicit secondary-device restriction.

## Restart, crash-loop and outage recovery

Observed on 2026-10-11 and 2026-10-13 with the owned disposable B profile and
the disposable Synapse. Native runs used a binary from revision `8844d27`
(PRs #326 and #328); the Docker runs used images built from `878d4cb` (#330)
and `71f23e5` (#331). Configuration, database, profile, logs and receipts
stayed in the private lab. Every start, stop, kill and firewall change was
recorded before it ran.

The reconnect schedule in the crash-loop, outage and restart runs below was
replaced on 2026-10-15 by the native recovery episode
([reconnect policy](../bridge-reconnect-design.md)); those runs record the
earlier behavior.

- **Crash loop (native).** The bridge was killed with SIGKILL about 2.7 s
  after each login began and restarted at once. The first restart, 43 s after
  a stable login, connected at once. The next waited until the previous login
  plus 10.0 s, and the one after until the previous login plus 20.0 s, each
  reporting `kakao-reconnect-scheduled` first. The last run then connected.
  The state message is mautrix's human-readable text for the code, not the
  bridge's countdown.
- **Operator reconnect (native).** With the profile lease held by another
  process, the bridge reported `kakao-profile-unavailable` and did not retry
  for 90 s. After the lease was released, `!kakao reconnect` replied
  "Reconnecting to KakaoTalk", and the login reached `CONNECTED` 18 s later
  after catch-up. A second `reconnect` replied "already connected" and opened
  no session.
- **Docker deployment.** The image ran as the host directory owner's UID with
  the private data directory bind-mounted, the homeserver reached it through a
  published loopback port, and the login connected and caught up.
- **Network outage (Docker).** A sidecar owning the container's network
  namespace dropped all traffic except DNS, Matrix and the appservice port.
  On the `878d4cb` image the minute-by-minute group refresh timed out and
  reported `kakao-group-membership-pending`, and the bridge still reported
  itself connected four minutes into the outage: each refresh request re-armed
  the keepalive ping, so it never fired. PR #331 fixed this. On the `71f23e5`
  image the refresh failed on the connection 64 s into the outage and the
  bridge reported `kakao-disconnected`. Recovery then retried after 5, 10, 20,
  40 and 60 s, each attempt failing on a 10 s GETCONF dial timeout, and
  reported `kakao-recovery-stalled` once with the next retry 15 minutes away.
- **Restart during backoff (Docker).** `docker restart` 13 s after the stalled
  state started a process that reported `kakao-reconnect-scheduled` and did
  not log in. The saved schedule held attempt 6 and the retry time from the
  previous process. The firewall was then opened. The waiting process
  connected at the saved retry time to the second, caught up, and reported
  `CONNECTED` 13 s later.
- **Receive-header timeout (Docker, 2026-10-13).** On an image built from
  `761f2e4` (PR #333), Kakao traffic was dropped just after a group refresh,
  and the tester sent one synthetic text into the selected portal. The bridge
  wrote WRITE into the dead carriage. Exactly 20.0 s later the session closed
  with "no reply within the receive-header timeout", the Matrix send failed,
  and the bridge reported `kakao-disconnected`. After the firewall opened, the
  first 5 s retry reconnected and caught up. The text was not resent.

Not live-tested: a real `KICKOUT` (requires revoking and re-enrolling the lab
device), a server-sent `CHANGESVR`, and an owner cleanup that times out. Each
is covered by synthetic regression tests. A laptop host does not run recovery
timers while asleep; the 2026-10-11 run recovered up to about two hours after
disconnects that happened during sleep.

## Beeper: independent gate

Beeper validation has not run. The current official
[bridge-manager instructions](https://github.com/beeper/bridge-manager#3rd-party-bridgev2-based-bridges)
provide a third-party bridgev2 route: authenticate with `bbctl`, generate a config
with `bbctl config --type bridgev2 <name>`, add the connector's `network` section,
and run the bridge. Appservice websockets are provided by bridgev2.

The documentation explicitly identifies incomplete Matrix API support as a
potential compatibility limit. Standard Synapse success therefore does not prove
Beeper compatibility. `bbctl` v0.15.0 has since been downloaded from its
official release and its release checksum verified. There is no local login configuration; an
operator-selected Beeper account is needed for the live gate. No service
credentials were requested or written into tracked files.

Before claiming Beeper support, validate configuration generation, websocket
connectivity, bot interaction, encrypted direct/group messaging, media transfer,
restart, and offline recovery. Record the manager/bridge versions, supported
configuration, and actual failures separately from standard-Matrix evidence.

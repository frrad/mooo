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
generation, check-key, or account-policy cause remains unresolved. Fresh enrollment acceptance requires identifying the cause, adding a
regression, and completing a controlled retry followed by restart/resume.

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

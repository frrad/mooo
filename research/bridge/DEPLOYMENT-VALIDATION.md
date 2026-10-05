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

## Beeper: independent gate

Beeper validation has not run. The current official
[bridge-manager instructions](https://github.com/beeper/bridge-manager#3rd-party-bridgev2-based-bridges)
provide a third-party bridgev2 route: authenticate with `bbctl`, generate a config
with `bbctl config --type bridgev2 <name>`, add the connector's `network` section,
and run the bridge. Appservice websockets are provided by bridgev2.

The documentation explicitly identifies incomplete Matrix API support as a
potential compatibility limit. Standard Synapse success therefore does not prove
Beeper compatibility. At inspection time there was no local `bbctl` executable
or login configuration; an operator-selected Beeper account is needed for the
live gate. No service credentials were requested or written into tracked files.

Before claiming Beeper support, validate configuration generation, websocket
connectivity, bot interaction, encrypted direct/group messaging, media transfer,
restart, and offline recovery. Record the manager/bridge versions, supported
configuration, and actual failures separately from standard-Matrix evidence.

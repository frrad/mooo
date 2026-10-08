# Bridge development harness

How to run `mooo-bridge` against a disposable local homeserver. Keep every
generated file (homeserver data, bridge config, registration, database, logs)
outside the repository, for example under a private lab directory.

## Build

The bridge uses mautrix-go's pure-Go Olm implementation, selected with the
`goolm` build tag. The tag is set for CI through `GOFLAGS`; set it locally too:

```bash
export GOFLAGS=-tags=goolm
go build -o "$LAB/bridge/mooo-bridge" ./cmd/mooo-bridge
```

`cgo` must be enabled because the SQLite driver uses it.

## Homeserver

Any homeserver that supports application services works. A throwaway Synapse
container is the simplest:

```bash
docker run --rm -v "$LAB/synapse:/data" \
  -e SYNAPSE_SERVER_NAME=mooo.localhost -e SYNAPSE_REPORT_STATS=no \
  matrixdotorg/synapse:latest generate
```

Add the bridge registration (generated below) to `homeserver.yaml` under
`app_service_config_files`, mount it into the container, then run Synapse with
port 8008 published. Register one local test user for yourself.

## Bridge configuration

```bash
cd "$LAB/bridge"
./mooo-bridge -e -c config.yaml
```

Edit `config.yaml`:

- `homeserver.address`: `http://localhost:8008`; `homeserver.domain`:
  `mooo.localhost`.
- `appservice.address`: an address the homeserver can reach, such as
  `http://host.docker.internal:29340` when Synapse runs in Docker Desktop.
- `database.type`: `sqlite3-fk-wal`; `database.uri`:
  `file:mooo-bridge.db?_txlock=immediate`.
- `bridge.permissions`: grant your test user `admin`. Profile import is
  restricted to bridge admins.
- `network.profile_dir`: the private (mode 0700) directory that already holds
  the lab profile.

`bridge.portal_event_buffer` and `bridge.async_events` are forced to `0` and
`false` at startup. They must stay that way: the connector commits a Kakao
message to the continuity checkpoint only after the framework reports it
handled, and buffered or asynchronous delivery would report success early.

Generate the registration, install it in the homeserver, restart the
homeserver, and start the bridge:

```bash
./mooo-bridge -g -c config.yaml -r registration.yaml
./mooo-bridge -c config.yaml
```

## Logging in

Start a DM with the bridge bot and send `login` to select the QR enrollment
flow. It creates a fresh bridge-owned profile; scan the displayed QR code with
your authorized primary Kakao client and complete any displayed device approval.
Fresh enrollment and one restart passed on owned B on 2026-10-07;
see [the enrollment evidence](QR-ENROLLMENT.md). Use `login qr` to select
the QR flow directly. An explicit account-protection restriction is a stop
condition, not a reason to regenerate repeatedly.

Alternatively, send `login import-profile`, then enter the profile's name. `list-logins` shows the login's connection state.
Stopping the bridge with an interrupt shuts it down cleanly even though the
process exits with status 1. The name must be a single file name inside
`profile_dir`, never a path.

Point `profile_dir` at the directory where the profile already lives rather
than copying it. A copy would be a second owner of the same device identity
and credentials, with its own lease and continuity checkpoint.

While the bridge runs it holds the profile's lease, so research probes cannot
use that profile until the bridge stops.

## Current validation and limits

- Initial names and member profiles/rosters are source-backed; direct/group live
  metadata validation remains outstanding. Avatar and membership updates are in
  review.
- Text, replies, and supported JPEG/PNG photos can be sent from Matrix. Photos
  use bounded transfer and encrypted Matrix media validation. Unsupported message
  kinds become notices; image replies are rejected.
- Reactions and automatic reconnect are in review. A lost Kakao session currently
  requires a bridge restart for recovery.
- Messages sent while the bridge is stopped are caught up on restart only for
  previously committed chats. Other history is not backfilled. Catch-up may have
  server read side effects; the local watermark discrepancy needs an A/B test.
- Persistent conversion failures remain an open acceptance item. Failed Matrix
  handling never authorizes checkpoint advancement.
- See [the bridge plan](PLAN.md) for implementation versus live acceptance evidence.

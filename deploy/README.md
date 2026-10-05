# Container deployment

This image packages the current bridge. Full alpha acceptance and Beeper
validation remain tracked in [the bridge plan](../research/bridge/PLAN.md).
Build and configuration smoke tests do not establish Kakao enrollment or
long-running recovery validation.

## Build and configure

The build uses Go 1.27.1, the pure-Go Olm backend, and cgo SQLite. The runtime
runs as UID/GID 10001. Build from a clean source checkout:

```sh
docker build -t mooo-bridge:local .
cd deploy
mkdir -p data/profiles
chmod 700 data data/profiles
sudo chown -R 10001:10001 data
docker compose run --rm mooo-bridge -e -c /data/config.yaml
```

Alternatively, set `MOOO_UID` and `MOOO_GID` to the owner of the data directory
and keep those values consistent for every command and upgrade.

Edit the generated `data/config.yaml`:

- Set `homeserver.address` to a homeserver address reachable from the container
  and `homeserver.domain` to its Matrix domain.
- Set `appservice.address` to the bridge address reachable from the homeserver.
  The example publishes port 29340 on host loopback. If the homeserver is in
  another container, put both on an operator-managed Docker network and use
  the bridge service address; change the example network configuration as needed.
- Set `appservice.hostname` to `0.0.0.0` and `appservice.port` to `29340`.
- Set `database.type` to `sqlite3-fk-wal` and `database.uri` to
  `file:/data/mooo-bridge.db?_txlock=immediate`.
- Set `network.profile_dir` to `/data/profiles`.
- Grant your Matrix user `admin` in `bridge.permissions`; remove any wildcard
  grants if this installation is intended for one user.
- Keep database, logging output, and any other writable paths under `/data`.

Generate registration:

```sh
docker compose run --rm mooo-bridge -g -c /data/config.yaml -r /data/registration.yaml
```

Install `data/registration.yaml` in the homeserver's appservice configuration
and restart the homeserver. Keep config and registration files private: they
contain generated application-service credentials. Start the bridge:

```sh
docker compose up -d
docker compose logs --tail 50 mooo-bridge
```

Open a DM with the bot. Use the login flows documented in the current bridge
release. Import an existing authorized profile by placing its file and associated
continuity state in `/data/profiles` while every other owner is stopped; do not
run two copies of the same Kakao device identity.

## Persistence and upgrades

Persist the whole data directory, including the bridge database (and SQLite
WAL/SHM files), configuration, registration, profiles, locks, and continuity
checkpoints. Stop the bridge before copying or snapshotting it. Preserve owner
permissions and treat the copy as sensitive authentication material.

Before upgrading, stop the bridge, make an offline copy of `data`, and record
the current image identifier. Build the new image from the intended source
revision and start it with the same mounted data. Database migrations run during
startup; rollback may require restoring the offline database/profile snapshot
with the old image. Never start old and new bridge containers against the same
profile concurrently.

The compose example deliberately does not automatically restart the process:
current session recovery behavior is described in the bridge plan. Set a restart
policy only when it matches the validated release behavior.

# Container deployment

This image packages the current bridge. Full alpha acceptance and Beeper
validation remain tracked in [the bridge plan](../research/bridge/PLAN.md).
Build and configuration smoke tests do not establish Kakao enrollment.
Restart and outage recovery was validated on an owned test account; see
[deployment validation](../research/bridge/DEPLOYMENT-VALIDATION.md).

To run the bridge against a Beeper account instead of your own homeserver, use
[`beeper/`](beeper/README.md).

## Get the image

CI publishes a multi-arch (`linux/amd64`, `linux/arm64`) image to
`ghcr.io/frrad/mooo-bridge` on every merge to `main`, tagged `main`, the full
commit SHA, and `sha-<short>`. To use it instead of building locally, export
the image name before every compose command below:

```sh
export MOOO_IMAGE=ghcr.io/frrad/mooo-bridge:main
docker compose pull
```

Pin a commit tag instead of `main` when you want an exact, rollback-able
revision.

## Build and configure

To build locally instead, the build uses the Go toolchain pinned in the Dockerfile, the pure-Go Olm backend, and cgo SQLite. The runtime
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
- Optionally set `network.network_icon` to the `mxc://` URI of an image you
  uploaded to your homeserver. It becomes the bridge space and bot avatar and
  Beeper's sidebar icon for the account; Beeper's chat-list badge does not use
  it.
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
revision, or `docker compose pull` the published tag, and start it with the
same mounted data. Database migrations run during
startup; rollback may require restoring the offline database/profile snapshot
with the old image. Never start old and new bridge containers against the same
profile concurrently.

## Restarts and recovery

The compose example restarts the bridge unless you stopped it
(`restart: unless-stopped`). This is safe because the bridge, not the
supervisor, decides when to log in to KakaoTalk:

- Inside one process the bridge reconnects on its own, on the official Mac
  client's schedule. After a dropped connection it tries again at once, then
  every 10 seconds. If nothing succeeds within 10 minutes it reports
  `kakao-recovery-stalled` and keeps trying every 15 minutes. A device-limit
  refusal (`LOGINLIST -328`) instead waits 1, 2, 4, 8 and 15 minutes. A dead
  connection is usually noticed within one to three minutes, by the periodic
  group refresh or the keepalive ping.
- Sessions that end within a minute of logging in, more than three times in
  a row, reconnect only every 15 minutes, so a server that keeps closing the
  session cannot cause a login every few seconds.
- The reconnect schedule is saved with the login. A restarted process first
  waits out a pending retry. A restart within a minute of the last login, for
  example in a crash loop, counts as such a short session: after three of
  them the bridge waits 15 minutes instead of logging in on every restart.
  While waiting it reports `kakao-reconnect-scheduled`.
- A KICKOUT is saved and blocks every later connect, including after a
  restart, until you log out of the bridge and log in again.
- Some stops wait for you instead of retrying: a rejected login, an
  unidentifiable message, or a profile that another process holds. Fix the
  cause, then send `reconnect` to the bridge bot (or `!kakao reconnect` in
  any room the bot is in). The command never shortens a scheduled retry.

Any other supervisor works the same way. For a native binary on macOS, a
launchd agent with `KeepAlive` is equivalent; keep its working directory and
data outside any source checkout, and never run it at the same time as
another copy that uses the same profile.

On a laptop, recovery timers do not run while the machine sleeps, so the
bridge reconnects a while after waking rather than on schedule. Run it on an
always-on host if that matters.

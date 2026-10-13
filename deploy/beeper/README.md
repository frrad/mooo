# Beeper deployment

Runs the bridge against a Beeper account. Beeper validation has not run yet;
see [deployment validation](../../research/bridge/DEPLOYMENT-VALIDATION.md#beeper-independent-gate).

Beeper hosts the Matrix homeserver, so there is no homeserver to configure and
no registration file to install. The bridge connects out to Beeper over a
websocket and needs no inbound port. Registration and config generation use
Beeper's [bridge-manager](https://github.com/beeper/bridge-manager) (`bbctl`),
which `compose.yaml` builds and runs as the `bbctl` service.

## Set up

`compose.yaml` is self-contained. Copy it into an empty directory and create
the data directory beside it, owned by the container user (UID/GID 10001):

```sh
mkdir -p data && chmod 700 data
sudo chown 10001:10001 data
```

Or set `MOOO_UID` and `MOOO_GID` to the data directory's owner, and keep them
the same for every command.

The bridge image defaults to `ghcr.io/frrad/mooo-bridge:main`. To pin a
revision, put it in a `.env` file beside `compose.yaml`:

```sh
MOOO_IMAGE=ghcr.io/frrad/mooo-bridge:sha-<short>
```

Log in to Beeper. This prompts for your email address and a code, and stores
the login in `data/bbctl/config.json`; treat that file as a credential:

```sh
docker compose run --rm bbctl login
```

Start everything:

```sh
docker compose up -d
docker compose logs --tail 50 mooo-bridge
```

On the first `up`, the `bbctl` service registers the bridge with Beeper as
`sh-kakaotalk` (override with `MOOO_BRIDGE_NAME`; the name must start with
`sh-`) and writes `data/config.yaml`. The bridge starts only after that
succeeds. On later runs, the service sees the existing config and exits.

The generated config has no `network` section. The bridge fills it in with
defaults on first start. The database, logs and profiles live under `data/`,
because the container's working directory is `/data`.

The bridge bot then appears in Beeper. Open a chat with it and log in to
KakaoTalk.

Upgrades, backups and restart behavior are as described in the
[main deployment guide](../README.md#persistence-and-upgrades).

# Beeper deployment

Runs the bridge against a Beeper account. On 2026-10-13 one owned account
passed config generation, QR login, group room creation and inbound messages
through Beeper. The rest of the
[Beeper gate](../../research/bridge/DEPLOYMENT-VALIDATION.md#beeper-independent-gate)
has not run.

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

`bbctl` writes no `network` section, so the service adds one with
`profile_dir: /data/profiles`, and the bridge fills in the remaining defaults
on first start. The database and logs also land under `data/`, because the
container's working directory is `/data`.

KakaoTalk does not appear in Beeper's "Add an account" list. Start a chat
with the bridge bot, `@sh-kakaotalkbot:beeper.local` for the default name, and
send `login qr`. Scan the QR code within about a minute with the scanner in
KakaoTalk on your phone: open it from the search icon at the top right, or from
the More tab. Then confirm the code the bot shows. If the code expires, send
`login qr` again.

Group chats appear after login. A one-to-one chat appears when its first new
message arrives. Earlier history is not brought over.

To debug a "could not be displayed (malformed_payload: …)" notice, opt in to
raw dumps by setting `network.gap_dump_dir: /data/gap-dumps` in
`data/config.yaml` and restarting the bridge. Each such message is then also
written to `data/gap-dumps/` on the host. The dumps contain private message
content, so keep them out of Git and delete them when you're done.

Upgrades, backups and restart behavior are as described in the
[main deployment guide](../README.md#persistence-and-upgrades).

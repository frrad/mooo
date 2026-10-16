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

Before the first login, set `network.device_name` in `data/config.yaml` to
the name KakaoTalk's device list should show, for example
`Alex's MacBook Pro`, and restart the bridge. In Docker there is no macOS
Computer Name to fall back to, so leaving it empty registers a device with no
name.

KakaoTalk does not appear in Beeper's "Add an account" list. Start a chat
with the bridge bot, `@sh-kakaotalkbot:beeper.local` for the default name, and
send `login qr`. Scan the QR code within about a minute with the scanner in
KakaoTalk on your phone: open it from the search icon at the top right, or from
the More tab. Then confirm the code the bot shows. If the code expires, send
`login qr` again.

Group chats appear after login. A one-to-one chat appears when its first new
message arrives. Earlier history is not brought over.

To debug a "could not be displayed (malformed_payload: …)" notice, an
"unsupported type" or "cannot be displayed (type …)" notice, or a media send
that fails, opt in to raw dumps by setting
`network.debug_dump_dir: /data/debug-dumps` in `data/config.yaml` and
restarting the bridge. Each such message or reply is then also written to
`data/debug-dumps/` on the host. The dumps contain private message content and
upload tokens, so keep them out of Git and delete them when you're done.

Upgrades, backups and restart behavior are as described in the
[main deployment guide](../README.md#persistence-and-upgrades).

## Sidebar network icon

`network.network_icon` takes a Matrix media URI (`mxc://...`), not a local
filename or an ordinary web URL. With that setting, mooo publishes the image
as the protocol and network `avatar_url` in each portal's `m.bridge` event.
Beeper Desktop shows the network avatar as the account's sidebar icon. The
small badges beside individual chats come from Beeper's built-in brand table,
so this setting does not change them. The bridge space and management room
take the icon as their room avatar only when they are first created; existing
ones keep their avatar. The bot's profile picture is set separately, by
`appservice.bot.avatar`.

Use a PNG you are entitled to use, saved locally as `kakaotalk-icon.png`.
An operator with the official Mac client can extract its bundled application
icon for their own installation without putting the proprietary asset in Git:

```sh
sips -s format png /Applications/KakaoTalk.app/Contents/Resources/AppIcon.icns \
  --out kakaotalk-icon.png
sips -g pixelWidth kakaotalk-icon.png
```

A macOS app icon draws its rounded square over only the middle 824/1024 of
the canvas, about 80%, with a transparent margin and a drop shadow around it.
Uncropped, it looks smaller than the other accounts' sidebar icons, which fill
their frame. Crop it to the centred square: 206 pixels for a 256-pixel image,
412 for 512 or 824 for 1024. This also removes the shadow:

```sh
sips --cropToHeightWidth 206 206 kakaotalk-icon.png
```

Upload the plain PNG directly to the homeserver's
[Matrix media API](https://spec.matrix.org/latest/client-server-api/#post_matrixmediav3upload).
Sending it as an encrypted chat attachment can upload encrypted bytes, which
are unsuitable for an avatar. Beeper Desktop's temporary attachment upload ID
is also not a Matrix media URI.

The upload authenticates with the bridge's `appservice.as_token` and goes to
its `homeserver.address`. On Beeper that address has the form
`https://matrix.beeper.com/_hungryserv/<username>`. Both values are in
`data/config.yaml`, which belongs to the container user and is usually not
readable from the host, so read them through the running bridge container.
Run the following in **Bash** in the directory holding `compose.yaml` and the
image. The token goes to curl through stdin and is never printed, passed as a
command-line argument or kept in shell history:

```bash
config_value() {
  docker compose exec -T mooo-bridge cat /data/config.yaml |
    sed -n "/^$1:/,/^[a-z]/p" | awk -v k="$2:" '$1 == k { print $2; exit }' |
    tr -d '"'
}
icon_homeserver=$(config_value homeserver address)
icon_token=$(config_value appservice as_token)
printf 'Authorization: Bearer %s\n' "$icon_token" | \
  curl --fail-with-body --silent --show-error --proto '=https' \
    --header @- --header 'Content-Type: image/png' \
    --data-binary @kakaotalk-icon.png \
    "${icon_homeserver%/}/_matrix/media/v3/upload?filename=kakaotalk-icon.png"
unset icon_token icon_homeserver
```

A successful response contains `{"content_uri":"mxc://.../..."}`; on Beeper
the server name is `local.beeper.com`. If the request fails, inspect the
reported status before trying again; do not change the hostname or send the
token to another service.

The generated `data/config.yaml` already contains both settings, as
`network_icon: ""` under `network` and `resend_bridge_info: false` under
`bridge`. The container has no text editor, so set them with `sed`,
substituting the returned URI:

```sh
docker compose exec mooo-bridge sed -i \
  -e 's|^\( *network_icon:\).*|\1 "mxc://local.beeper.com/your-media-id"|' \
  -e 's|^\( *resend_bridge_info:\).*|\1 true|' /data/config.yaml
```

Restart with `docker compose restart mooo-bridge`. The bridge then
republishes the bridge metadata of existing portals and logs
`Resent bridge info to all portals`. Then set `bridge.resend_bridge_info`
back to `false`, for example with the second `-e` expression ending in
`false`, and restart again; keep `network.network_icon` set. Do not share the
config or its tokens when recording the result.

On 2026-10-13 this procedure ran against the account above: the upload
returned an `mxc://local.beeper.com/...` URI whose download matched the PNG
byte for byte, and after the resend every portal's `m.bridge` event carried
the URI as its protocol and network avatar. Beeper Desktop then showed the
icon in the sidebar, though the uncropped icon looked smaller than its
neighbours. The cropped icon was then uploaded and resent the same way; its
appearance in the sidebar has not yet been confirmed.

The sidebar names the account `KakaoTalk (sh-kakaotalk)`. Beeper Desktop
4.3.176 classes every bridge whose ID starts with `sh-` as self-hosted and
always appends the bridge ID in parentheses to such an account's name.
`bbctl` requires the `sh-` prefix, and the client offers no way to rename an
account, so the suffix cannot be removed from the bridge side.

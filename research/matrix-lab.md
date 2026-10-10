# Encrypted Matrix lab companion

`tools/matrix-lab` exercises the pinned mautrix-go `CryptoHelper` against an
operator-owned localhost homeserver and an existing encrypted portal. It uses
normal login, sync, encryption, decryption and media APIs; it does not rewrite
bridge mappings or implement another cryptographic layer.

Build with `GOFLAGS='-tags=goolm' go build -o mooo-matrix-lab ./tools/matrix-lab`. Run `--help`
for the private file inputs. Keep the password, pickle key, crypto SQLite DB,
device credentials, event fixtures and receipts outside the repository, inside
an owner-only directory. Password and pickle files must be owner-only. Supply
one stable `--device-id`, `--crypto-db`, `--pickle-key-file` and
`--credentials-file` across all invocations. Credentials are created exclusively
on first login and subsequently checked against `whoami`; a mismatch or invalid
existing file fails closed rather than creating another device.

Explicit `--connect` is required for network operations. `startup` initializes
the device; `sync` processes one complete sync response; `decrypt` checks an
exported encrypted event against private expected JSON containing `type`, `body`
and optionally `sha256` for an encrypted attachment. Optional `event_type` defaults
to `m.room.message`; set it to `m.sticker` and use an empty `type` for native
stickers. Decryption checks the event type as well as its content and media hash. `send-text` reads the exact
body bytes; `send-file` accepts a synthetic 64×64 PNG and encrypts both attachment
and room event. Both sends require a new absolute `--receipt-file`, reserved
before upload/send. An existing receipt always blocks another attempt, including
when a previous attempt's outcome is uncertain. Inspect remote state before
making a new deliberate attempt. No write is automatically replayed.

## Adding edge cases

When acceptance testing finds a failure, first add a regression exercising the
real helper or SDK path, fix it, and document the sanitized finding here. Keep
private runtime details in `.lab/STATE.md`. Do not weaken bridge trust settings to
make a test pass. A successful unencrypted portal test does not establish
end-to-end encrypted room behavior.

- Exported event JSON must call `Content.ParseRaw` before SDK decryption. JSON
  unmarshalling alone leaves `Content.Parsed` empty; the SDK rejects that input.
  The regression was checked against the missing-parse behavior and the fix.
- Logging in on every operation exhausts local Synapse login limits. Persist the
  access token bound to the stable crypto device; report the rate-limit delay
  without printing credentials or automatically retrying.
- Encrypted uploads use `application/octet-stream`; the decrypted message names
  the original image MIME type and has an encrypted `file`, without a plaintext
  `url`. Media decryption must verify the exact synthetic SHA-256.

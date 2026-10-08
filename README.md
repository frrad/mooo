# mooo

`mooo` is an open-source research project aimed at interoperating with KakaoTalk's
secondary-device protocol and, eventually, providing a self-hosted Matrix/Beeper
bridge.

The project is still pre-alpha, but its clean-room Go client now performs
client-owned QR enrollment, persistent secondary-device authentication, direct
text/photo messaging, replies, reactions, and durable cursor-based message
continuity. A minimal Matrix bridge supports profile import, bidirectional text,
inbound replies, and restart catch-up. Fresh bridge-native QR login and one restart have also passed on an owned
account. Broader media, room/member metadata, automatic reconnect, and deployment
acceptance remain in progress;
see the [bridge plan](research/bridge/PLAN.md).

## Principles

- Interoperability with accounts and devices the operator is authorized to use.
- No credentials, device identifiers, private messages, packet captures, or
  account-specific material in Git.
- Reproducible observations, with version and provenance recorded.
- A modular Go implementation that can later support a Matrix application service.
- Public documentation describes behavior without publishing reusable secrets.

## Repository layout

- `cmd/mooo-lab`: non-invasive research CLI and future protocol exerciser.
- `internal/client`: long-lived authenticated client, messaging, events, and
  durable continuity orchestration.
- `internal/continuity`: private versioned resume checkpoint and commit boundary.
- `internal/protocol`: transport-independent wire models and codecs.
- `internal/buildinfo`: build metadata used to verify the Go toolchain and CI.
- `research`: sourced notes, experiment records, and sanitized findings.
- `docs/adr`: architecture decision records.

## Development

Requires Go 1.27 or later and Make. The default development and CI backend is
mautrix-go's pure-Go Olm implementation (`goolm`), which does not require the C
libolm headers or library. The Make targets set the build tag automatically:

```sh
make test # race-enabled tests; also the default for plain make
make vet
make build
```

`make check` also checks formatting, lint, and vulnerabilities; install
`golangci-lint` and `govulncheck` first. Secret scanning remains a separate
required pre-merge check, as in CI.

For direct Go commands, set the tag once in the current shell while working on
this repository (this also covers `go run` commands below):

```sh
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-tags=goolm"
go test ./...
go vet ./...
```

Go does not read repository-local default build tags from `go.mod`. Avoid
`go env -w GOFLAGS=...` for this setup because it changes the user-wide Go
defaults for other projects. Untagged commands select the dependency's C Olm
backend and require an installed libolm development package.

The lab CLI can create and inspect an offline, client-owned secondary-device
identity. It never discovers or imports an official KakaoTalk profile:

```sh
go run ./cmd/mooo-lab auth init \
  --state /absolute/private/path/authstate.json \
  --device-name "Mooo Lab Mac" \
  --app-version 26.8.0 \
  --os-version "macOS 26" \
  --model MacBookAir

go run ./cmd/mooo-lab auth inspect \
  --state /absolute/private/path/authstate.json
```

The state path must be absolute. Its directory and file are restricted to the
current user, and inspection output reports presence only; identity and credential
values stay redacted. These commands do not make network requests.

Network-capable APIs remain internal while their lifecycle and compatibility
contracts are being validated. A profile opened by the client receives a sibling
`.continuity` checkpoint under the same owner-only directory and profile lease.

See [PLAN.md](PLAN.md) for the current research sequence.

## Status

Pre-alpha research. Do not use this project with an account you cannot afford to
lose, and do not assume protocol behavior is stable.

## License

MIT. See [LICENSE](LICENSE).

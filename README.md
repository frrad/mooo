# mooo

`mooo` is an open-source research project aimed at interoperating with KakaoTalk's
secondary-device protocol and, eventually, providing a self-hosted Matrix/Beeper
bridge.

The project is intentionally starting with protocol research. It does not yet
authenticate with KakaoTalk or bridge messages.

## Principles

- Interoperability with accounts and devices the operator is authorized to use.
- No credentials, device identifiers, private messages, packet captures, or
  account-specific material in Git.
- Reproducible observations, with version and provenance recorded.
- A modular Go implementation that can later support a Matrix application service.
- Public documentation describes behavior without publishing reusable secrets.

## Repository layout

- `cmd/mooo-lab`: non-invasive research CLI and future protocol exerciser.
- `internal/buildinfo`: build metadata used to verify the Go toolchain and CI.
- `research`: sourced notes, experiment records, and sanitized findings.
- `docs/adr`: architecture decision records.

## Development

Requires Go 1.27 or later.

```sh
go test ./...
go vet ./...
```

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

See [PLAN.md](PLAN.md) for the current research sequence.

## Status

Pre-alpha research. Do not use this project with an account you cannot afford to
lose, and do not assume protocol behavior is stable.

## License

MIT. See [LICENSE](LICENSE).

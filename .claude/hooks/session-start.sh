#!/bin/bash
# Prepares Claude Code on the web sessions to run the same checks as CI:
# go test, go vet, gofmt, golangci-lint, govulncheck, and gitleaks.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

cd "$CLAUDE_PROJECT_DIR"

# go.mod pins a newer Go than the base image; GOTOOLCHAIN=auto fetches it.
go version
go mod download

gobin="$(go env GOPATH)/bin"
if [ -n "$(go env GOBIN)" ]; then
  gobin="$(go env GOBIN)"
fi
export PATH="$gobin:$PATH"

# Tools must be built with the go.mod toolchain: golangci-lint refuses to lint
# a module targeting a newer Go than the one it was built with. `go install
# pkg@latest` ignores this go.mod, so pin GOTOOLCHAIN explicitly. Reinstall when
# the cached binary was built by a different toolchain.
want="$(go env GOVERSION)"
install_tool() {
  local name="$1" pkg="$2"
  if [ -x "$gobin/$name" ] && go version "$gobin/$name" 2>/dev/null | grep -q ": $want\$"; then
    return
  fi
  echo "installing $name"
  GOTOOLCHAIN="$want" go install "$pkg@latest"
}

install_tool golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint
install_tool govulncheck golang.org/x/vuln/cmd/govulncheck
install_tool gitleaks github.com/zricethezav/gitleaks/v8

# Put GOPATH/bin ahead of the image's older /usr/local/bin/golangci-lint.
if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
  echo "export PATH=\"$gobin:\$PATH\"" >> "$CLAUDE_ENV_FILE"
fi

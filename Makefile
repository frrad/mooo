# Match CI: use mautrix-go's pure-Go Olm backend without requiring libolm.
# Keep caller-supplied GOFLAGS (for example, -mod=readonly).
override GOFLAGS += -tags=goolm
export GOFLAGS

.DEFAULT_GOAL := test
.PHONY: test vet build lint vuln format-check check

test:
	go test -race ./...

vet:
	go vet ./...

build:
	go build ./...

lint:
	golangci-lint run

vuln:
	govulncheck ./...

format-check:
	test -z "$$(gofmt -l .)"

check: test vet format-check lint vuln

# Match CI: use mautrix-go's pure-Go Olm backend without requiring libolm.
# Keep caller-supplied GOFLAGS (for example, -mod=readonly).
override GOFLAGS += -tags=goolm
export GOFLAGS

.DEFAULT_GOAL := test
.PHONY: test lab-test vet build lint vuln format-check check

test:
	go test -race ./...

lab-test:
	bash -n research/emu.sh
	python3 -m unittest discover -s tools/lab -p 'test_*.py'

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

check: test lab-test vet format-check lint vuln

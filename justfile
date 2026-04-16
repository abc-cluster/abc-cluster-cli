set shell := ["bash", "-euo", "pipefail", "-c"]

# abc-cluster-cli — routine dev tasks (`just` / `just --list`).
export CGO_ENABLED := "0"

mod := "github.com/abc-cluster/abc-cluster-cli"

# Show recipes (default).
default:
    @just --list

# Fast dev binary at ./abc (no injected version).
build:
    go build -trimpath -o ./abc .

# Dev binary with git-derived version/commit (same -X wiring as release CI).
build-release out="./abc":
    #!/usr/bin/env bash
    set -euo pipefail
    CLI_VERSION="${CLI_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
    CLI_COMMIT="${CLI_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
    mkdir -p "$(dirname "{{out}}")"
    go build -trimpath \
      -ldflags="-s -w -X '{{mod}}/cmd.version=${CLI_VERSION}' -X '{{mod}}/cmd.commit=${CLI_COMMIT}'" \
      -o "{{out}}" .

# Cross-compile one target into dist/ (set GOOS/GOARCH; defaults to current platform).
dist:
    #!/usr/bin/env bash
    set -euo pipefail
    GOOS="${GOOS:-$(go env GOOS)}"
    GOARCH="${GOARCH:-$(go env GOARCH)}"
    CLI_VERSION="${CLI_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
    CLI_COMMIT="${CLI_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
    mkdir -p dist
    EXT=""
    if [[ "${GOOS}" == "windows" ]]; then EXT=".exe"; fi
    OUT="dist/abc-${GOOS}-${GOARCH}${EXT}"
    go build -trimpath \
      -ldflags="-s -w -X '{{mod}}/cmd.version=${CLI_VERSION}' -X '{{mod}}/cmd.commit=${CLI_COMMIT}'" \
      -o "${OUT}" .
    echo "Built ${OUT}"

# Install release-style binary to ~/bin/abc.
install-local:
    #!/usr/bin/env bash
    set -euo pipefail
    CLI_VERSION="${CLI_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
    CLI_COMMIT="${CLI_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
    mkdir -p "${HOME}/bin"
    tmp="${HOME}/bin/abc.just.tmp.$$"
    go build -trimpath \
      -ldflags="-s -w -X '{{mod}}/cmd.version=${CLI_VERSION}' -X '{{mod}}/cmd.commit=${CLI_COMMIT}'" \
      -o "${tmp}" .
    mv "${tmp}" "${HOME}/bin/abc"
    chmod 0755 "${HOME}/bin/abc"
    echo "Installed ${HOME}/bin/abc"

# Run the CLI from source (`just run -- version`).
run *args:
    go run . {{ args }}

# Offline unit tests (excludes //go:build integration).
test:
    go test -count=1 ./...

# Race detector (slower).
test-race:
    go test -count=1 -race ./...

# Live Nomad tests — needs NOMAD_ADDR (+ token if ACL); see cmd/job/run_integration_test.go.
test-integration:
    go test -tags integration -count=1 -v ./cmd/job/...

vet:
    go vet ./...

fmt:
    gofmt -s -w .

# CI-style formatting check (non-zero exit if any file would change).
fmt-check:
    test -z "$(gofmt -s -l .)"

tidy:
    go mod tidy

mod-verify:
    go mod verify

# Quick pre-push gate (vet, module checksums, tests).
check: vet mod-verify test

# Stricter gate: formatting must already match gofmt (`just fmt` if this fails).
ci: fmt-check check

# Remove build artifacts from this repo.
clean:
    rm -rf dist/ ./abc ./abc.exe

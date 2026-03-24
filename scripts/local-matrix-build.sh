#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

mkdir -p dist

for GOOS in linux darwin windows; do
  for GOARCH in amd64 arm64; do
    EXT=""
    if [[ "${GOOS}" == "windows" ]]; then
      EXT=".exe"
    fi

    OUT="dist/abc-cluster-cli-${GOOS}-${GOARCH}${EXT}"
    echo "Building ${OUT}"
    CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
      go build -trimpath -ldflags="-s -w" -o "${OUT}" .
  done
done

echo "Done. Artifacts are in dist/."

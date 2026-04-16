#!/usr/bin/env bash
set -euo pipefail

REPO_OWNER="abc-cluster"
REPO_NAME="abc-cluster-cli"
INSTALL_DIR="/usr/local/bin"
BIN_BASENAME="abc"

USE_SUDO=0
REQUESTED_VERSION=""
OUT_DIR="$(pwd)"

usage() {
  cat <<'EOF'
Install abc CLI from GitHub releases.

Usage:
  install-abc.sh [--sudo] [--version <tag>] [--out-dir <dir>] [--help]

Options:
  --sudo            Move binary to /usr/local/bin/abc (prompts for sudo password)
  --version <tag>   Release tag to install (default: latest, e.g. v1.2.3)
  --out-dir <dir>   Output directory when not using --sudo (default: current directory)
  --help            Show this help message

Examples:
  # Download latest binary to current directory
  curl -fsSL https://raw.githubusercontent.com/abc-cluster/abc-cluster-cli/main/scripts/install-abc.sh | bash

  # Install latest to /usr/local/bin/abc
  curl -fsSL https://raw.githubusercontent.com/abc-cluster/abc-cluster-cli/main/scripts/install-abc.sh | bash -s -- --sudo

  # Install a specific version
  curl -fsSL https://raw.githubusercontent.com/abc-cluster/abc-cluster-cli/main/scripts/install-abc.sh | bash -s -- --version v1.2.3
EOF
}

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

download_to_file() {
  local url="$1"
  local out="$2"
  if have_cmd curl; then
    curl -fsSL "$url" -o "$out"
    return
  fi
  if have_cmd wget; then
    wget -q "$url" -O "$out"
    return
  fi
  echo "error: curl or wget is required" >&2
  exit 1
}

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m | tr '[:upper:]' '[:lower:]')"

  case "$os" in
    linux) os="linux" ;;
    darwin) os="darwin" ;;
    msys*|mingw*|cygwin*) os="windows" ;;
    *)
      echo "error: unsupported OS: $os" >&2
      exit 1
      ;;
  esac

  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
      echo "error: unsupported architecture: $arch" >&2
      exit 1
      ;;
  esac

  printf '%s %s\n' "$os" "$arch"
}

resolve_version() {
  if [[ -n "${REQUESTED_VERSION}" ]]; then
    printf '%s\n' "${REQUESTED_VERSION}"
    return
  fi

  local url tag
  url="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest"
  tag="$(
    if have_cmd curl; then
      curl -fsSL "$url" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
    else
      wget -qO- "$url" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
    fi
  )"

  if [[ -z "$tag" ]]; then
    echo "error: failed to resolve latest release tag from GitHub" >&2
    exit 1
  fi
  printf '%s\n' "$tag"
}

main() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --sudo)
        USE_SUDO=1
        shift
        ;;
      --version)
        REQUESTED_VERSION="${2:-}"
        if [[ -z "${REQUESTED_VERSION}" ]]; then
          echo "error: --version requires a value" >&2
          exit 1
        fi
        shift 2
        ;;
      --out-dir)
        OUT_DIR="${2:-}"
        if [[ -z "${OUT_DIR}" ]]; then
          echo "error: --out-dir requires a value" >&2
          exit 1
        fi
        shift 2
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        echo "error: unknown argument: $1" >&2
        usage >&2
        exit 1
        ;;
    esac
  done

  if [[ ! -d "$OUT_DIR" ]]; then
    echo "error: output directory does not exist: $OUT_DIR" >&2
    exit 1
  fi

  read -r goos goarch < <(detect_platform)
  local ext="" version asset_name asset_url tmp_file final_path
  if [[ "$goos" == "windows" ]]; then
    ext=".exe"
  fi

  version="$(resolve_version)"
  asset_name="${BIN_BASENAME}-${goos}-${goarch}${ext}"
  asset_url="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${version}/${asset_name}"

  echo "Installing ${BIN_BASENAME} ${version} for ${goos}/${goarch}"
  echo "Asset: ${asset_name}"

  tmp_file="$(mktemp)"
  trap 'rm -f "$tmp_file"' EXIT
  download_to_file "$asset_url" "$tmp_file"
  chmod 0755 "$tmp_file"

  if [[ "$USE_SUDO" -eq 1 ]]; then
    final_path="${INSTALL_DIR}/${BIN_BASENAME}${ext}"
    echo "Moving binary to ${final_path} (sudo required)..."
    sudo mv "$tmp_file" "$final_path"
    sudo chmod 0755 "$final_path"
    echo "Installed: ${final_path}"
  else
    final_path="${OUT_DIR}/${BIN_BASENAME}${ext}"
    mv "$tmp_file" "$final_path"
    chmod 0755 "$final_path"
    echo "Downloaded: ${final_path}"
  fi

  trap - EXIT
  echo "Done."
  echo "Try: ${final_path} --version"
}

main "$@"

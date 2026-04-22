#!/bin/sh
set -eu

# POSIX sh: pipe this file into `sh` (see usage() for the recommended curl URL).
# (`| bash` is still fine.)

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
  # Download latest binary to current directory (GitHub Contents API: ref=main
  # tracks the branch; raw.githubusercontent.com/.../main/... can be CDN-stale.)
  curl -fsSL -H "Accept: application/vnd.github.raw+json" \
    "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" | sh

  # Install latest to /usr/local/bin/abc
  curl -fsSL -H "Accept: application/vnd.github.raw+json" \
    "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" | sh -s -- --sudo

  # Install a specific version
  curl -fsSL -H "Accept: application/vnd.github.raw+json" \
    "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" | sh -s -- --version v1.2.3
EOF
}

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

# GNU Wget requires -L to follow GitHub's 302 to object storage; BusyBox wget
# typically follows redirects and treats unknown options as errors.
wget_to_file() {
  _w_out="$1"
  _w_url="$2"
  if wget -V 2>/dev/null | grep -qi 'GNU [Ww]get'; then
    wget -qL "$_w_url" -O "$_w_out"
  else
    wget -q "$_w_url" -O "$_w_out"
  fi
}

download_to_file() {
  _dl_url="$1"
  _dl_out="$2"
  if have_cmd curl; then
    curl -fsSL "$_dl_url" -o "$_dl_out" && return 0
    return 1
  fi
  if have_cmd wget; then
    wget_to_file "$_dl_out" "$_dl_url" && return 0
    return 1
  fi
  echo "error: curl or wget is required" >&2
  exit 1
}

detect_platform() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m | tr '[:upper:]' '[:lower:]')"

  case "$os" in
    linux) os="linux" ;;
    darwin) os="darwin" ;;
    msys*|mingw*|cygwin*)
      os="windows"
      ;;
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

fetch_latest_tag() {
  _api_url="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest"
  _rel_json="$(mktemp)"
  if have_cmd curl; then
    if ! curl -fsSL "$_api_url" -o "$_rel_json"; then
      rm -f "$_rel_json"
      echo "error: could not fetch ${_api_url}" >&2
      echo "hint: export GITHUB_TOKEN for authenticated API (higher rate limits)" >&2
      exit 1
    fi
  elif have_cmd wget; then
    if ! wget_to_file "$_rel_json" "$_api_url"; then
      rm -f "$_rel_json"
      echo "error: could not fetch ${_api_url}" >&2
      exit 1
    fi
  else
    echo "error: curl or wget is required to resolve latest version" >&2
    exit 1
  fi
  _tag="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$_rel_json" | head -n 1)"
  rm -f "$_rel_json"
  printf '%s' "$_tag"
}

resolve_version() {
  if [ -n "${REQUESTED_VERSION}" ]; then
    printf '%s\n' "${REQUESTED_VERSION}"
    return
  fi

  tag="$(fetch_latest_tag)"
  if [ -z "$tag" ]; then
    echo "error: failed to resolve latest release tag from GitHub (empty result)" >&2
    echo "hint: set GITHUB_TOKEN for higher API rate limits, or pass --version vX.Y.Z" >&2
    exit 1
  fi
  printf '%s\n' "$tag"
}

main() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --sudo)
        USE_SUDO=1
        shift
        ;;
      --version)
        REQUESTED_VERSION="${2:-}"
        if [ -z "${REQUESTED_VERSION}" ]; then
          echo "error: --version requires a value" >&2
          exit 1
        fi
        shift 2
        ;;
      --out-dir)
        OUT_DIR="${2:-}"
        if [ -z "${OUT_DIR}" ]; then
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

  if [ ! -d "$OUT_DIR" ]; then
    echo "error: output directory does not exist: $OUT_DIR" >&2
    echo "hint: mkdir -p \"$OUT_DIR\" or omit --out-dir to use $(pwd)" >&2
    exit 1
  fi

  _plat="$(detect_platform)"
  goos="${_plat%% *}"
  goarch="${_plat#* }"

  ext=""
  if [ "$goos" = "windows" ]; then
    ext=".exe"
  fi

  version="$(resolve_version)"
  asset_name="${BIN_BASENAME}-${goos}-${goarch}${ext}"
  asset_url="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${version}/${asset_name}"

  echo "Installing ${BIN_BASENAME} ${version} for ${goos}/${goarch}"
  echo "Asset: ${asset_name}"

  tmp_file="$(mktemp)"
  trap 'rm -f "$tmp_file"' EXIT INT HUP TERM

  if ! download_to_file "$asset_url" "$tmp_file"; then
    echo "error: download failed: $asset_url" >&2
    echo "hint: check the tag exists and this platform is published (see release assets on GitHub)" >&2
    exit 1
  fi

  chmod 0755 "$tmp_file"

  if [ "$USE_SUDO" -eq 1 ]; then
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

  trap - EXIT INT HUP TERM
  echo "Done."
  echo "Try: ${final_path} --version"
}

main "$@"

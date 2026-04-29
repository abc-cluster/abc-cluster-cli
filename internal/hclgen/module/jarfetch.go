package module

// PipelineGenJarFetchScript returns the bash fragment that resolves
// `$JAR_PATH` for the nf-pipeline-gen jar. The caller is responsible for
// setting `$PIPELINE_GEN_REPO`, `$PIPELINE_GEN_VERSION`, `$JAR_PATH`, and
// (optionally) `$ABC_PIPELINE_GEN_URL_BASE`, `$ABC_PIPELINE_GEN_URL_RESOLVE`,
// `$GITHUB_TOKEN` before invoking. Direct-URL mode is preferred when
// available; otherwise resolves the asset via the GitHub releases API.
//
// Extracted from `generateScriptTmpl` (the prestart of `abc module run`) so
// that the new `abc module samplesheet emit` job can share the same fetch
// logic — a single source of truth for mirror handling, sha256 verification,
// magicDNS overrides, and GitHub auth.
func PipelineGenJarFetchScript() string {
	return `if [ -n "${ABC_PIPELINE_GEN_URL_BASE:-}" ]; then
  # Direct-URL mode: fetch JAR from a mirror (e.g. RustFS) instead of GitHub.
  JAR_URL="${ABC_PIPELINE_GEN_URL_BASE%/}/${PIPELINE_GEN_VERSION}/pipeline-gen.jar"
  SHA_URL="${ABC_PIPELINE_GEN_URL_BASE%/}/${PIPELINE_GEN_VERSION}/sha256sums.txt"
  RESOLVE_OPTS=()
  if [ -n "${ABC_PIPELINE_GEN_URL_RESOLVE:-}" ]; then
    RESOLVE_OPTS+=(--resolve "$ABC_PIPELINE_GEN_URL_RESOLVE")
  fi
  echo ">> Fetching pipeline-gen.jar from mirror: $JAR_URL"
  curl -fsSL "${RESOLVE_OPTS[@]}" -L -o "$JAR_PATH" "$JAR_URL"
  test -s "$JAR_PATH"
  if curl -fsSL "${RESOLVE_OPTS[@]}" -L -o /local/sha256sums.txt "$SHA_URL" 2>/dev/null; then
    EXPECTED="$(awk '$2 == "pipeline-gen.jar" {print $1; exit}' /local/sha256sums.txt)"
    if [ -n "$EXPECTED" ]; then
      ACTUAL="$(sha256sum "$JAR_PATH" | awk '{print $1}')"
      if [ "$ACTUAL" != "$EXPECTED" ]; then
        echo "pipeline-gen.jar sha256 mismatch: got $ACTUAL want $EXPECTED" >&2
        exit 1
      fi
      echo ">> JAR sha256 verified ($EXPECTED)"
    else
      echo ">> sha256sums.txt fetched but did not contain pipeline-gen.jar entry; skipping verify"
    fi
  else
    echo ">> No sha256sums.txt at mirror; skipping JAR integrity check"
  fi
else

if [ "$PIPELINE_GEN_VERSION" = "latest" ]; then
  RELEASE_URL="https://api.github.com/repos/${PIPELINE_GEN_REPO}/releases/latest"
else
  RELEASE_URL="https://api.github.com/repos/${PIPELINE_GEN_REPO}/releases/tags/${PIPELINE_GEN_VERSION}"
fi

ASSET_ID="$(python3 - "$RELEASE_URL" <<'PY'
import json
import os
import sys
import urllib.request

token = os.environ.get("GITHUB_TOKEN", "")
if not token:
    raise SystemExit("Missing GITHUB_TOKEN for nf-pipeline-gen release download")

release_url = sys.argv[1] if len(sys.argv) > 1 else ""
if not release_url:
    raise SystemExit("Missing RELEASE_URL")

headers = {
    "Authorization": f"Bearer {token}",
    "Accept": "application/vnd.github+json",
    "X-GitHub-Api-Version": "2022-11-28",
    "User-Agent": "abc-module-run"
}
req = urllib.request.Request(release_url, headers=headers)
with urllib.request.urlopen(req) as r:
    release = json.load(r)
assets = release.get("assets", [])
jar_assets = [a for a in assets if a.get("name", "").endswith(".jar") or "standalone" in a.get("name", "").lower()]
asset = jar_assets[0] if jar_assets else (assets[0] if assets else None)
if not asset:
    raise SystemExit("No nf-pipeline-gen release assets found")
print(asset["id"])
PY
)"
test -n "$ASSET_ID"
curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/octet-stream" -L -o "$JAR_PATH" \
  "https://api.github.com/repos/${PIPELINE_GEN_REPO}/releases/assets/$ASSET_ID"
test -s "$JAR_PATH"

fi  # end direct-URL mode branch
`
}

// NfCoreModulesFetchScript returns the bash fragment that downloads and
// extracts the nf-core/modules tarball into `$MODULES_DIR`. Caller sets
// `$MODULES_DIR`, `$MODULES_TGZ` ahead of time; honors `$GITHUB_TOKEN` for
// rate-limit mitigation. Resolves the master commit SHA, exports
// `$MODULES_COMMIT` and `$MODULES_REVISION` (12-char prefix unless
// `$ABC_MODULE_REVISION` overrides) for the caller to embed elsewhere.
func NfCoreModulesFetchScript() string {
	return `MODULES_COMMIT="$(python3 - <<'PY'
import json
import os
import urllib.request
headers = {
    "Accept": "application/vnd.github+json",
    "X-GitHub-Api-Version": "2022-11-28",
    "User-Agent": "abc-module-run"
}
tok = os.environ.get("GITHUB_TOKEN", "")
if tok:
    headers["Authorization"] = f"Bearer {tok}"
req = urllib.request.Request(
    "https://api.github.com/repos/nf-core/modules/commits/master",
    headers=headers,
)
with urllib.request.urlopen(req) as r:
    data = json.load(r)
sha = data.get("sha")
if not sha:
    raise SystemExit("Unable to resolve nf-core/modules master commit")
print(sha)
PY
)"
MODULES_REVISION="$(printf '%s' "$MODULES_COMMIT" | cut -c1-12)"
if [ -n "${ABC_MODULE_REVISION:-}" ]; then
  MODULES_REVISION="$ABC_MODULE_REVISION"
fi
if [ -n "${GITHUB_TOKEN:-}" ]; then
  curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -L -o "$MODULES_TGZ" "https://github.com/nf-core/modules/archive/${MODULES_COMMIT}.tar.gz"
else
  curl -fsSL -L -o "$MODULES_TGZ" "https://github.com/nf-core/modules/archive/${MODULES_COMMIT}.tar.gz"
fi
test -s "$MODULES_TGZ"

mkdir -p "$MODULES_DIR"
python3 - <<'PY'
import tarfile
src = '/local/modules.tgz'
dst = '/local/modules-src'
with tarfile.open(src, 'r:gz') as tf:
    members = tf.getmembers()
    root = members[0].name.split('/')[0]
    for m in members:
        if m.name.startswith(root + '/'):
            m.name = m.name[len(root)+1:]
            if m.name:
                tf.extract(m, dst)
PY
`
}

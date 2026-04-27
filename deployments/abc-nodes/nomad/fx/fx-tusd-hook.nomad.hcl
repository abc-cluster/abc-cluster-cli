# fx-tusd-hook.nomad.hcl
#
# tusd post-finish hook: rename S3 object → original filename + ntfy notification.
#
# Architecture
# ────────────
#   tusd (post-finish event)
#     → /hook  (this job, port 14002)
#       ├─ HEAD  s3://<bucket>/<uuid-key>        (check existence)
#       ├─ PUT   s3://<bucket>/<filename>        (copy with x-amz-copy-source)
#       ├─ DELETE s3://<bucket>/<uuid-key>       (remove original)
#       └─ POST  ntfy.aither/tusd-uploads        (notification)
#
# No external dependencies — uses Python 3 stdlib only (hmac, hashlib, urllib).
# AWS SigV4 signing is implemented inline; no boto3 required.
#
# Wiring tusd
# ───────────
# Deploy fx-tusd-hook first (via Terraform), then redeploy tusd with hooks enabled:
#
#   # Step 1 — deploy fx-tusd-hook via Terraform (preferred)
#   cd analysis/packages/abc-cluster-cli/deployments/abc-nodes/terraform
#   abc admin services cli terraform -- apply -auto-approve
#
#   # Step 2 — (re)deploy tusd with the hook URL wired in
#   cd analysis/packages/abc-cluster-cli
#   abc admin services cli nomad -- job run \
#     -var="hook_url=http://fx-tusd-hook.service.consul:14002/hook" \
#     deployments/abc-nodes/nomad/tusd.nomad.hcl
#
# Manual fallback (skip Terraform)
# ─────────────────────────────────
#   abc admin services cli nomad -- job run \
#     deployments/abc-nodes/nomad/fx/fx-tusd-hook.nomad.hcl
#
# Test
# ────
#   bash deployments/abc-nodes/nomad/fx/scripts/test-tusd-hook.sh
#
# Logs
# ────
#   NOMAD_NAMESPACE=abc-automations \
#     abc admin services cli nomad -- alloc logs -f <alloc-id>

# ── Variables ────────────────────────────────────────────────────────────────

variable "docker_node" {
  type    = string
  default = "aither"
  # Must run on a node with a Consul agent so the service can register.
  # On this cluster only "aither" runs Consul; "nomad01" has no Consul agent.
  description = "Node to schedule on (must have a local Consul agent)."
}

variable "port" {
  type        = number
  default     = 14002
  description = "Static port for the hook server (reserved fx range: 14000-14099)."
}

variable "ntfy_url" {
  type        = string
  default     = "http://ntfy.aither/tusd-uploads"
  description = "ntfy topic URL for upload notifications."
}

variable "minio_endpoint" {
  type        = string
  default     = "http://100.70.185.46:9000"
  description = "MinIO S3 API base URL (no trailing slash)."
}

variable "minio_bucket" {
  type        = string
  default     = "tusd"
  description = "Bucket where tusd stores uploads."
}

variable "s3_access_key" {
  type    = string
  default = "minioadmin"
}

variable "s3_secret_key" {
  type    = string
  default = "minioadmin"
}

variable "s3_region" {
  type    = string
  default = "us-east-1"
}

# ── Job ──────────────────────────────────────────────────────────────────────

job "fx-tusd-hook" {
  namespace = "abc-automations"
  type      = "service"
  priority  = 50

  group "hook" {
    count = 1

    constraint {
      attribute = "${attr.unique.hostname}"
      value     = var.docker_node
    }

    restart {
      attempts = 3
      delay    = "20s"
      interval = "5m"
      mode     = "delay"
    }

    network {
      port "http" {
        static = var.port
      }
    }

    service {
      name = "fx-tusd-hook"
      port = "http"
      # provider omitted — uses the cluster default (consul).
      # Explicit "consul" was set but prevented Consul registration on this
      # cluster; dropping to the implicit default matches fx-notify's working
      # behaviour.

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.fx-tusd-hook.rule=Host(`tusd-hook.aither`)",
        "traefik.http.routers.fx-tusd-hook.entrypoints=web",
      ]

      check {
        type     = "http"
        path     = "/healthz"
        interval = "30s"
        timeout  = "5s"
      }

      check_restart {
        limit           = 3
        grace           = "10s"
        ignore_warnings = false
      }
    }

    task "hook" {
      driver = "raw_exec"

      config {
        command = "python3"
        args    = ["${NOMAD_TASK_DIR}/hook.py"]
      }

      env {
        PORT                  = var.port
        NTFY_URL              = var.ntfy_url
        MINIO_ENDPOINT        = var.minio_endpoint
        MINIO_BUCKET          = var.minio_bucket
        AWS_ACCESS_KEY_ID     = var.s3_access_key
        AWS_SECRET_ACCESS_KEY = var.s3_secret_key
        AWS_REGION            = var.s3_region
      }

      template {
        destination = "local/hook.py"
        change_mode = "restart"
        perms       = "0755"

        data = <<-PYEOF
#!/usr/bin/env python3
"""
hook.py — tusd post-finish hook: rename S3 object + ntfy notification.

No external dependencies. AWS SigV4 signing implemented with stdlib only.
"""

import datetime
import hashlib
import hmac
import json
import logging
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
    stream=sys.stdout,
)

# ── Config ────────────────────────────────────────────────────────────────────

PORT      = int(os.environ.get("PORT", "14002"))
ENDPOINT  = os.environ.get("MINIO_ENDPOINT", "http://100.70.185.46:9000").rstrip("/")
BUCKET    = os.environ.get("MINIO_BUCKET", "tusd")
REGION    = os.environ.get("AWS_REGION", "us-east-1")
AK        = os.environ.get("AWS_ACCESS_KEY_ID", "minioadmin")
SK        = os.environ.get("AWS_SECRET_ACCESS_KEY", "minioadmin")
NTFY_URL  = os.environ.get("NTFY_URL", "http://ntfy.aither/tusd-uploads")

# ── AWS SigV4 (stdlib only) ───────────────────────────────────────────────────

def _sign(key, msg):
    return hmac.new(key, msg.encode(), hashlib.sha256).digest()

def _signing_key(secret, date_stamp):
    return _sign(
        _sign(
            _sign(
                _sign(("AWS4" + secret).encode(), date_stamp),
                REGION,
            ),
            "s3",
        ),
        "aws4_request",
    )

def _s3_request(method, key, headers=None, body=b"", copy_source=None):
    """
    Build and send an S3 REST request with AWS SigV4 auth.
    Returns (status_code, response_body_bytes).
    """
    now       = datetime.datetime.utcnow()
    amz_date  = now.strftime("%Y%m%dT%H%M%SZ")
    date_stamp = now.strftime("%Y%m%d")

    parsed   = urllib.parse.urlparse(ENDPOINT)
    host     = parsed.netloc
    url      = f"{ENDPOINT}/{BUCKET}/{urllib.parse.quote(key, safe='/')}"

    body_hash = hashlib.sha256(body).hexdigest()

    hdrs = {
        "Host":                 host,
        "x-amz-date":          amz_date,
        "x-amz-content-sha256": body_hash,
    }
    if copy_source:
        hdrs["x-amz-copy-source"] = copy_source
    if headers:
        hdrs.update(headers)

    signed_headers = ";".join(sorted(k.lower() for k in hdrs))
    canonical_headers = "".join(
        f"{k.lower()}:{v}\n" for k, v in sorted(hdrs.items(), key=lambda x: x[0].lower())
    )

    canonical_request = "\n".join([
        method,
        f"/{BUCKET}/{urllib.parse.quote(key, safe='/')}",
        "",               # query string
        canonical_headers,
        signed_headers,
        body_hash,
    ])

    credential_scope = f"{date_stamp}/{REGION}/s3/aws4_request"
    string_to_sign   = "\n".join([
        "AWS4-HMAC-SHA256",
        amz_date,
        credential_scope,
        hashlib.sha256(canonical_request.encode()).hexdigest(),
    ])

    sig = hmac.new(
        _signing_key(SK, date_stamp),
        string_to_sign.encode(),
        hashlib.sha256,
    ).hexdigest()

    auth = (
        f"AWS4-HMAC-SHA256 Credential={AK}/{credential_scope}, "
        f"SignedHeaders={signed_headers}, Signature={sig}"
    )
    hdrs["Authorization"] = auth

    req = urllib.request.Request(url, data=body or None, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()

# ── S3 helpers ────────────────────────────────────────────────────────────────

def _exists(key):
    status, _ = _s3_request("HEAD", key)
    return status == 200

def _copy(src_key, dst_key):
    copy_src = f"/{BUCKET}/{urllib.parse.quote(src_key, safe='/')}"
    status, body = _s3_request("PUT", dst_key, copy_source=copy_src)
    if status not in (200, 204):
        raise RuntimeError(f"S3 copy failed: {status} {body[:200]}")

def _delete(key):
    status, body = _s3_request("DELETE", key)
    if status not in (200, 204):
        raise RuntimeError(f"S3 delete failed: {status} {body[:200]}")

# ── Payload parsing ───────────────────────────────────────────────────────────

def _first(*values):
    return next((v for v in values if isinstance(v, str) and v), "")

def _source_key(upload):
    storage = upload.get("Storage") or upload.get("storage") or {}
    s3info  = storage.get("S3") or storage.get("s3") or {}
    uid     = _first(upload.get("ID"), upload.get("id"))
    if uid and "+" in uid:
        uid = uid.split("+", 1)[0]
    return _first(
        s3info.get("Key"), s3info.get("ObjectKey"),
        s3info.get("object_key"), s3info.get("Object"), uid,
    )

def _fmt_size(b):
    b = int(b or 0)
    if b < 1024:     return f"{b} B"
    if b < 1 << 20:  return f"{b / 1024:.1f} KB"
    if b < 1 << 30:  return f"{b / (1 << 20):.1f} MB"
    return f"{b / (1 << 30):.2f} GB"

# ── Core logic ────────────────────────────────────────────────────────────────

def handle_upload(payload):
    event  = payload.get("Event") or payload.get("event") or {}
    upload = event.get("Upload") or event.get("upload") or {}
    meta   = upload.get("MetaData") or upload.get("metadata") or {}
    size   = upload.get("Size") or upload.get("size") or 0

    desired = _first(meta.get("filename"), meta.get("name"))
    src     = _source_key(upload)

    logging.info("hook: upload finished  src=%s  desired=%s  size=%s",
                 src or "(unknown)", desired or "(unnamed)", _fmt_size(size))

    if not src:
        logging.warning("hook: cannot determine source key; skipping rename")
        _notify(desired or "(unnamed)", size, desired or "(unnamed)")
        return

    if not desired or src == desired:
        logging.info("hook: no rename needed (key=%s)", src)
        _notify(src, size, src)
        return

    dst      = desired
    src_info = f"{src}.info"
    dst_info = f"{dst}.info"

    # Avoid clobbering an existing file.
    if _exists(dst) or _exists(dst_info):
        stem, dot, ext = desired.partition(".")
        suffix  = src[:8]
        dst      = f"{stem}__{suffix}{dot}{ext}" if dot else f"{stem}__{suffix}"
        dst_info = f"{dst}.info"
        logging.info("hook: name collision, using %s", dst)

    try:
        logging.info("hook: copy s3://%s/%s → %s", BUCKET, src, dst)
        _copy(src, dst)
        _delete(src)
        logging.info("hook: renamed OK")

        if _exists(src_info):
            logging.info("hook: copy .info %s → %s", src_info, dst_info)
            _copy(src_info, dst_info)
            _delete(src_info)
    except Exception as exc:
        logging.error("hook: rename failed: %s", exc)
        # Notify with the UUID key so the upload is still traceable.
        _notify(desired, size, src)
        return

    _notify(desired, size, dst)

# ── ntfy ──────────────────────────────────────────────────────────────────────

def _notify(filename, size, final_key):
    topic = NTFY_URL.rstrip("/").rsplit("/", 1)[-1]
    base  = "/".join(NTFY_URL.rstrip("/").rsplit("/", 1)[:-1])
    msg   = f"{BUCKET}/{final_key}\nOriginal name: {filename}\nSize: {_fmt_size(size)}"
    data  = json.dumps({
        "topic":    topic,
        "title":    "tusd: upload complete",
        "message":  msg,
        "tags":     ["inbox_tray"],
        "priority": 3,
    }).encode()
    req = urllib.request.Request(base, data=data)
    req.add_header("Content-Type", "application/json")
    tok = os.environ.get("NTFY_TOKEN", "")
    if tok:
        req.add_header("Authorization", f"Bearer {tok}")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            logging.info("hook: ntfy %s → HTTP %d", filename, resp.status)
    except Exception as exc:
        logging.warning("hook: ntfy error: %s", exc)

# ── HTTP handler ──────────────────────────────────────────────────────────────

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/healthz":
            self._respond(200, b"ok\n")
        else:
            self._respond(404, b"not found\n")

    def do_POST(self):
        if self.path != "/hook":
            self._respond(404, b"not found\n")
            return
        length = int(self.headers.get("Content-Length", "0"))
        body   = self.rfile.read(length) if length > 0 else b"{}"
        try:
            payload = json.loads(body.decode("utf-8"))
            handle_upload(payload)
        except Exception:
            logging.exception("hook: unexpected error")
        # Always return 200 — never fail the upload due to hook errors.
        self._respond(200, b"{}\n", ct="application/json")

    def _respond(self, code, body, ct="text/plain"):
        self.send_response(code)
        self.send_header("Content-Type", ct)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        logging.info("http: " + fmt, *args)

# ── Entry point ───────────────────────────────────────────────────────────────

if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", PORT), Handler)
    logging.info(
        "hook: listening on 0.0.0.0:%d  bucket=%s  endpoint=%s",
        PORT, BUCKET, ENDPOINT,
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        logging.info("hook: shutting down")
        sys.exit(0)
PYEOF
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}

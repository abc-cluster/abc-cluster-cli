# fx-notify.nomad.hcl
#
# Phase 2 — MinIO/RustFS bucket events → NATS event bus (+ optional ntfy mirror).
#
# Architecture
# ────────────
#   MinIO/RustFS (webhook POST)
#     → notify.aither  (Traefik vhost)
#       → this job, port 14001 (raw_exec Python HTTP server on docker_node)
#         ├─ NATS publish to subject events.uploads.<class>      (primary)
#         └─ ntfy POST to $NTFY_URL                              (mirror, kept for back-compat)
#
# Subject taxonomy (NATS):
#   events.uploads.created   — ObjectCreated:* (Put / Copy / CompleteMultipartUpload …)
#   events.uploads.removed   — ObjectRemoved:*
#   events.uploads.accessed  — ObjectAccessed:*
#   events.uploads.other     — anything else
#
# Subscribers can listen to events.uploads.> (all upload activity), or to a
# specific class.  Payload is the original S3 event record JSON, so subscribers
# don't have to resync schema with this bridge — re-derive whatever they need.
#
# The ntfy mirror stays on by default so existing alerts (humans + tasks
# subscribed to that ntfy topic) keep working.  Set NTFY_URL="" to disable.
#
# The Python HTTP server speaks the NATS wire protocol over a fresh TCP
# socket per publish — no client library, no install step, no dependencies
# beyond the stdlib.  This keeps the raw_exec model intact (Python 3 is on
# every cluster node by default).
#
# Note on fx: the original implementation used metrue/fx to wrap a Go
# function as a Docker container.  That approach ran into a Docker-host
# mismatch (fx hit localhost:8866 — Nomad's Docker plugin endpoint — which
# did not have the locally-built image).  The Python approach is simpler and
# more reliable for a PoC.
#
# Prerequisites
# ─────────────
#   1. python3 in PATH of docker_node (standard on all cluster nodes).
#   2. MinIO webhook configured:
#        bash deployments/abc-nodes/nomad/fx/scripts/minio-webhook-setup.sh
#
# Deploy (Terraform — preferred)
# ──────────────────────────────
#   cd analysis/packages/abc-cluster-cli/deployments/abc-nodes/terraform
#   abc admin services cli terraform -- apply -auto-approve
#
# Deploy (manual fallback)
# ────────────────────────
#   cd analysis/packages/abc-cluster-cli
#   abc admin services cli nomad -- job run \
#     -var="docker_node=aither" \
#     deployments/abc-nodes/nomad/fx/fx-notify.nomad.hcl
#
# Test
# ────
#   bash deployments/abc-nodes/nomad/fx/scripts/test-webhook.sh
#
# Logs
# ────
#   NOMAD_NAMESPACE=abc-automations \
#     abc admin services cli nomad -- alloc logs -f <alloc-id>

# ── Variables ────────────────────────────────────────────────────────────────

variable "docker_node" {
  type        = string
  default     = "aither"
  description = "Hostname of the node to schedule on (constraint target)."
}

variable "ntfy_url" {
  type        = string
  default     = "http://ntfy.aither/minio-events"
  description = "ntfy topic URL for the human-readable mirror. Set to \"\" to disable the ntfy mirror entirely (NATS-only)."
}

variable "nats_host" {
  type        = string
  default     = "100.70.185.46"
  description = "NATS broker host. raw_exec uses host network so the Tailscale IP works directly; bridge mode would need consul-DNS resolution instead."
}

variable "nats_port" {
  type    = number
  default = 4222
}

variable "nats_subject_prefix" {
  type        = string
  default     = "events.uploads"
  description = "Subject prefix for upload events. Full subject is <prefix>.<class> where class ∈ {created, removed, accessed, other}."
}

variable "port" {
  type        = number
  default     = 14001
  description = "Static port for the notify function (reserved fx range: 14000-14099)."
}

# ── Job ──────────────────────────────────────────────────────────────────────

job "fx-notify" {
  namespace = "abc-automations"
  type      = "service"
  priority  = 50

  group "function" {
    count = 1

    # Pin to the requested node.
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

    # Traefik picks this up from Consul and creates the notify.aither vhost.
    service {
      name = "fx-notify"
      port = "http"

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.fx-notify.rule=Host(`notify.aither`)",
        "traefik.http.routers.fx-notify.entrypoints=web",
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

    task "notify" {
      driver = "raw_exec"

      config {
        command = "python3"
        args    = ["${NOMAD_TASK_DIR}/notify.py"]
      }

      env {
        NTFY_URL            = var.ntfy_url
        NATS_HOST           = var.nats_host
        NATS_PORT           = var.nats_port
        NATS_SUBJECT_PREFIX = var.nats_subject_prefix
        PORT                = var.port
      }

      # ── Python HTTP server ──────────────────────────────────────────────────
      # Parses MinIO S3 webhook events and delivers them to ntfy.
      # change_mode = "restart" so a code update triggers a restart.
      template {
        destination = "local/notify.py"
        change_mode = "restart"
        perms       = "0755"

        data = <<-PYEOF
#!/usr/bin/env python3
"""
notify.py — MinIO/RustFS webhook → NATS event bus (+ optional ntfy mirror).

Listens on $PORT (default 14001) for S3 event POSTs.  Each record is:
  • PUBLISHED to NATS subject  $NATS_SUBJECT_PREFIX.<class>  (primary path)
  • POSTED to ntfy at $NTFY_URL                              (mirror; opt-out by setting NTFY_URL="")

Class is derived from the eventName: ObjectCreated:* → "created",
ObjectRemoved:* → "removed", ObjectAccessed:* → "accessed", else "other".

NATS publish uses the wire protocol over a fresh TCP socket per call (no
client library needed — keeps the raw_exec / stdlib-only deploy model).
"""

import json
import os
import socket
import sys
import urllib.request
import urllib.error
from http.server import HTTPServer, BaseHTTPRequestHandler

NTFY_URL            = os.environ.get("NTFY_URL", "http://ntfy.aither/minio-events")
NATS_HOST           = os.environ.get("NATS_HOST", "100.70.185.46")
NATS_PORT           = int(os.environ.get("NATS_PORT", "4222"))
NATS_SUBJECT_PREFIX = os.environ.get("NATS_SUBJECT_PREFIX", "events.uploads")
PORT                = int(os.environ.get("PORT", "14001"))


class Handler(BaseHTTPRequestHandler):
    # ── Health check & routing ──────────────────────────────────────────────

    def do_GET(self):
        if self.path == "/healthz":
            self._respond(200, b"ok\n")
        else:
            self._respond(404, b"not found\n")

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)

        try:
            ev = json.loads(body)
            records = ev.get("Records", [])
        except Exception:
            self._respond(200, b"ok (skipped - not JSON)\n")
            return

        if not records:
            self._respond(200, b"ok (no records)\n")
            return

        for rec in records:
            event_name = rec.get("eventName", "")
            s3 = rec.get("s3", {})
            bucket = s3.get("bucket", {}).get("name", "")
            obj = s3.get("object", {})
            key = obj.get("key", "")
            size = obj.get("size", 0)
            ctype = obj.get("contentType", "")
            etime = rec.get("eventTime", "")

            cls = _event_class(event_name)
            subject = f"{NATS_SUBJECT_PREFIX}.{cls}"
            payload = json.dumps(rec, separators=(",", ":")).encode()

            # 1. NATS publish — primary path.  Failure is non-fatal so the
            #    bridge stays available even if NATS hiccups; the ntfy mirror
            #    will still fire below.
            nats_err = _pub_nats(NATS_HOST, NATS_PORT, subject, payload)
            if nats_err:
                print(f"[notify] nats error on {subject}: {nats_err}", flush=True)
            else:
                print(f"[notify] nats <- {subject}  {bucket}/{key}", flush=True)

            # 2. ntfy mirror — kept for back-compat with existing operator
            #    alerting.  Set NTFY_URL="" to disable.  Errors are logged
            #    but don't fail the request (the source of truth is NATS).
            if NTFY_URL:
                title = _event_title(event_name)
                tags = _event_tags(event_name)
                msg = (
                    f"{bucket}/{key}\n"
                    f"Size: {_fmt_size(size)}\n"
                    f"Type: {ctype}\n"
                    f"Time: {etime}"
                )
                ntfy_err = _post_ntfy(NTFY_URL, title, tags, msg)
                if ntfy_err:
                    print(f"[notify] ntfy error: {ntfy_err}", flush=True)
                else:
                    print(f"[notify] ntfy <- {event_name} {bucket}/{key}", flush=True)

        self._respond(200, b"ok\n")

    # ── Helpers ─────────────────────────────────────────────────────────────

    def _respond(self, code, body):
        self.send_response(code)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        print(f"[notify] {self.address_string()} {fmt % args}", flush=True)


# ── Event helpers ─────────────────────────────────────────────────────────────

def _event_class(name):
    """Map MinIO/RustFS eventName → NATS subject suffix."""
    if "ObjectCreated" in name:
        return "created"
    if "ObjectRemoved" in name:
        return "removed"
    if "ObjectAccessed" in name:
        return "accessed"
    return "other"


def _pub_nats(host, port, subject, payload):
    """One-shot NATS publish — opens a TCP socket, sends CONNECT + PUB, closes.

    Implements just enough of the NATS wire protocol
    (https://docs.nats.io/reference/reference-protocols/nats-protocol)
    to publish without auth.  No-op for the INFO / +OK responses.
    Returns None on success, error string on failure.
    """
    if not host:
        return "NATS_HOST unset"
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            s.settimeout(5)
            s.connect((host, port))
            # Read the server's INFO line — we don't parse it.
            _ = s.recv(4096)
            # Minimal CONNECT — no auth, no TLS, no echo back.
            connect = (
                b'CONNECT {"verbose":false,"pedantic":false,"name":"fx-notify",'
                b'"echo":false,"protocol":1,"lang":"python","version":"stdlib"}\r\n'
            )
            s.sendall(connect)
            # PUB <subject> <#bytes>\r\n<payload>\r\n
            header = f"PUB {subject} {len(payload)}\r\n".encode()
            s.sendall(header + payload + b"\r\n")
            # PING/PONG to confirm the publish reached the server before we
            # close — without this the kernel may drop the buffer on close.
            s.sendall(b"PING\r\n")
            s.settimeout(2)
            _ = s.recv(64)
        return None
    except Exception as e:
        return str(e)


def _event_title(name):
    # HTTP headers are sent as Latin-1 by Python's urllib; use ASCII only
    # to avoid replacement-character corruption in ntfy's title field.
    if "ObjectCreated" in name:
        return "MinIO: file uploaded"
    if "ObjectRemoved" in name:
        return "MinIO: file deleted"
    if "ObjectAccessed" in name:
        return "MinIO: file accessed"
    return f"MinIO: {name}"


def _event_tags(name):
    if "ObjectCreated" in name:
        return "inbox_tray"
    if "ObjectRemoved" in name:
        return "wastebasket"
    return "bell"


def _fmt_size(b):
    if b < 1024:
        return f"{b} B"
    if b < 1 << 20:
        return f"{b / 1024:.1f} KB"
    if b < 1 << 30:
        return f"{b / (1 << 20):.1f} MB"
    return f"{b / (1 << 30):.2f} GB"


def _post_ntfy(url, title, tags, msg):
    # Use ntfy's JSON publish API so all fields (title, tags, message) are
    # sent as UTF-8 JSON — no Latin-1 header encoding surprises.
    payload = json.dumps({
        "topic":    url.rstrip("/").rsplit("/", 1)[-1],
        "title":    title,
        "message":  msg,
        "tags":     [tags],
        "priority": 3,
    }).encode()

    # POST to the ntfy base URL (strip the topic from the path).
    base = "/".join(url.rstrip("/").rsplit("/", 1)[:-1])
    req = urllib.request.Request(base, data=payload)
    req.add_header("Content-Type", "application/json")

    tok = os.environ.get("NTFY_TOKEN", "")
    if tok:
        req.add_header("Authorization", f"Bearer {tok}")

    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            if resp.status >= 400:
                return f"ntfy returned {resp.status}"
        return None
    except urllib.error.URLError as e:
        return str(e)
    except Exception as e:
        return str(e)


# ── Entry point ───────────────────────────────────────────────────────────────

if __name__ == "__main__":
    server = HTTPServer(("0.0.0.0", PORT), Handler)
    print(
        f"[notify] listening on 0.0.0.0:{PORT}"
        f"  nats={NATS_HOST}:{NATS_PORT}/{NATS_SUBJECT_PREFIX}.*"
        f"  ntfy={NTFY_URL or '(disabled)'}",
        flush=True,
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("[notify] shutting down", flush=True)
        sys.exit(0)
PYEOF
      }

      resources {
        cpu    = 50
        memory = 64
      }
    }
  }
}

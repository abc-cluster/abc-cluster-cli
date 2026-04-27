# abc-nodes-auth.nomad.hcl
# Lightweight HTTP service that validates Nomad ACL tokens.
# Caddy calls this as a forward_auth backend before proxying to tusd.
#
# The service:
#   1. Reads the X-Nomad-Token header (or Authorization: Bearer <token>)
#   2. Calls the local Nomad API to validate the token
#   3. Returns 200 with X-Auth-User / X-Auth-Group / X-Auth-Namespace headers on success
#   4. Returns 401 on invalid/missing token
#
# Registered in Consul as "abc-nodes-auth" so Caddy resolves it via
# abc-nodes-auth.service.consul:9191.
#
# Deploy:
#   abc admin services nomad cli -- job run deployments/abc-nodes/nomad/abc-nodes-auth.nomad.hcl

job "abc-nodes-auth" {
  namespace   = "abc-services"
  type        = "service"
  priority    = 80   # Keep higher than user jobs so auth is always available

  group "auth" {
    count = 1

    # Pin to aither: Caddy's forward_auth resolves abc-nodes-auth.service.consul
    # from within the same host. Exec driver + Consul registration works reliably
    # on aither; other nodes may lack Consul integration for exec tasks.
    constraint {
      attribute = "${attr.unique.hostname}"
      value     = "aither"
    }

    network {
      mode = "host"
      port "http" { static = 9191 }
    }

    task "server" {
      driver = "exec"

      config {
        command = "/usr/bin/python3"
        args    = ["-u", "${NOMAD_TASK_DIR}/auth_server.py"]
      }

      template {
        destination = "${NOMAD_TASK_DIR}/auth_server.py"
        data        = <<EOF
#!/usr/bin/env python3
"""
Nomad token ForwardAuth server for Traefik.

Validates X-Nomad-Token (or Authorization: Bearer ...) against Nomad's
/v1/acl/token/self endpoint.  Returns:
  200 + X-Auth-User / X-Auth-Group / X-Auth-Namespace  on valid token
  401                                                   on invalid/missing token
  403                                                   on valid token but no tusd upload capability

Traefik config:
  http.middlewares.nomad-auth.forwardAuth.address: http://127.0.0.1:9191/auth
  http.middlewares.nomad-auth.forwardAuth.authResponseHeaders:
    - X-Auth-User
    - X-Auth-Group
    - X-Auth-Namespace
"""

import http.server
import json
import os
import urllib.request
import urllib.error
from http import HTTPStatus

NOMAD_ADDR   = os.environ.get("NOMAD_ADDR", "http://127.0.0.1:4646")
LISTEN_PORT  = int(os.environ.get("AUTH_LISTEN_PORT", "9191"))

# Map policy names to (group, namespace) for the response headers.
# Extend as new groups are added.
POLICY_MAP = {
    "su-mbhg-bioinformatics-group-admin": ("su-mbhg-bioinformatics", "su-mbhg-bioinformatics"),
    "su-mbhg-bioinformatics-submit":      ("su-mbhg-bioinformatics", "su-mbhg-bioinformatics"),
    "su-mbhg-bioinformatics-member":      ("su-mbhg-bioinformatics", "su-mbhg-bioinformatics"),
    "su-mbhg-hostgen-group-admin":        ("su-mbhg-hostgen",        "su-mbhg-hostgen"),
    "su-mbhg-hostgen-submit":             ("su-mbhg-hostgen",        "su-mbhg-hostgen"),
    "su-mbhg-hostgen-member":             ("su-mbhg-hostgen",        "su-mbhg-hostgen"),
    "admin":                              ("cluster-admin",           "*"),
    "observer":                           ("observer",                "*"),
}


def validate_token(token: str):
    """
    Call Nomad /v1/acl/token/self.
    Returns (name, policies, is_management) or raises urllib.error.HTTPError.

    When Nomad ACLs are disabled the endpoint returns a synthetic token with
    AccessorID == "acls-disabled" and Policies == null.  Treat that as
    management access so all requests are permitted.
    """
    req = urllib.request.Request(
        f"{NOMAD_ADDR}/v1/acl/token/self",
        headers={"X-Nomad-Token": token},
    )
    with urllib.request.urlopen(req, timeout=3) as resp:
        data = json.loads(resp.read())
    # Guard: Nomad sends Policies: null (not []) when ACLs are disabled or the
    # token has no policies — normalise to an empty list to avoid TypeError.
    policies  = data.get("Policies") or []
    is_mgmt   = (
        data.get("Type") == "management"
        or data.get("AccessorID") == "acls-disabled"
    )
    return (data.get("Name", "unknown"), policies, is_mgmt)


class AuthHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass  # suppress access log noise

    def do_GET(self):
        if self.path != "/auth":
            self.send_response(HTTPStatus.NOT_FOUND)
            self.end_headers()
            return

        # Extract token from X-Nomad-Token or Authorization: Bearer <token>
        token = self.headers.get("X-Nomad-Token", "")
        if not token:
            auth = self.headers.get("Authorization", "")
            if auth.startswith("Bearer "):
                token = auth[7:].strip()

        if not token:
            self.send_response(HTTPStatus.UNAUTHORIZED)
            self.send_header("WWW-Authenticate", 'Bearer realm="nomad-token"')
            self.end_headers()
            return

        try:
            name, policies, is_mgmt = validate_token(token)
        except urllib.error.HTTPError:
            # For ForwardAuth we intentionally normalize upstream Nomad ACL
            # errors to 401 so clients never see internals and tests can rely
            # on a strict auth contract.
            self.send_response(HTTPStatus.UNAUTHORIZED)
            self.end_headers()
            return
        except Exception:
            self.send_response(HTTPStatus.INTERNAL_SERVER_ERROR)
            self.end_headers()
            return

        if is_mgmt:
            group, namespace = "cluster-admin", "*"
        else:
            group, namespace = None, None
            for p in policies:
                if p in POLICY_MAP:
                    group, namespace = POLICY_MAP[p]
                    break
            if group is None:
                # Valid Nomad token but no recognised group policy -> deny tusd
                self.send_response(HTTPStatus.FORBIDDEN)
                self.end_headers()
                return

        # Token Name == MinIO username (convention: su-mbhg-<group>_<username>)
        # e.g. token Name="su-mbhg-bioinformatics_alice" => MinIO user is exactly that.
        username = name

        self.send_response(HTTPStatus.OK)
        self.send_header("X-Auth-User",      username)
        self.send_header("X-Auth-Group",     group)
        self.send_header("X-Auth-Namespace", namespace)
        self.end_headers()


if __name__ == "__main__":
    server = http.server.HTTPServer(("0.0.0.0", LISTEN_PORT), AuthHandler)
    print(f"abc-nodes-auth listening on :{LISTEN_PORT}", flush=True)
    server.serve_forever()
EOF
      }

      env {
        # Nomad listens on the Tailscale IP, not loopback, on this cluster.
        NOMAD_ADDR       = "http://100.70.185.46:4646"
        AUTH_LISTEN_PORT = "9191"
      }

      resources {
        cpu    = 50
        memory = 64
      }

      service {
        name     = "abc-nodes-auth"
        port     = "http"
        provider = "consul"
        tags     = ["abc-nodes", "auth", "forwardauth"]

        # TCP check: the auth server has no dedicated health endpoint;
        # a successful TCP connection confirms it is accepting requests.
        check {
          name     = "auth-tcp"
          type     = "tcp"
          interval = "15s"
          timeout  = "3s"
        }
      }
    }
  }
}

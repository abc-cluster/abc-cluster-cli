# abc-nodes-auth — Nomad ACL token ForwardAuth service

job "abc-nodes-auth" {
  namespace = [[ var "auth_namespace" . | quote ]]
  type      = "service"
  priority  = 80

  group "auth" {
    count = 1

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
import http.server
import json
import os
import urllib.request
import urllib.error
from http import HTTPStatus

NOMAD_ADDR = os.environ.get("NOMAD_ADDR", "http://127.0.0.1:4646")
LISTEN_PORT = int(os.environ.get("AUTH_LISTEN_PORT", "9191"))

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
    req = urllib.request.Request(
        f"{NOMAD_ADDR}/v1/acl/token/self",
        headers={"X-Nomad-Token": token},
    )
    with urllib.request.urlopen(req, timeout=3) as resp:
        data = json.loads(resp.read())
    return (
        data.get("Name", "unknown"),
        data.get("Policies", []),
        data.get("Type") == "management",
    )


class AuthHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def do_GET(self):
        if self.path != "/auth":
            self.send_response(HTTPStatus.NOT_FOUND)
            self.end_headers()
            return

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
                self.send_response(HTTPStatus.FORBIDDEN)
                self.end_headers()
                return

        self.send_response(HTTPStatus.OK)
        self.send_header("X-Auth-User", name)
        self.send_header("X-Auth-Group", group)
        self.send_header("X-Auth-Namespace", namespace)
        self.end_headers()


if __name__ == "__main__":
    server = http.server.HTTPServer(("0.0.0.0", LISTEN_PORT), AuthHandler)
    print(f"abc-nodes-auth listening on :{LISTEN_PORT}", flush=True)
    server.serve_forever()
EOF
      }

      env {
        NOMAD_ADDR       = [[ var "auth_nomad_addr" . | quote ]]
        AUTH_LISTEN_PORT = "9191"
      }

      resources {
        cpu    = 50
        memory = 64
      }
    }
  }
}

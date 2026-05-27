package workbench

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"text/template"
)

// caddyAdminAddr is the workbench Caddy admin API address on the platform node.
// It listens on localhost only (not public) and is accessed via SSH.
const caddyAdminAddr = "localhost:2019"

// caddyRouteTmpl generates the Caddy JSON config for one workbench user route.
//
// Route structure: /<user>/* → strip prefix /<user> → reverse_proxy upstreamAddr.
// upstreamAddr is a "host:port" string — for VM backend it is vmIP:8080; for
// Docker backend it is the dynamic allocation port (e.g. 100.70.185.46:23456).
//
// The "@id" field enables idempotent management: DELETE (tolerate 404) + POST
// on every start replaces stale routes without duplicate-ID conflicts.
//
// WebSocket headers (Upgrade/Connection) are forwarded explicitly so that
// code-server and Jupyter kernel connections survive the inner aither→VM hop.
// The outer GCP Caddy sets them on the edge; we mirror the same pattern here.
//
// Note: Caddy template variables like {http.request.header.Upgrade} contain
// single curly braces — they are JSON string content and are NOT interpreted
// by Go's text/template (which only acts on {{...}}).
var caddyRouteTmpl = template.Must(template.New("caddy-route").Parse(`{
  "@id": "wb-{{.User}}",
  "match": [{"path": ["/{{.User}}/*"]}],
  "handle": [{
    "handler": "subroute",
    "routes": [{
      "handle": [
        {"handler": "rewrite", "strip_path_prefix": "/{{.User}}"},
        {
          "handler": "reverse_proxy",
          "upstreams": [{"dial": "{{.UpstreamAddr}}"}],
          "headers": {"request": {"set": {
            "Upgrade":    ["{http.request.header.Upgrade}"],
            "Connection": ["{http.request.header.Connection}"]
          }}}
        }
      ]
    }]
  }]
}`))

// caddyRegisterScriptTmpl is a bash script executed on the platform node to
// register (or update) a workbench route in the live Caddy config via admin API.
//
// Strategy: DELETE (tolerate 404) + POST.
//   - PUT with an @id body causes Caddy to detect a duplicate-ID conflict while
//     re-indexing; DELETE+POST avoids that race.
//   - Idempotent: 404 on DELETE means the route wasn't registered, which is fine.
//   - On VM recreate (new IP), the old route is removed and the fresh one posted.
//
// The route JSON is embedded in a heredoc with a sentinel that cannot appear
// inside valid JSON, avoiding all shell-quoting issues.
var caddyRegisterScriptTmpl = template.Must(template.New("caddy-register").Parse(`#!/usr/bin/env bash
set -euo pipefail
ROUTE_JSON=$(cat << 'ABC_WB_ROUTE_EOF'
{{.RouteJSON}}
ABC_WB_ROUTE_EOF
)
# Remove any existing route for this user (idempotent — 404 is fine).
DEL_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    -X DELETE http://{{.AdminAddr}}/id/{{.RouteID}})
if [ "$DEL_STATUS" != "200" ] && [ "$DEL_STATUS" != "404" ]; then
    echo "caddy admin DELETE error (HTTP $DEL_STATUS)" >&2
    exit 1
fi
# Register the fresh route (current VM IP).
POST_STATUS=$(printf '%s' "$ROUTE_JSON" | curl -s -o /tmp/caddy-wb-resp.txt -w "%{http_code}" \
    -X POST -H "Content-Type: application/json" -d @- \
    http://{{.AdminAddr}}/config/apps/http/servers/workbench/routes)
if [ "$POST_STATUS" = "200" ]; then
    echo "workbench route registered: {{.RouteID}}"
else
    echo "caddy admin POST error (HTTP $POST_STATUS):" >&2
    cat /tmp/caddy-wb-resp.txt >&2
    exit 1
fi
`))

// caddyDeregisterScriptTmpl is a bash script executed on the platform node to
// remove a workbench route from the live Caddy config via admin API.
// Tolerates 404 (route already absent) so stop is idempotent.
var caddyDeregisterScriptTmpl = template.Must(template.New("caddy-deregister").Parse(`#!/usr/bin/env bash
set -eo pipefail
STATUS=$(curl -s -o /tmp/caddy-wb-resp.txt -w "%{http_code}" \
    -X DELETE http://{{.AdminAddr}}/id/{{.RouteID}})
if [ "$STATUS" = "200" ]; then
    echo "workbench route removed: {{.RouteID}}"
elif [ "$STATUS" = "404" ]; then
    echo "workbench route already absent: {{.RouteID}}"
else
    echo "caddy admin DELETE error (HTTP $STATUS):" >&2
    cat /tmp/caddy-wb-resp.txt >&2
    exit 1
fi
`))

// RegisterWorkbenchRoute registers (or updates) the /<user>/* → upstreamAddr
// route in the workbench Caddy running on the platform node.
//
// upstreamAddr is a "host:port" string:
//   - VM backend:     vmIP + ":8080"  (e.g. "10.216.45.192:8080")
//   - Docker backend: alloc host + ":" + dynamic port (e.g. "100.70.185.46:23456")
//
// Idempotent: DELETE (tolerate 404) + POST replaces any stale route on every
// start, including after a VM recreate that produces a new IP.
func RegisterWorkbenchRoute(node NodeSSH, user, upstreamAddr string) error {
	routeID := "wb-" + user

	var routeBuf bytes.Buffer
	if err := caddyRouteTmpl.Execute(&routeBuf, struct {
		User         string
		UpstreamAddr string
	}{user, upstreamAddr}); err != nil {
		return fmt.Errorf("render caddy route JSON: %w", err)
	}

	var scriptBuf bytes.Buffer
	if err := caddyRegisterScriptTmpl.Execute(&scriptBuf, map[string]string{
		"RouteJSON": routeBuf.String(),
		"AdminAddr": caddyAdminAddr,
		"RouteID":   routeID,
	}); err != nil {
		return fmt.Errorf("render caddy register script: %w", err)
	}

	out, err := runScriptOnNode(node, scriptBuf.String())
	if err != nil {
		return fmt.Errorf("caddy admin register %s: %w\n%s", routeID, err, out)
	}
	return nil
}

// DeregisterWorkbenchRoute removes the /<user>/* route from the workbench Caddy.
// Called on 'abc workbench stop' for VM-backend sessions.
// Tolerates 404 so stop remains idempotent if the route was never registered.
func DeregisterWorkbenchRoute(node NodeSSH, user string) error {
	routeID := "wb-" + user

	var scriptBuf bytes.Buffer
	if err := caddyDeregisterScriptTmpl.Execute(&scriptBuf, map[string]string{
		"AdminAddr": caddyAdminAddr,
		"RouteID":   routeID,
	}); err != nil {
		return fmt.Errorf("render caddy deregister script: %w", err)
	}

	out, err := runScriptOnNode(node, scriptBuf.String())
	if err != nil {
		return fmt.Errorf("caddy admin deregister %s: %w\n%s", routeID, err, out)
	}
	return nil
}

// runScriptOnNode executes a bash script on the node via SSH piped through stdin.
// Returns combined stdout+stderr and any error.
func runScriptOnNode(node NodeSSH, script string) (string, error) {
	full := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=30",
		"-o", "StrictHostKeyChecking=no",
		node.Host,
		"bash", "-s",
	}
	cmd := exec.Command("ssh", full...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

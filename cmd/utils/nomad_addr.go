package utils

import (
	"net"
	"net/url"
	"strings"
)

// NormalizeNomadAPIAddr fixes common Nomad HTTP base URL mistakes:
//   - http://host with no port uses HTTP’s implicit :80; Nomad’s default HTTP API port is 4646.
//   - A trailing /v1 is stripped because callers append paths like /v1/jobs themselves.
//
// https://host with no port is left unchanged (often a reverse proxy on :443).
func NormalizeNomadAPIAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return strings.TrimRight(addr, "/")
	}
	if u.Scheme == "http" && u.Port() == "" {
		if h := u.Hostname(); h != "" {
			u.Host = net.JoinHostPort(h, "4646")
		}
	}
	addr = u.String()
	for strings.HasSuffix(addr, "/v1") {
		addr = strings.TrimSuffix(addr, "/v1")
	}
	return strings.TrimRight(addr, "/")
}

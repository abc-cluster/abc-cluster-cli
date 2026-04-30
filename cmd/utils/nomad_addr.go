package utils

import (
	"net"
	"net/url"
	"strings"
)

// WithDefaultNomadHTTPPort appends :4646 when addr uses http, parses, and the
// host is a bare IP literal with no port. Used for CLI flags, environment
// variables, and legacy ~/.abc rows so ad-hoc `http://10.0.0.1` still reaches a
// typical Nomad agent.
//
// DNS hostnames are left at port 80 (implicit). Reasoning: bare IPs almost
// always point at a raw Nomad agent (port 4646 by convention), but DNS names
// usually point at a reverse-proxy / gateway (Caddy, Traefik) that fronts the
// Nomad API on port 80/443. Auto-injecting :4646 on a hostname like
// `aither.mb.sun.ac.za` made the CLI unreachable behind such gateways.
//
// Persisted config can still use an explicit port (see ValidateNomadAddrForContext).
func WithDefaultNomadHTTPPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return addr
	}
	if strings.EqualFold(u.Scheme, "http") && u.Port() == "" {
		if h := u.Hostname(); h != "" && net.ParseIP(h) != nil {
			u.Host = net.JoinHostPort(h, "4646")
		}
	}
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String()
}

// NormalizeNomadAPIAddr trims whitespace, strips a trailing /v1 path segment,
// and removes a trailing slash. It does not add a port; use WithDefaultNomadHTTPPort
// before this when a default Nomad HTTP port is desired.
func NormalizeNomadAPIAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return strings.TrimRight(addr, "/")
	}
	addr = u.String()
	for strings.HasSuffix(addr, "/v1") {
		addr = strings.TrimSuffix(addr, "/v1")
	}
	return strings.TrimRight(addr, "/")
}

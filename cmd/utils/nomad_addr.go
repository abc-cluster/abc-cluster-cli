package utils

import (
	"net"
	"net/url"
	"strings"
)

// WithDefaultNomadHTTPPort appends :4646 when addr uses http, parses, and the host has no port.
// Used for CLI flags, environment variables, and legacy ~/.abc rows so ad-hoc
// `http://host` still reaches a typical Nomad agent. Persisted config should
// use an explicit port instead (see config.ValidateNomadAddrForContext).
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
		if h := u.Hostname(); h != "" {
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

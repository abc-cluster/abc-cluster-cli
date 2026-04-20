package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// CanonicalNomadAPIAddrForYAML normalizes nomad_addr for storage and validation:
// bare http://host (no port) becomes http://host:4646; a trailing /v1 segment and
// trailing slashes are stripped. Matches the effective address used by the Nomad client.
func CanonicalNomadAPIAddrForYAML(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return strings.TrimRight(addr, "/")
	}
	if strings.EqualFold(u.Scheme, "http") && u.Port() == "" {
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

// ValidateNomadAddrForContext rejects values that should not be persisted under
// admin.services.nomad.nomad_addr: http URLs must include an explicit port
// (same style as other admin.services.* base URLs). https without a port is
// allowed (implicit 443).
func ValidateNomadAddrForContext(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	u, err := url.Parse(addr)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("nomad_addr must be a URL with scheme and host (e.g. http://100.115.92.1:4646)")
	}
	if strings.EqualFold(u.Scheme, "http") && u.Port() == "" {
		return fmt.Errorf("nomad_addr for http must include an explicit port (e.g. :4646); use abc config set contexts.<name>.admin.services.nomad.nomad_addr 'http://HOST:PORT'")
	}
	return nil
}

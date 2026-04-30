package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// CanonicalNomadAPIAddrForYAML normalizes nomad_addr for storage and validation:
// bare http://<bare-IP> (no port) becomes http://<IP>:4646 since IP literals
// almost always point at a raw Nomad agent. DNS hostnames are left at their
// implicit port (80) — they typically front Nomad through a reverse-proxy.
// A trailing /v1 segment and trailing slashes are stripped.
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
		if h := u.Hostname(); h != "" && net.ParseIP(h) != nil {
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
// admin.services.nomad.nomad_addr. http URLs targeting a bare IP must include
// an explicit port (almost always :4646 for raw Nomad agents). DNS hostnames
// are accepted without a port — they typically point at a reverse-proxy on :80.
// https without a port is allowed (implicit :443).
func ValidateNomadAddrForContext(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	u, err := url.Parse(addr)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("nomad_addr must be a URL with scheme and host (e.g. http://nomad.example.com or http://100.115.92.1:4646)")
	}
	if strings.EqualFold(u.Scheme, "http") && u.Port() == "" {
		if h := u.Hostname(); net.ParseIP(h) != nil {
			return fmt.Errorf("nomad_addr for http on a bare IP must include an explicit port (e.g. :4646); use abc config set contexts.<name>.admin.services.nomad.nomad_addr 'http://HOST:PORT'")
		}
	}
	return nil
}

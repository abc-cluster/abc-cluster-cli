package config

import (
	"fmt"
	"net/url"
	"strings"
)

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

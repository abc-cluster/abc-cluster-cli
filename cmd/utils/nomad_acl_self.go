package utils

import (
	"context"
	"strings"
)

// NomadACLToken is the subset of GET /v1/acl/token/self used for CLI display
// AND for platform-policy preflight checks (driver allowlist etc.). Roles are
// needed because membership in r-multi-group-admin bypasses driver restrictions.
type NomadACLToken struct {
	AccessorID string         `json:"AccessorID"`
	Name       string         `json:"Name"`
	Type       string         `json:"Type"`
	Policies   []string       `json:"Policies"`
	Roles      []NomadACLRole `json:"Roles"`
}

// NomadACLRole is the subset of Nomad's role-link shape we read off the token.
type NomadACLRole struct {
	ID   string `json:"ID"`
	Name string `json:"Name"`
}

// GetACLTokenSelf calls Nomad GET /v1/acl/token/self with the client's token.
func (c *NomadClient) GetACLTokenSelf(ctx context.Context) (*NomadACLToken, error) {
	var out NomadACLToken
	if err := c.get(ctx, "/v1/acl/token/self", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// NomadWhoamiLabelFromACLToken builds a short human-readable label for config.auth.whoami.
//
// Pool-token Names carry a `pool-` prefix (`provision-pool.sh` mints
// tokens like `pool-lunar_hornbill`) so the operator can distinguish
// pool from named-user tokens at a glance in the Nomad UI. The CLI's
// `auth.whoami` is used downstream as a path segment (pipeline workdir,
// upload destination, job-ID prefix), where the slot pseudonym alone
// is the desired identity — the `pool-` prefix is operator metadata,
// not researcher identity. The tusd mover already strips this prefix
// when computing the upload destination path; doing it here keeps the
// CLI's notion of identity consistent across all paths (job IDs,
// pipeline workdirs, upload destinations).
func NomadWhoamiLabelFromACLToken(tok *NomadACLToken) string {
	if tok == nil {
		return ""
	}
	if n := strings.TrimSpace(tok.Name); n != "" {
		return strings.TrimPrefix(n, "pool-")
	}
	if strings.EqualFold(strings.TrimSpace(tok.Type), "management") {
		return "management"
	}
	var pol []string
	for _, p := range tok.Policies {
		p = strings.TrimSpace(p)
		if p != "" {
			pol = append(pol, p)
		}
	}
	if len(pol) > 0 {
		return strings.Join(pol, ",")
	}
	id := strings.TrimSpace(tok.AccessorID)
	if len(id) >= 8 {
		return "token:" + id[:8]
	}
	if id != "" {
		return "token:" + id
	}
	return ""
}

// NomadTokenWhoamiLabel queries Nomad for the current ACL token and returns a display label.
func NomadTokenWhoamiLabel(ctx context.Context, c *NomadClient) (string, error) {
	if c == nil || strings.TrimSpace(c.Token()) == "" {
		return "", nil
	}
	tok, err := c.GetACLTokenSelf(ctx)
	if err != nil {
		return "", err
	}
	return NomadWhoamiLabelFromACLToken(tok), nil
}

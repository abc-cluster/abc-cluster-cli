package utils

import (
	"context"
	"strings"
)

// NomadACLToken is the subset of GET /v1/acl/token/self used for CLI display.
type NomadACLToken struct {
	AccessorID string   `json:"AccessorID"`
	Name       string   `json:"Name"`
	Type       string   `json:"Type"`
	Policies   []string `json:"Policies"`
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
func NomadWhoamiLabelFromACLToken(tok *NomadACLToken) string {
	if tok == nil {
		return ""
	}
	if n := strings.TrimSpace(tok.Name); n != "" {
		return n
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

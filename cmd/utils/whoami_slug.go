package utils

import (
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// WhoamiSlug converts an auth.whoami value into a short, DNS-safe prefix of
// up to 5 characters.
//
// Rules:
//   - When the value contains colons (e.g. "abc:default:cluster-admin"),
//     only the rightmost segment is used ("cluster-admin").
//   - The segment is lowercased and reduced to [a-z0-9-].
//   - Hyphen-separated words are each abbreviated proportionally so that
//     similar names produce distinct slugs ("group-admin" → "groad",
//     "group-leader" → "grole").
//   - Returns "" when the input is empty or produces an empty slug.
func WhoamiSlug(whoami string) string {
	whoami = strings.TrimSpace(whoami)
	if whoami == "" {
		return ""
	}
	// Use rightmost colon-separated segment.
	if idx := strings.LastIndex(whoami, ":"); idx >= 0 {
		whoami = whoami[idx+1:]
	}
	whoami = strings.TrimSpace(whoami)
	if whoami == "" {
		return ""
	}
	// Sanitize to [a-z0-9-].
	var b strings.Builder
	for _, r := range strings.ToLower(whoami) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return ""
	}

	// Split on hyphens and abbreviate each segment proportionally.
	// ceil(5 / nParts) chars per segment, joined and truncated to 5.
	parts := strings.Split(sanitized, "-")
	// Filter empty parts produced by consecutive hyphens.
	filtered := parts[:0]
	for _, p := range parts {
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return ""
	}

	n := len(filtered)
	charsPerPart := (5 + n - 1) / n // ceil(5/n)

	var out strings.Builder
	for _, p := range filtered {
		if len(p) > charsPerPart {
			out.WriteString(p[:charsPerPart])
		} else {
			out.WriteString(p)
		}
	}

	slug := out.String()
	if len(slug) > 5 {
		slug = strings.TrimRight(slug[:5], "-")
	}
	return slug
}

// ActiveWhoamiSlug returns WhoamiSlug applied to the active context's
// auth.whoami value, or "" if config is unavailable or the field is unset.
func ActiveWhoamiSlug() string {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return ""
	}
	ctx := cfg.ActiveCtx()
	if ctx.Auth == nil {
		return ""
	}
	return WhoamiSlug(ctx.Auth.Whoami)
}

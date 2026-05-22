package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// PlatformDriverPolicy is the canonical "which task drivers may which
// principals submit" rule for the abc-cluster platform.
//
// The policy is intentionally narrow:
//
//   - Default-allowed drivers (everyone): docker, exec.
//
//   - Restricted drivers (privileged bypass only):
//     raw_exec, hpc-bridge, containerd, containerd-driver,
//     java, qemu, podman.
//
//   - Bypass conditions (any one is sufficient):
//     • token Type == "management"  (cluster admins)
//     • token holds role  r-multi-group-admin
//     • token Name in {abhi, jorge, gvds}  (belt-and-braces for the bootstrap
//       roster — should be redundant with the two checks above, but kept
//       explicit so the policy is auditable in one place)
//
// Why CLI-side and not Nomad ACL: Nomad OSS namespace policies have no
// per-driver capability; the canonical mechanism (Sentinel
// `enforce_task_driver`) is Enterprise-only. The CLI is the entrypoint for
// ~all platform users so a friendly client-side rejection is the practical
// 90% solution. The remaining 10% (raw `nomad job run` against the API)
// is left to the control-plane admission proxy follow-up — same policy,
// enforced server-side. See brainstorms/job-admission-policy/.
//
// The policy strings are exported so the control-plane admission service
// can vendor them or re-derive against the same constants.
type PlatformDriverPolicy struct {
	AllowedDrivers    map[string]struct{}
	RestrictedDrivers map[string]struct{}
	PrivilegedRoles   map[string]struct{}
	PrivilegedUsers   map[string]struct{}
}

// DefaultPlatformDriverPolicy returns the policy currently in force.
// Keep this in one place so the control-plane admission proxy can target
// the same source of truth.
func DefaultPlatformDriverPolicy() PlatformDriverPolicy {
	return PlatformDriverPolicy{
		AllowedDrivers: setOf("docker", "exec"),
		RestrictedDrivers: setOf(
			"raw_exec",
			"hpc-bridge",
			"containerd",
			"containerd-driver",
			"java",
			"qemu",
			"podman",
		),
		PrivilegedRoles: setOf("r-multi-group-admin"),
		PrivilegedUsers: setOf("abhi", "jorge", "gvds"),
	}
}

func setOf(items ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, i := range items {
		out[strings.ToLower(strings.TrimSpace(i))] = struct{}{}
	}
	return out
}

// IsPrivileged reports whether the token is allowed to use any task driver.
func (p PlatformDriverPolicy) IsPrivileged(tok *NomadACLToken) bool {
	if tok == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(tok.Type), "management") {
		return true
	}
	if _, ok := p.PrivilegedUsers[strings.ToLower(strings.TrimSpace(tok.Name))]; ok {
		return true
	}
	for _, r := range tok.Roles {
		if _, ok := p.PrivilegedRoles[strings.ToLower(strings.TrimSpace(r.Name))]; ok {
			return true
		}
	}
	return false
}

// CheckDrivers returns the sorted, deduped list of drivers in `drivers` that
// are NOT permitted under `p` for the given token. Empty slice ⇒ allowed.
func (p PlatformDriverPolicy) CheckDrivers(tok *NomadACLToken, drivers []string) []string {
	if p.IsPrivileged(tok) {
		return nil
	}
	seen := map[string]struct{}{}
	var bad []string
	for _, d := range drivers {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		if _, ok := p.AllowedDrivers[strings.ToLower(d)]; ok {
			continue
		}
		bad = append(bad, d)
	}
	sort.Strings(bad)
	return bad
}

// AllowedList returns the sorted, human-readable allow-list for error messages.
func (p PlatformDriverPolicy) AllowedList() []string {
	out := make([]string, 0, len(p.AllowedDrivers))
	for d := range p.AllowedDrivers {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// PreflightJobDriverPolicy enforces the platform's per-principal task-driver
// allowlist BEFORE the existing PreflightJobTaskDrivers fingerprint check.
//
// Behaviour:
//   - Extract the unique driver set from the parsed job JSON.
//   - Read /v1/acl/token/self.
//   - If the principal is privileged, return nil immediately.
//   - Otherwise reject if any extracted driver is outside AllowedDrivers,
//     with a message that names the rejected drivers and the allow-list.
//
// If the token-self call fails (e.g. anonymous against an ACL-disabled
// cluster), we treat that as a fail-open: print a single status line and
// return nil. Hard enforcement is the control plane's job; this is the
// friendly client-side guardrail.
func (c *NomadClient) PreflightJobDriverPolicy(ctx context.Context, jobJSON json.RawMessage, status io.Writer) error {
	if status == nil {
		status = io.Discard
	}
	drivers, err := ExtractJobTaskDrivers(jobJSON)
	if err != nil {
		return err
	}
	if len(drivers) == 0 {
		return nil
	}
	policy := DefaultPlatformDriverPolicy()

	tok, err := c.GetACLTokenSelf(ctx)
	if err != nil {
		// ACL disabled or token unreadable — defer to the control plane.
		fmt.Fprintf(status, "  [abc] driver-policy preflight: cannot read token identity (%v) — skipping\n", err)
		return nil
	}

	if policy.IsPrivileged(tok) {
		return nil
	}
	bad := policy.CheckDrivers(tok, drivers)
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf(
		"abc platform policy: task driver(s) %s are restricted to privileged users\n"+
			"  allowed for your account: %s\n"+
			"  use --driver=docker or --driver=exec, or ask an operator to grant cluster-admin or multi-group-admin role\n"+
			"  (policy enforced client-side; the control-plane admission service mirrors this rule server-side)",
		strings.Join(bad, ", "),
		strings.Join(policy.AllowedList(), ", "),
	)
}

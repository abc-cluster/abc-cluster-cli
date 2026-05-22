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
// Model: ALLOW-by-default with a narrow restricted set. Anything that is
// not in `RestrictedDrivers` is allowed for every principal, including
// official Nomad drivers we haven't enumerated and community drivers
// (singularity, etc.).
//
//   - Restricted drivers (privileged bypass only):
//     raw_exec  — runs as the Nomad client agent UID (root on aither),
//                 outside any sandbox; classic privilege-escalation path.
//     qemu      — full machine virtualisation primitive; can mount host
//                 devices and is not appropriate for general use.
//     java      — direct JVM exec without container isolation; same UID
//                 footprint as raw_exec for security purposes.
//
//   - Allowed by default (non-exhaustive):
//     docker, exec, containerd / containerd-driver, podman,
//     singularity, hpc-bridge, any other registered driver.
//
//   - Bypass conditions for the restricted set (any one is sufficient):
//     • token Type == "management"  (cluster admins)
//     • token holds role  r-multi-group-admin
//     • token Name in {abhi, jorge, gvds}  (belt-and-braces for the bootstrap
//       roster — should be redundant with the two checks above, but kept
//       explicit so the policy is auditable in one place)
//
// FUTURE TIGHTENING — docker becomes restricted once containerd is the
// platform default. The dockerd daemon runs as root and mounts the host
// docker socket into every container build that needs Wave; that's a
// large blast radius. nomad-driver-containerd runs containers via
// containerd directly without a privileged daemon, so it can serve as a
// rootless replacement for the docker driver for end-user workloads.
// When containerd is broadly deployed across the fleet and Wave is
// wired through it, move "docker" from AllowedByDefault into
// RestrictedDrivers (same privileged-bypass list). The CLI guardrail
// flips with a one-line constant change; the control-plane admission
// service must flip in lockstep. See brainstorms/job-admission-policy/.
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
	// RestrictedDrivers is the closed set requiring a privileged bypass.
	// Anything not in this set is allowed by default.
	RestrictedDrivers map[string]struct{}
	PrivilegedRoles   map[string]struct{}
	PrivilegedUsers   map[string]struct{}
}

// DefaultPlatformDriverPolicy returns the policy currently in force.
// Keep this in one place so the control-plane admission proxy can target
// the same source of truth.
func DefaultPlatformDriverPolicy() PlatformDriverPolicy {
	return PlatformDriverPolicy{
		// raw_exec / qemu / java only. See type doc for the future
		// addition of "docker" once containerd is the platform default.
		RestrictedDrivers: setOf("raw_exec", "qemu", "java"),
		PrivilegedRoles:   setOf("r-multi-group-admin"),
		PrivilegedUsers:   setOf("abhi", "jorge", "gvds"),
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
// are RESTRICTED for the given token (i.e. in RestrictedDrivers AND the
// token is not privileged). Drivers not in RestrictedDrivers are allowed
// by default for everyone — official drivers we haven't enumerated,
// community drivers (singularity, etc.), and anything else.
// Empty slice ⇒ allowed.
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
		if _, ok := p.RestrictedDrivers[strings.ToLower(d)]; ok {
			bad = append(bad, d)
		}
	}
	sort.Strings(bad)
	return bad
}

// RestrictedList returns the sorted, human-readable restricted-list for error messages.
func (p PlatformDriverPolicy) RestrictedList() []string {
	out := make([]string, 0, len(p.RestrictedDrivers))
	for d := range p.RestrictedDrivers {
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
			"  restricted set (privileged bypass only): %s\n"+
			"  every other driver is allowed by default — use docker / exec / containerd / podman / singularity / etc., or ask an operator to grant cluster-admin or multi-group-admin role\n"+
			"  (policy enforced client-side; the control-plane admission service mirrors this rule server-side)",
		strings.Join(bad, ", "),
		strings.Join(policy.RestrictedList(), ", "),
	)
}

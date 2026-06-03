package job

// url.go — accept Nomad Web UI URLs in place of a bare job ID.
//
// A URL pasted from the Nomad web UI carries everything needed to address
// a job, its namespace, and (optionally) a specific allocation + task:
//
//   https://<host>/ui/jobs/<job>@<namespace>
//   https://<host>/ui/jobs/<job>@<namespace>/allocations
//   https://<host>/ui/jobs/<job>@<namespace>/allocations?activeTask=<alloc-uuid>-<task>
//
// The hostname is intentionally ignored — it varies by tier (nomad.seedling
// vs nomad.grove vs nomad.cloud). Only the part after `…cloud/` (i.e. the
// path + query) is parsed.
//
// Used by `abc job logs|show|status|stop` so a user pasting a UI URL gets
// the same behaviour as if they'd typed the job ID + flags by hand.

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// resolveJobArg returns a bare Nomad job ID for the positional argument.
//
// If `arg` already looks like a bare ID, it's returned unchanged. If it's
// a Nomad Web UI URL, the URL is parsed and the corresponding flags
// (`--namespace`, `--alloc`, `--task`) are seeded on `cmd` IF AND ONLY IF
// the user hasn't passed them explicitly. The flags are looked up by name,
// so subcommands that don't define a given flag simply skip it (no panic).
//
// Use from any `abc job <verb>` RunE that accepts a positional job ID:
//
//	func runFoo(cmd *cobra.Command, args []string) error {
//	    jobID, err := resolveJobArg(cmd, args[0])
//	    if err != nil { return err }
//	    …
//	}
func resolveJobArg(cmd *cobra.Command, arg string) (string, error) {
	if !looksLikeNomadURL(arg) {
		return arg, nil
	}
	ref, err := ParseNomadURL(arg)
	if err != nil {
		return "", fmt.Errorf("parse Nomad UI URL %q: %w", arg, err)
	}
	// Seed flags only when (a) the parser found a value, (b) the verb
	// defines a flag of that name, and (c) the user hasn't overridden.
	seed := func(flag, val string) {
		if val == "" {
			return
		}
		f := cmd.Flags().Lookup(flag)
		if f == nil || cmd.Flags().Changed(flag) {
			return
		}
		_ = cmd.Flags().Set(flag, val)
	}
	seed("namespace", ref.Namespace)
	seed("alloc", ref.AllocID)
	seed("task", ref.Task)
	return ref.JobID, nil
}

// NomadURLRef holds the addressable parts of a parsed Nomad UI URL.
// All fields are optional except JobID; absent fields are empty strings.
type NomadURLRef struct {
	JobID     string // e.g. "solar-civet-1780492799-9433c853-CALL_WF_GATK_HAPLOTYPE_CALLER"
	Namespace string // e.g. "su-mbhg-hostgen" (from @<ns> after job)
	AllocID   string // e.g. "9259c1ad-ddfa-aa6e-d748-413f0d56dfb2" (from activeTask)
	Task      string // e.g. "nf-task" (suffix of activeTask after the UUID)
}

// looksLikeNomadURL is a cheap predicate so callers don't need to import
// net/url just to decide whether to call ParseNomadURL.
func looksLikeNomadURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// ParseNomadURL extracts the job/ns/alloc/task fields from a Nomad UI URL.
//
// Recognised shapes (everything after the host is what's parsed; the host
// itself is ignored — `nomad.seedling.…` and `nomad.grove.…` both work):
//
//   /ui/jobs/<job>
//   /ui/jobs/<job>@<namespace>
//   /ui/jobs/<job>@<namespace>/allocations
//   /ui/jobs/<job>@<namespace>/allocations?activeTask=<alloc-uuid>-<task>
//
// Returns an error only when the URL is structurally invalid (unparseable,
// or doesn't have a `/ui/jobs/<…>` path). Missing optional fields just
// leave the corresponding NomadURLRef field empty.
func ParseNomadURL(raw string) (NomadURLRef, error) {
	var ref NomadURLRef
	u, err := url.Parse(raw)
	if err != nil {
		return ref, fmt.Errorf("parse URL: %w", err)
	}

	// Path-prefix dispatch. We only care about /ui/jobs/<…>.
	const prefix = "/ui/jobs/"
	if !strings.Contains(u.Path, prefix) {
		return ref, errors.New("not a Nomad jobs URL (path must contain /ui/jobs/<job>)")
	}
	rest := u.Path[strings.Index(u.Path, prefix)+len(prefix):]
	if rest == "" {
		return ref, errors.New("Nomad URL has no job segment after /ui/jobs/")
	}

	// rest may look like: "<job>" | "<job>@<ns>" | "<job>@<ns>/allocations"
	// First slice off any trailing /<subpath> (we don't care which sub-tab).
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}

	// Split on the LAST '@' to defend against job names that happen to
	// contain an '@' (none in practice today, but cheap to be careful).
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		ref.JobID = rest[:at]
		ref.Namespace = rest[at+1:]
	} else {
		ref.JobID = rest
	}

	// activeTask=<alloc-uuid>-<task>
	// A Nomad alloc UUID is canonical 36 chars (8-4-4-4-12 hex). We split
	// on that boundary so the task suffix can contain hyphens of its own
	// (e.g. "nf-task" or "my-multi-dash-task").
	if at := u.Query().Get("activeTask"); at != "" {
		const uuidLen = 36
		if len(at) >= uuidLen && at[uuidLen-1] != '-' {
			// shape: "<36 chars>-<task>"  (UUID has hyphens at fixed positions)
			if len(at) == uuidLen {
				ref.AllocID = at
			} else if len(at) > uuidLen && at[uuidLen] == '-' {
				ref.AllocID = at[:uuidLen]
				ref.Task = at[uuidLen+1:]
			}
		}
	}

	if strings.TrimSpace(ref.JobID) == "" {
		return ref, errors.New("Nomad URL has an empty job ID")
	}
	return ref, nil
}

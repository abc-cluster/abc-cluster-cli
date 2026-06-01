// Package auth — `abc auth claim` subcommand.
//
// Redeems a one-time claim_code against an abc-cluster signup service
// (today: `abc-signup-svc` on the seedling deployment) and installs the
// returned config.yaml into ~/.abc/config.yaml (or ABC_CLI_CONFIG_FILE).
//
// This is the CLI sibling of the browser-based claim form. Both call
// the same HTTP /claim endpoint; the server's Supabase `claim_slot`
// RPC is the single authoritative claim path. See
// design/exploring/abc-auth-claim-cli.md in abc-universe for the full
// rationale and 3-stage roadmap.
//
// Stage-1 surface (this file):
//
//   abc auth claim <claim-code>
//     --tier seedling        # → https://signup.<tier>.abc-cluster.cloud/claim
//     --endpoint URL         # overrides --tier
//     --email EMAIL          # interactive prompt if omitted
//     --name NAME            # interactive prompt if omitted
//     --consent              # skip the interactive y/N consent prompt
//     --group-name GROUP     # blind-pool draw (no claim-code required)
//     --as NAME              # rename the context on import
//     --force                # overwrite existing context of the same name
//     --print-only           # don't write to disk; emit YAML to stdout
//     --no-sync              # skip the post-claim capabilities sync (TBD)
//
// Exit codes are mapped from the server's error vocabulary so callers
// can branch (see ClaimExitCode below).
package auth

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// Server-side error code → CLI exit code mapping. Documented in the
// design doc; stable so shell wrappers can branch reliably.
const (
	exitCodeOK            = 0
	exitCodeGenericErr    = 1
	exitCodeBadFlags      = 2
	exitCodeCodeInvalid   = 3
	exitCodePoolExhausted = 4
	exitCodeEligibility   = 5
	exitCodeNetwork       = 10
)

// claimErrorExit is returned from RunE when we want a non-zero non-1
// exit. cobra's default error path returns 1; we set os.Exit
// explicitly only when the user supplied valid input but the server
// rejected it with a known code, so scripts can distinguish "I asked
// wrong" from "the pool is empty".
type claimErrorExit struct {
	code int
	msg  string
}

func (e *claimErrorExit) Error() string { return e.msg }

func newClaimCmd() *cobra.Command {
	var (
		tier       string
		endpoint   string
		email      string
		name       string
		consent    bool
		groupName  string
		asName     string
		force      bool
		printOnly  bool
		// noSync     bool // Stage-1 deferred; capabilities sync not yet integrated.
		codeStdin  bool
		writePath  string
		credSource string // Phase 1.4: opt-in to a non-default credential-resolution mode
	)

	cmd := &cobra.Command{
		Use:   "claim <claim-code>",
		Short: "Claim a pool slot and install a ready-to-use config",
		Long: `Redeem a claim_code against an abc-cluster signup service.

The server (today: abc-signup-svc on the seedling deployment) calls the
Supabase claim_slot RPC, draws an unclaimed slot from the code's group,
marks it claimed, and returns a complete config.yaml. This command
installs that config at ~/.abc/config.yaml (or ABC_CLI_CONFIG_FILE) and
makes it the active context — equivalent to running
'abc auth context add --from-file' on a downloaded YAML file, but in
one step.

Most users only need the first form below. The other flags exist for
multi-deployment, scripting, and debugging scenarios.

Examples:
  # Default (minimal) — uses --tier=seedling, prompts for consent
  abc auth claim <CODE> --email <you>@sun.ac.za --name "Your Name"

  # Skip the consent prompt (you must have read the deployment policy)
  abc auth claim <CODE> --email <you>@sun.ac.za --name "Your Name" --consent

  # Read claim code from stdin (keeps it out of shell history)
  echo "$CLAIM_CODE" | abc auth claim --code-stdin \
    --email <you>@sun.ac.za --name "Your Name" --consent

  # Blind-pool draw (no code; server picks any free slot in the group)
  abc auth claim --group-name demo \
    --email <you>@sun.ac.za --name "Your Name"

  # Non-default deployment (e.g. grove or a custom tier)
  abc auth claim <CODE> --tier grove --email <you>@example.com --name "…"

  # Print the YAML without touching the on-disk config (debug / piping)
  abc auth claim <CODE> --email <you>@sun.ac.za --name "…" --consent --print-only

Endpoint resolution (rarely needed):
  --endpoint URL          explicit URL wins
  --tier <name>           composes https://signup.<name>.abc-cluster.cloud/claim
  neither                 defaults to --tier=seedling
`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// ── 1. Resolve claim-code ─────────────────────────────────────
			var claimCode string
			if len(args) == 1 {
				claimCode = strings.TrimSpace(args[0])
			}
			if codeStdin {
				if claimCode != "" {
					return &claimErrorExit{exitCodeBadFlags,
						"cannot pass --code-stdin AND a positional argument"}
				}
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read claim code from stdin: %w", err)
				}
				claimCode = strings.TrimSpace(string(b))
			}
			if claimCode == "" && groupName == "" {
				return &claimErrorExit{exitCodeBadFlags,
					"a claim-code (positional, --code-stdin, or env ABC_CLAIM_CODE) " +
						"or --group-name is required"}
			}
			if claimCode == "" {
				if env := strings.TrimSpace(os.Getenv("ABC_CLAIM_CODE")); env != "" {
					claimCode = env
				}
			}

			// ── 2. Resolve endpoint ───────────────────────────────────────
			url, err := resolveClaimEndpoint(endpoint, tier)
			if err != nil {
				return &claimErrorExit{exitCodeBadFlags, err.Error()}
			}

			// ── 3. Resolve identity (email + name) ────────────────────────
			r := bufio.NewReader(cmd.InOrStdin())
			if email == "" {
				email = strings.TrimSpace(os.Getenv("ABC_CLAIM_EMAIL"))
			}
			if email == "" {
				fmt.Fprint(cmd.OutOrStdout(), "Email: ")
				line, _ := r.ReadString('\n')
				email = strings.TrimSpace(line)
			}
			if email == "" {
				return &claimErrorExit{exitCodeBadFlags, "--email is required"}
			}

			if name == "" {
				name = strings.TrimSpace(os.Getenv("ABC_CLAIM_NAME"))
			}
			if name == "" {
				fmt.Fprint(cmd.OutOrStdout(), "Name: ")
				line, _ := r.ReadString('\n')
				name = strings.TrimSpace(line)
			}
			if name == "" {
				return &claimErrorExit{exitCodeBadFlags, "--name is required"}
			}

			// ── 4. POPIA consent — never silent ──────────────────────────
			if !consent {
				fmt.Fprintln(cmd.OutOrStdout(), "")
				fmt.Fprintln(cmd.OutOrStdout(),
					"This deployment is governed by POPIA (and where applicable other")
				fmt.Fprintln(cmd.OutOrStdout(),
					"jurisdictional regulations). By proceeding you confirm that you")
				fmt.Fprintln(cmd.OutOrStdout(),
					"have read the policy at the deployment's signup portal and")
				fmt.Fprintln(cmd.OutOrStdout(),
					"consent to your identity being recorded in the audit log for")
				fmt.Fprintln(cmd.OutOrStdout(),
					"compliance purposes.")
				fmt.Fprint(cmd.OutOrStdout(), "Proceed? [y/N] ")
				line, _ := r.ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
					return &claimErrorExit{exitCodeEligibility, "consent declined"}
				}
				consent = true
			}

			// ── 5. POST /claim ────────────────────────────────────────────
			// Phase 1.4: if the user explicitly asked for a non-default
			// credential-resolution mode, send it in the body. Empty value
			// is omitted so the server falls back to the group's default
			// (or "local" if the group has none).
			payload := map[string]interface{}{
				"claim_code":    claimCode,
				"group_name":    groupName,
				"name":          name,
				"email":         email,
				"popia_consent": consent,
			}
			if cs := strings.TrimSpace(credSource); cs != "" {
				payload["cred_source"] = cs
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("marshal claim request: %w", err)
			}
			req, err := http.NewRequestWithContext(cmd.Context(), "POST", url, bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("build request: %w", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", fmt.Sprintf("abc-cluster-cli/%s", versionForUA()))

			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return &claimErrorExit{exitCodeNetwork,
					fmt.Sprintf("POST %s: %v", url, err)}
			}
			defer resp.Body.Close()
			respBody, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != http.StatusOK {
				return mapServerError(resp.StatusCode, respBody)
			}

			// ── 6. Parse YAML response ───────────────────────────────────
			// The body is a complete config.yaml. We hand it to the same
			// LoadFrom helper that `abc auth context add --from-file` uses,
			// so every safety check (alias collision, force-required overwrite)
			// is inherited verbatim.
			if printOnly {
				_, _ = cmd.OutOrStdout().Write(respBody)
				return nil
			}

			tmp, err := os.CreateTemp("", "abc-claim-*.yaml")
			if err != nil {
				return fmt.Errorf("create temp YAML: %w", err)
			}
			tmpPath := tmp.Name()
			defer os.Remove(tmpPath)
			if _, err := tmp.Write(respBody); err != nil {
				tmp.Close()
				return fmt.Errorf("write temp YAML: %w", err)
			}
			tmp.Close()

			src, err := cfg.LoadFrom(tmpPath)
			if err != nil {
				return fmt.Errorf("parse server response as config YAML: %w", err)
			}
			if len(src.Contexts) == 0 {
				return fmt.Errorf("server returned a YAML with zero contexts")
			}

			// ── 7. Merge into ~/.abc/config.yaml ────────────────────────
			c, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("load existing config: %w", err)
			}

			srcActive := strings.TrimSpace(src.ActiveContext)
			if srcActive == "" {
				for n := range src.Contexts {
					srcActive = n
					break
				}
			}

			added := 0
			for srcName, srcCtx := range src.Contexts {
				targetName := srcName
				if asName != "" && srcName == srcActive {
					targetName = asName
				}
				if _, exists := c.Contexts[targetName]; exists && !force {
					return fmt.Errorf(
						"context %q already exists; use --force to overwrite "+
							"or --as to rename", targetName)
				}
				if _, isAlias := c.ContextAliases[targetName]; isAlias && !force {
					return fmt.Errorf(
						"name %q is already a context alias; use --force "+
							"to overwrite", targetName)
				}
				if err := c.SetContext(targetName, srcCtx); err != nil {
					return fmt.Errorf("set context %q: %w", targetName, err)
				}
				added++
			}
			active := srcActive
			if asName != "" {
				active = asName
			}
			if c.HasDefinedContext(active) {
				c.ActiveContext = active
			}

			if writePath != "" {
				// Honour ABC_CLI_CONFIG_FILE only when --write-path is unset;
				// when --write-path is supplied, write there directly.
				if err := c.SaveTo(writePath); err != nil {
					return fmt.Errorf("save config to %s: %w", writePath, err)
				}
			} else if err := c.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"Claimed slot. Imported %d context(s); active context: %q\n",
				added, c.ActiveContext)
			return nil
		},
	}

	cmd.Flags().StringVar(&tier, "tier", "seedling",
		`Deployment tier; composes https://signup.<tier>.abc-cluster.cloud/claim (default "seedling")`)
	cmd.Flags().StringVar(&endpoint, "endpoint", "",
		"Explicit signup endpoint URL (overrides --tier)")
	cmd.Flags().StringVar(&email, "email", "", "Email address (or env ABC_CLAIM_EMAIL)")
	cmd.Flags().StringVar(&name, "name", "", "Display name (or env ABC_CLAIM_NAME)")
	cmd.Flags().BoolVar(&consent, "consent", false,
		"Skip the interactive POPIA consent prompt (the user must have read the policy already)")
	cmd.Flags().StringVar(&groupName, "group-name", "",
		"Blind-pool group name (server picks a random unclaimed slot)")
	cmd.Flags().StringVar(&asName, "as", "",
		"Rename the imported context to this name")
	cmd.Flags().BoolVar(&force, "force", false,
		"Overwrite an existing context of the same name")
	cmd.Flags().BoolVar(&printOnly, "print-only", false,
		"Print the returned config to stdout instead of writing to disk")
	cmd.Flags().BoolVar(&codeStdin, "code-stdin", false,
		"Read the claim code from stdin (keeps it out of shell history)")
	cmd.Flags().StringVar(&writePath, "write-path", "",
		"Write the config to this path (default: ABC_CLI_CONFIG_FILE or ~/.abc/config.yaml)")
	cmd.Flags().StringVar(&credSource, "cred-source", "",
		"Credential-resolution mode for this claim: \"\" (server default), \"local\", or \"seedling/v1\". "+
			"\"seedling/v1\" mints an opaque token and routes the CLI through the auth-svc broker; "+
			"real Nomad/MinIO secrets never land on the laptop.")

	return cmd
}

// resolveClaimEndpoint maps the --endpoint / --tier flags to a concrete
// signup URL. --endpoint wins outright; --tier composes the canonical
// abc-cluster.cloud subdomain shape. Unset tier defaults to "seedling"
// (set as the Cobra flag default).
func resolveClaimEndpoint(endpoint, tier string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	tier = strings.TrimSpace(tier)
	if endpoint != "" {
		// Allow either a bare host (we append /claim) or a full URL
		// terminating in /claim.
		if !strings.HasPrefix(endpoint, "http://") &&
			!strings.HasPrefix(endpoint, "https://") {
			return "", fmt.Errorf("--endpoint must include scheme (http:// or https://); got %q", endpoint)
		}
		if !strings.HasSuffix(endpoint, "/claim") {
			endpoint = strings.TrimRight(endpoint, "/") + "/claim"
		}
		return endpoint, nil
	}
	if tier == "" {
		return "", errors.New("either --tier or --endpoint must be set")
	}
	// Single canonical pattern. Future tiers (grove, cloud) inherit it;
	// any deployment that breaks the pattern uses --endpoint instead.
	return fmt.Sprintf("https://signup.%s.abc-cluster.cloud/claim", tier), nil
}

// mapServerError translates the JSON error shape that abc-signup-svc
// returns (e.g. {"error":"code_invalid_or_used"}) into a typed
// claimErrorExit with the canonical exit-code mapping.
func mapServerError(status int, body []byte) error {
	var parsed struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(body, &parsed)
	preview := strings.TrimSpace(string(body))
	if len(preview) > 200 {
		preview = preview[:200] + "…"
	}

	switch parsed.Error {
	case "code_invalid_or_used":
		return &claimErrorExit{exitCodeCodeInvalid,
			"claim code is invalid or has already been used"}
	case "pool_exhausted":
		return &claimErrorExit{exitCodePoolExhausted,
			"the pool for this group is empty; ask an admin to top it up"}
	case "consent_required":
		return &claimErrorExit{exitCodeEligibility,
			"server requires explicit POPIA consent"}
	case "email_not_eligible":
		return &claimErrorExit{exitCodeEligibility,
			"email is not in the allowlist for this deployment"}
	case "group_invalid":
		return &claimErrorExit{exitCodeEligibility,
			"--group-name is not a recognised allowlisted group"}
	}

	if status >= 500 {
		return &claimErrorExit{exitCodeNetwork,
			fmt.Sprintf("server error %d: %s", status, preview)}
	}
	return &claimErrorExit{exitCodeGenericErr,
		fmt.Sprintf("claim failed (%d): %s", status, preview)}
}

// versionForUA returns the CLI version string for the User-Agent
// header. Replace with the build-time-injected version once that
// wiring lands; for now a placeholder is good enough — the server
// only needs UA presence for adoption telemetry, not semver gating.
func versionForUA() string {
	if v := os.Getenv("ABC_CLI_VERSION"); v != "" {
		return v
	}
	return "dev"
}

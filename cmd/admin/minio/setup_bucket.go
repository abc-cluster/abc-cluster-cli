package minio

// setup_bucket.go — `abc admin minio setup-bucket`
//
// Configures a su-<group> bucket with platform-recommended defaults:
//   1. Enable object versioning (mc version enable)
//   2. Create required prefix placeholders (.keep objects)
//   3. Set lifecycle rules (mc ilm add):
//        trash/      — expire objects after 30 days
//        user/       — expire non-current versions after 90 days
//        shared/     — expire non-current versions after 180 days
//   4. Tag the bucket: managed-by=abc-cluster, group=<group>
//
// Idempotent — re-running does not reset existing data or lifecycle rules.
// Safe to run on a bucket that already has data.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
	"github.com/spf13/cobra"
)

func newSetupBucketCmd() *cobra.Command {
	var group string
	var dryRun bool
	var skipVersioning bool
	var skipLifecycle bool
	var trashDays int
	var userVersionDays int
	var sharedVersionDays int

	cmd := &cobra.Command{
		Use:   "setup-bucket",
		Short: "Configure a group bucket with versioning, lifecycle rules, and platform prefixes",
		Long: `Set up a su-<group> MinIO bucket with ABC-cluster platform defaults.

Steps performed:
  1. Enable object versioning — required for meaningful delete vs purge semantics
  2. Create required prefix placeholders (common/, shared/, trash/, user/, archive/)
  3. Set lifecycle rules:
       trash/   — objects expire after N days (default 30)
       user/    — non-current versions expire after N days (default 90)
       shared/  — non-current versions expire after N days (default 180)
  4. Tag the bucket: managed-by=abc-cluster, group=<group>

This command is idempotent — re-running on an already-configured bucket is safe.

Why versioning matters:
  - abc data delete: permanently removes current version (not just a delete marker)
  - abc data purge: removes ALL versions and delete markers (full erasure)
  - Without versioning, delete and purge are identical

Examples:

  # Set up the mbhg-hostgen group bucket with defaults:
  abc admin minio setup-bucket --group mbhg-hostgen

  # Preview without making changes:
  abc admin minio setup-bucket --group mbhg-hostgen --dry-run

  # Custom lifecycle retention:
  abc admin minio setup-bucket --group mbhg-hostgen \
    --trash-days 14 --user-version-days 60 --shared-version-days 365`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if group == "" {
				return fmt.Errorf("--group is required (e.g. mbhg-hostgen for bucket su-mbhg-hostgen)")
			}
			group = strings.TrimPrefix(group, "su-")
			bucket := "su-" + group

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			ctx := cfg.ActiveCtx()

			bin, alias, tmpDir, cleanup, err := findMcliWithAlias(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			if dryRun {
				fmt.Fprintln(errOut, "[dry-run] No changes will be made.")
			}

			fmt.Fprintf(out, "Configuring bucket: %s (group: %s)\n\n", bucket, group)

			// Step 1: Enable versioning.
			if !skipVersioning {
				if err := setupVersioning(bin, alias, tmpDir, bucket, dryRun, out); err != nil {
					return fmt.Errorf("versioning: %w", err)
				}
			}

			// Step 2: Create required prefix placeholders.
			prefixes := []string{"common", "shared", "trash", "user", "archive"}
			for _, p := range prefixes {
				if err := ensurePrefix(bin, alias, tmpDir, bucket, p, dryRun, out); err != nil {
					fmt.Fprintf(errOut, "  [warn] could not create %s/: %v\n", p, err)
				}
			}

			// Step 3: Set lifecycle rules.
			if !skipLifecycle {
				rules := []lifecycleRule{
					{prefix: "trash/", expiryDays: trashDays, desc: "trash objects expire"},
					{prefix: "user/", noncurrentDays: userVersionDays, desc: "user/ non-current versions expire"},
					{prefix: "shared/", noncurrentDays: sharedVersionDays, desc: "shared/ non-current versions expire"},
				}
				for _, r := range rules {
					if err := applyLifecycleRule(bin, alias, tmpDir, bucket, r, dryRun, out); err != nil {
						fmt.Fprintf(errOut, "  [warn] lifecycle rule for %s: %v\n", r.prefix, err)
					}
				}
			}

			// Step 4: Tag the bucket.
			if err := tagBucket(bin, alias, tmpDir, bucket, group, dryRun, out); err != nil {
				fmt.Fprintf(errOut, "  [warn] bucket tagging: %v\n", err)
			}

			fmt.Fprintf(out, "\n✓ Bucket %s configured.\n", bucket)
			if !skipVersioning && !dryRun {
				fmt.Fprintf(out, "\n  Versioning: enabled\n")
				fmt.Fprintf(out, "  Trash retention: %d days\n", trashDays)
				fmt.Fprintf(out, "  User version retention: %d days\n", userVersionDays)
				fmt.Fprintf(out, "  Shared version retention: %d days\n", sharedVersionDays)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&group, "group", "", "Group name (e.g. mbhg-hostgen for su-mbhg-hostgen) [required]")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without applying them")
	cmd.Flags().BoolVar(&skipVersioning, "skip-versioning", false, "Skip enabling versioning")
	cmd.Flags().BoolVar(&skipLifecycle, "skip-lifecycle", false, "Skip setting lifecycle rules")
	cmd.Flags().IntVar(&trashDays, "trash-days", 30, "Days before trash/ objects expire")
	cmd.Flags().IntVar(&userVersionDays, "user-version-days", 90, "Days before non-current user/ versions expire")
	cmd.Flags().IntVar(&sharedVersionDays, "shared-version-days", 180, "Days before non-current shared/ versions expire")

	_ = cmd.MarkFlagRequired("group")
	return cmd
}

// findMcliWithAlias resolves the mcli binary and sets up an ephemeral alias.
// Returns the binary path, alias name, tmpDir, and cleanup func.
func findMcliWithAlias(ctx config.Context) (bin, alias, tmpDir string, cleanup func(), err error) {
	mcBin, err := findMcliBin()
	if err != nil {
		return "", "", "", func() {}, err
	}

	ep, ak, sk := resolveMinioCredsForMcli(ctx)
	if ep == "" || ak == "" || sk == "" {
		return "", "", "", func() {}, fmt.Errorf(
			"no MinIO credentials in active context\n"+
				"  Run: abc config set contexts.<name>.admin.services.minio.endpoint http://<host>:9000",
		)
	}

	tmp, err := os.MkdirTemp("", "abc-mcli-admin-*")
	if err != nil {
		return "", "", "", func() {}, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(tmp) }

	c := exec.Command(mcBin, "alias", "set", "abcadm", ep, ak, sk, "--quiet")
	c.Env = append(os.Environ(), "MCLI_CONFIG_DIR="+tmp)
	if out, err := c.CombinedOutput(); err != nil {
		cleanup()
		return "", "", "", func() {}, fmt.Errorf("mcli alias set: %w\n%s", err, out)
	}
	return mcBin, "abcadm", tmp, cleanup, nil
}

// findMcliBin resolves the mcli or mc (MinIO Client) binary.
func findMcliBin() (string, error) {
	// Check ABC_BIN_MCLI env var.
	if v := strings.TrimSpace(envvars.Get("ABC_BIN_MCLI")); v != "" {
		if isExec(v) {
			return v, nil
		}
	}
	// Check ABC_BIN_MC env var.
	if v := strings.TrimSpace(envvars.Get("ABC_BIN_MC")); v != "" {
		if isExec(v) && isMinioClientBin(v) {
			return v, nil
		}
	}
	// Check ~/.abc/binaries/mcli then mc.
	for _, name := range []string{"mcli", "mc"} {
		home, _ := os.UserHomeDir()
		p := home + "/.abc/binaries/" + name
		if isExec(p) {
			if name == "mc" && !isMinioClientBin(p) {
				continue
			}
			return p, nil
		}
	}
	// System PATH.
	for _, name := range []string{"mcli", "mc"} {
		if p, err := exec.LookPath(name); err == nil {
			if name == "mc" && !isMinioClientBin(p) {
				continue
			}
			return p, nil
		}
	}
	return "", fmt.Errorf(
		"mcli (MinIO Client) not found.\n\n"+
			"Install: abc admin tools fetch mcli\n"+
			"Or set:  ABC_BIN_MCLI=/path/to/mc",
	)
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

func isMinioClientBin(p string) bool {
	out, err := exec.Command(p, "--version").Output()
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(out))
	return strings.Contains(lower, "minio") || strings.Contains(lower, "mc version")
}

func resolveMinioCredsForMcli(ctx config.Context) (endpoint, accessKey, secretKey string) {
	for _, svc := range []string{"minio", "rustfs"} {
		ep, _ := config.GetAdminFloorField(&ctx.Admin.Services, svc, "endpoint")
		if ep == "" {
			ep, _ = config.GetAdminFloorField(&ctx.Admin.Services, svc, "http")
		}
		ak, _ := config.GetAdminFloorField(&ctx.Admin.Services, svc, "access_key")
		if ak == "" {
			ak, _ = config.GetAdminFloorField(&ctx.Admin.Services, svc, "user")
		}
		sk, _ := config.GetAdminFloorField(&ctx.Admin.Services, svc, "secret_key")
		if sk == "" {
			sk, _ = config.GetAdminFloorField(&ctx.Admin.Services, svc, "password")
		}
		if ep != "" && ak != "" && sk != "" {
			return strings.TrimRight(ep, "/"), ak, sk
		}
	}
	if n := ctx.Admin.ABCNodes; n != nil {
		return strings.TrimRight(n.S3Endpoint, "/"), n.S3AccessKey, n.S3SecretKey
	}
	return "", "", ""
}

func mcliRun(bin, tmpDir string, args ...string) (string, error) {
	c := exec.Command(bin, args...)
	c.Env = append(os.Environ(), "MCLI_CONFIG_DIR="+tmpDir)
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func setupVersioning(bin, alias, tmpDir, bucket string, dryRun bool, out interface{ Write([]byte) (int, error) }) error {
	// Check current state first.
	info, _ := mcliRun(bin, tmpDir, "version", "info", alias+"/"+bucket)
	if strings.Contains(info, "un-versioned") || !strings.Contains(info, "versioned") {
		if dryRun {
			fmt.Fprintf(out, "  [dry-run] would enable versioning on %s\n", bucket)
			return nil
		}
		result, err := mcliRun(bin, tmpDir, "version", "enable", alias+"/"+bucket)
		if err != nil {
			return fmt.Errorf("mc version enable: %w\n%s", err, result)
		}
		fmt.Fprintf(out, "  ✓ versioning enabled: %s\n", result)
	} else {
		fmt.Fprintf(out, "  ✓ versioning already enabled\n")
	}
	return nil
}

func ensurePrefix(bin, alias, tmpDir, bucket, prefix string, dryRun bool, out interface{ Write([]byte) (int, error) }) error {
	key := alias + "/" + bucket + "/" + prefix + "/.keep"
	if dryRun {
		fmt.Fprintf(out, "  [dry-run] would ensure %s/.keep exists\n", prefix)
		return nil
	}
	// Check if .keep already exists.
	if _, err := mcliRun(bin, tmpDir, "stat", key); err == nil {
		fmt.Fprintf(out, "  ✓ %s/ already exists\n", prefix)
		return nil
	}
	// Create the placeholder.
	c := exec.Command(bin, "pipe", key)
	c.Env = append(os.Environ(), "MCLI_CONFIG_DIR="+tmpDir)
	c.Stdin = strings.NewReader("")
	if result, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("create %s/.keep: %w\n%s", prefix, err, result)
	}
	fmt.Fprintf(out, "  ✓ created %s/.keep\n", prefix)
	return nil
}

type lifecycleRule struct {
	prefix         string
	expiryDays     int // 0 = don't set expiry
	noncurrentDays int // 0 = don't set noncurrent
	desc           string
}

func applyLifecycleRule(bin, alias, tmpDir, bucket string, rule lifecycleRule, dryRun bool, out interface{ Write([]byte) (int, error) }) error {
	args := []string{"ilm", "add", alias + "/" + bucket, "--prefix", rule.prefix}
	if rule.expiryDays > 0 {
		args = append(args, "--expiry-days", fmt.Sprintf("%d", rule.expiryDays))
	}
	if rule.noncurrentDays > 0 {
		args = append(args, "--noncurrent-expire-days", fmt.Sprintf("%d", rule.noncurrentDays))
	}

	if dryRun {
		fmt.Fprintf(out, "  [dry-run] would add lifecycle rule: %s → %s (%d/%d days)\n",
			rule.prefix, rule.desc, rule.expiryDays, rule.noncurrentDays)
		return nil
	}

	result, err := mcliRun(bin, tmpDir, args...)
	if err != nil {
		return fmt.Errorf("mc ilm add: %w\n%s", err, result)
	}
	fmt.Fprintf(out, "  ✓ lifecycle rule: %s → %s\n", rule.prefix, rule.desc)
	return nil
}

func tagBucket(bin, alias, tmpDir, bucket, group string, dryRun bool, out interface{ Write([]byte) (int, error) }) error {
	tags := fmt.Sprintf("managed-by=abc-cluster&group=%s", group)
	if dryRun {
		fmt.Fprintf(out, "  [dry-run] would tag bucket: %s\n", tags)
		return nil
	}
	result, err := mcliRun(bin, tmpDir, "tag", "set", alias+"/"+bucket, tags)
	if err != nil {
		return fmt.Errorf("mc tag set: %w\n%s", err, result)
	}
	fmt.Fprintf(out, "  ✓ bucket tagged: %s\n", tags)
	return nil
}

package workbench

// provision-group-mounts sets up geesefs FUSE mounts for a group's common/
// and shared/ S3 prefixes and bind-mounts them read-only (common/) and
// read-write (shared/) into each slot's home directory.
//
// One geesefs systemd service per group (not per slot) mounts the whole
// group bucket. Per-slot bind mounts are fstab entries with
// x-systemd.requires so they only activate after the geesefs service is up.
//
// Storage impact: zero bytes on aither — geesefs streams directly from MinIO.
// Writes to ~/group-shared/ go directly to the MinIO bucket; reads from
// ~/group-common/ stream from MinIO on access.
//
// Grove/garden upgrade path: replace geesefs with JuiceFS backed by a
// Redis/PostgreSQL metadata store for full POSIX semantics across nodes.

import (
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	wbinternal "github.com/abc-cluster/abc-cluster-cli/internal/workbench"
	"github.com/spf13/cobra"
)

const defaultGeesefsVersion = "v0.42.1"

// geesefsDownloadURL returns the GitHub release URL for the Linux amd64 binary.
func geesefsDownloadURL(version string) string {
	return fmt.Sprintf(
		"https://github.com/yandex-cloud/geesefs/releases/download/%s/geesefs-linux-amd64",
		version,
	)
}

func newProvisionGroupMountsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provision-group-mounts",
		Short: "Mount group common/ and shared/ S3 prefixes into slot home dirs via geesefs",
		Long: `Set up geesefs FUSE mounts so each workbench slot gets:

  ~/group-common/   read-only view of s3://<bucket>/common/
  ~/group-shared/   read-write view of s3://<bucket>/shared/

One geesefs systemd service per group mounts the bucket on aither.
Per-slot bind mounts (fstab entries) expose the two subdirs into each
slot's home directory.

Storage impact: zero — geesefs streams directly from MinIO. No data is
replicated to aither's disk.

These directories are for casual copy operations only, not job execution
directories. Running Nextflow/Nomad workloads against them is unsupported
(FUSE latency degrades job throughput).

Grove/garden upgrade path: replace geesefs with JuiceFS backed by
Redis/PostgreSQL for full POSIX semantics across multiple nodes.

Examples:

  # Mount for group mbhg-hostgen, all slots:
  abc admin services workbench provision-group-mounts --group mbhg-hostgen

  # Specific slots only:
  abc admin services workbench provision-group-mounts --group mbhg-hostgen \
    --slots calm-dassie,lunar-hornbill

  # Dry-run:
  abc admin services workbench provision-group-mounts --group mbhg-hostgen \
    --dry-run`,
		RunE: runProvisionGroupMounts,
	}

	cmd.Flags().String("group", "", "Group name — the MinIO bucket/Nomad namespace suffix (e.g. mbhg-hostgen for su-mbhg-hostgen) [required]")
	cmd.Flags().String("slots", "", "Comma-separated slot names to bind-mount (default: all slots under /data/workbench/)")
	cmd.Flags().String("node", "", "SSH host alias for the platform node (default: sun-<node> from config or sun-aither)")
	cmd.Flags().String("geesefs-version", defaultGeesefsVersion, "geesefs release tag to install if absent")
	cmd.Flags().Bool("dry-run", false, "Show what would be done without changing anything")

	_ = cmd.MarkFlagRequired("group")

	return cmd
}

func runProvisionGroupMounts(cmd *cobra.Command, args []string) error {
	groupFlag, _ := cmd.Flags().GetString("group")
	slotsFlag, _ := cmd.Flags().GetString("slots")
	nodeOverride, _ := cmd.Flags().GetString("node")
	geesefsVer, _ := cmd.Flags().GetString("geesefs-version")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Normalise group name: strip leading "su-" if provided.
	group := strings.TrimPrefix(groupFlag, "su-")
	bucket := "su-" + group

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()
	node := nodeFromCtx(actx, nodeOverride)

	// Resolve MinIO credentials from admin.services.minio.
	minioEndpoint, accessKey, secretKey, err := resolveMinioCredsForGeesefs(actx)
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "[dry-run] No changes will be made.")
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Group: %s  Bucket: %s  Node: %s\n", group, bucket, node.Host,
	)

	// 1. FUSE prerequisites — fuse3 package and user_allow_other.
	if err := ensureFusePrereqs(cmd, node, dryRun); err != nil {
		return err
	}

	// 2. geesefs binary.
	if err := ensureGeesefsInstalled(cmd, node, geesefsVer, dryRun); err != nil {
		return err
	}

	// 3. Ensure common/ and shared/ prefixes exist in the MinIO bucket.
	if err := ensureGroupPrefixes(cmd, node, minioEndpoint, accessKey, secretKey, bucket, dryRun); err != nil {
		return err
	}

	// 4. Write and start the geesefs systemd service for this group.
	if err := ensureGeesefsService(cmd, node, group, bucket, minioEndpoint, accessKey, secretKey, dryRun); err != nil {
		return err
	}

	// 5. Discover slots and add per-slot bind mount fstab entries.
	slots, err := resolveSlots(node, slotsFlag)
	if err != nil {
		return fmt.Errorf("resolve slots: %w", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Adding bind mounts for %d slot(s)...\n", len(slots))

	ok, failed := 0, 0
	for _, slot := range slots {
		if err := provisionSlotBindMounts(cmd, node, group, slot, dryRun); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  [%s] error: %v\n", slot, err)
			failed++
		} else {
			ok++
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Done: %d slots provisioned, %d failed.\n", ok, failed)
	if failed > 0 {
		return fmt.Errorf("%d slot(s) failed", failed)
	}
	return nil
}

// ensureFusePrereqs installs fuse3 and enables user_allow_other.
func ensureFusePrereqs(cmd *cobra.Command, node wbinternal.NodeSSH, dryRun bool) error {
	if dryRun {
		fmt.Fprintln(cmd.OutOrStdout(), "  [dry-run] would ensure fuse3 installed + user_allow_other in /etc/fuse.conf")
		return nil
	}
	script := `set -euo pipefail
# Install fuse3 if absent.
if ! dpkg -l fuse3 2>/dev/null | grep -q '^ii'; then
    apt-get install -y -qq fuse3
fi
# Enable user_allow_other (needed when non-root users run geesefs).
FUSE_CONF=/etc/fuse.conf
if ! grep -qE '^\s*user_allow_other' "$FUSE_CONF" 2>/dev/null; then
    echo "user_allow_other" >> "$FUSE_CONF"
fi
echo "ok"
`
	out, err := wbinternal.RunOnNodePublic(node, "sudo", "bash", "-c", script)
	if err != nil {
		return fmt.Errorf("fuse prereqs: %w\n%s", err, out)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "  fuse3 prereqs: ok")
	return nil
}

// ensureGeesefsInstalled downloads and installs the pinned geesefs binary
// to /usr/local/bin/geesefs if not already present.
func ensureGeesefsInstalled(cmd *cobra.Command, node wbinternal.NodeSSH, version string, dryRun bool) error {
	out, _ := wbinternal.RunOnNodePublic(node, "test", "-x", "/usr/local/bin/geesefs", "&&", "echo", "present")
	if strings.Contains(out, "present") {
		v, _ := wbinternal.RunOnNodePublic(node, "/usr/local/bin/geesefs", "--version")
		fmt.Fprintf(cmd.OutOrStdout(), "  geesefs already installed: %s\n", strings.TrimSpace(v))
		return nil
	}

	dlURL := geesefsDownloadURL(version)
	fmt.Fprintf(cmd.OutOrStdout(), "  Installing geesefs %s...\n", version)

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "  [dry-run] would download %s → /usr/local/bin/geesefs\n", dlURL)
		return nil
	}

	script := fmt.Sprintf(`set -euo pipefail
curl -fsSL %q -o /usr/local/bin/geesefs
chmod 755 /usr/local/bin/geesefs
/usr/local/bin/geesefs --version
`, dlURL)

	out, err := wbinternal.RunOnNodePublic(node, "sudo", "bash", "-c", script)
	if err != nil {
		return fmt.Errorf("install geesefs: %w\n%s", err, out)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  installed: %s\n", strings.TrimSpace(out))
	return nil
}

// ensureGroupPrefixes creates common/.keep and shared/.keep placeholder objects
// in the bucket so the directories appear in geesefs listings.
func ensureGroupPrefixes(cmd *cobra.Command, node wbinternal.NodeSSH, endpoint, accessKey, secretKey, bucket string, dryRun bool) error {
	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "  [dry-run] would ensure s3://%s/common/ and s3://%s/shared/ exist\n", bucket, bucket)
		return nil
	}
	script := fmt.Sprintf(`set -euo pipefail
export AWS_ACCESS_KEY_ID=%q
export AWS_SECRET_ACCESS_KEY=%q
MC=/usr/local/bin/mc
if ! command -v "$MC" &>/dev/null; then MC=$(which mc || which mcli); fi

# Set up ephemeral alias.
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
MC_CONFIG_DIR="$TMP" "$MC" alias set _abc %q %q %q --quiet

# Create placeholder objects to establish the directory structure.
for PREFIX in common shared; do
    if ! MC_CONFIG_DIR="$TMP" "$MC" stat "_abc/%s/$PREFIX/.keep" &>/dev/null; then
        echo -n "" | MC_CONFIG_DIR="$TMP" "$MC" pipe "_abc/%s/$PREFIX/.keep" --quiet
        echo "  created s3://%s/$PREFIX/.keep"
    else
        echo "  s3://%s/$PREFIX/ already exists"
    fi
done
echo "ok"
`, accessKey, secretKey, endpoint, accessKey, secretKey, bucket, bucket, bucket, bucket)

	out, err := wbinternal.RunOnNodePublic(node, "bash", "-c", script)
	if err != nil {
		return fmt.Errorf("ensure group prefixes: %w\n%s", err, out)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  group prefixes: ok\n%s\n", strings.TrimSpace(out))
	return nil
}

// geesefsServiceName returns the systemd service name for a group's geesefs mount.
func geesefsServiceName(group string) string {
	return "abc-geesefs-" + group + ".service"
}

// geesefsMountPoint returns the FUSE mount point for a group on the node.
func geesefsMountPoint(group string) string {
	return "/mnt/abc-group-" + group
}

// ensureGeesefsService writes, enables, and starts the geesefs systemd service.
// Idempotent — reloads systemd and restarts if the service file changed.
func ensureGeesefsService(cmd *cobra.Command, node wbinternal.NodeSSH, group, bucket, endpoint, accessKey, secretKey string, dryRun bool) error {
	svcName := geesefsServiceName(group)
	mountPoint := geesefsMountPoint(group)

	svcContent := fmt.Sprintf(`[Unit]
Description=geesefs FUSE mount — bucket %s — abc-cluster workbench
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=AWS_ACCESS_KEY_ID=%s
Environment=AWS_SECRET_ACCESS_KEY=%s
ExecStartPre=/bin/mkdir -p %s
ExecStart=/usr/local/bin/geesefs \
    --endpoint-url %s \
    --memory-limit 256 \
    --stat-cache-ttl 10s \
    -o allow_other \
    -f \
    %s %s
ExecStop=/bin/fusermount3 -u %s
Restart=on-failure
RestartSec=15

[Install]
WantedBy=multi-user.target
`,
		bucket,
		accessKey, secretKey,
		mountPoint,
		endpoint,
		bucket, mountPoint,
		mountPoint,
	)

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(),
			"  [dry-run] would write /etc/systemd/system/%s and start it\n", svcName,
		)
		return nil
	}

	script := fmt.Sprintf(`set -euo pipefail
SVC_FILE=/etc/systemd/system/%s
NEW_CONTENT=%q

# Write only if content changed (avoid unnecessary restarts).
if [ "$(cat "$SVC_FILE" 2>/dev/null)" != "$NEW_CONTENT" ]; then
    printf '%%s' "$NEW_CONTENT" > "$SVC_FILE"
    systemctl daemon-reload
    echo "service file updated"
fi

# Enable and start (or restart if already running and file changed).
systemctl enable --now %s
if ! systemctl is-active --quiet %s; then
    systemctl restart %s
fi
systemctl is-active --quiet %s && echo "running" || { echo "FAILED to start %s"; exit 1; }
`,
		svcName, svcContent,
		svcName,
		svcName,
		svcName,
		svcName, svcName,
	)

	out, err := wbinternal.RunOnNodePublic(node, "sudo", "bash", "-c", script)
	if err != nil {
		return fmt.Errorf("geesefs service %s: %w\n%s", svcName, err, out)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  geesefs service %s: ok\n", svcName)
	return nil
}

// provisionSlotBindMounts adds fstab entries for ~/group-common and ~/group-shared
// for one slot and mounts them. Idempotent — checks for the abc-workbench marker
// before appending.
func provisionSlotBindMounts(cmd *cobra.Command, node wbinternal.NodeSSH, group, slot string, dryRun bool) error {
	mountPoint := geesefsMountPoint(group)
	homeDir := "/data/workbench/" + slot + "/home"
	svcName := geesefsServiceName(group)
	marker := fmt.Sprintf("# abc-workbench-group-mount: su-%s slot %s", group, slot)

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "  [dry-run] %s: would add bind mounts for group %s\n", slot, group)
		return nil
	}

	script := fmt.Sprintf(`set -euo pipefail
MARKER=%q
HOME_DIR=%q
MOUNT_POINT=%q
SVC=%q

# Create target mount dirs in the slot home.
mkdir -p "$HOME_DIR/group-common" "$HOME_DIR/group-shared"

# Add fstab entries if marker not already present.
if ! grep -qF "$MARKER" /etc/fstab; then
    cat >> /etc/fstab << FSTAB
$MARKER
${MOUNT_POINT}/common ${HOME_DIR}/group-common none bind,ro,nofail,x-systemd.requires=${SVC} 0 0
${MOUNT_POINT}/shared ${HOME_DIR}/group-shared none bind,nofail,x-systemd.requires=${SVC} 0 0
FSTAB
fi

# Mount now if the geesefs service is running and dirs aren't already mounted.
if systemctl is-active --quiet "$SVC"; then
    mountpoint -q "$HOME_DIR/group-common" || mount "$HOME_DIR/group-common"
    mountpoint -q "$HOME_DIR/group-shared" || mount "$HOME_DIR/group-shared"
fi

echo "ok"
`,
		marker, homeDir, mountPoint, svcName,
	)

	out, err := wbinternal.RunOnNodePublic(node, "sudo", "bash", "-c", script)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	if !strings.Contains(out, "ok") {
		return fmt.Errorf("unexpected output: %s", out)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  %s: ok\n", slot)
	return nil
}

// resolveMinioCredsForGeesefs extracts the MinIO endpoint, access key, and
// secret key from admin.services.minio in the active context. Returns an
// error with a helpful message if any required field is missing.
func resolveMinioCredsForGeesefs(actx config.Context) (endpoint, accessKey, secretKey string, err error) {
	svc := actx.Admin.Services.MinIO
	if svc == nil {
		return "", "", "", fmt.Errorf(
			"admin.services.minio not configured in active context\n" +
				"  Run: abc cluster capabilities sync",
		)
	}

	endpoint = strings.TrimSpace(svc.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(svc.HTTP)
	}

	// Prefer access_key/secret_key; fall back to user/password (legacy abc-nodes fields).
	accessKey = strings.TrimSpace(svc.AccessKey)
	if accessKey == "" {
		accessKey = strings.TrimSpace(svc.User)
	}
	secretKey = strings.TrimSpace(svc.SecretKey)
	if secretKey == "" {
		secretKey = strings.TrimSpace(svc.Password)
	}

	// Also check cred_source.local (written by config sync on abc-nodes clusters).
	if svc.CredSource != nil {
		local := svc.CredSource.Local
		if v := strings.TrimSpace(local["endpoint"]); v != "" && endpoint == "" {
			endpoint = v
		}
		if v := strings.TrimSpace(local["access_key"]); v != "" && accessKey == "" {
			accessKey = v
		}
		if v := strings.TrimSpace(local["secret_key"]); v != "" && secretKey == "" {
			secretKey = v
		}
	}

	if endpoint == "" {
		return "", "", "", fmt.Errorf(
			"admin.services.minio.endpoint not set\n" +
				"  Run: abc config set contexts.<name>.admin.services.minio.endpoint http://<host>:9000",
		)
	}
	if accessKey == "" || secretKey == "" {
		return "", "", "", fmt.Errorf(
			"admin.services.minio access_key / secret_key not set\n" +
				"  Run: abc config set contexts.<name>.admin.services.minio.access_key <key>",
		)
	}
	return endpoint, accessKey, secretKey, nil
}

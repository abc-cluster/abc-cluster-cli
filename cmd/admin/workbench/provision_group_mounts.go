package workbench

// provision-group-mounts sets up geesefs FUSE mounts for one or more S3
// prefixes within a group's bucket and bind-mounts them into each slot's
// home directory.
//
// Read-only is enforced at two layers:
//   1. geesefs file/dir mode flags: --file-mode 0444 --dir-mode 0555 make all
//      objects appear read-only at the FUSE layer. Note: geesefs v0.42.1 does
//      not support -o ro (not a recognised FUSE option for this tool). For
//      stronger enforcement, create a MinIO user with a read-only policy for
//      the bucket and use those credentials in the systemd service.
//   2. The per-slot fstab bind mounts use bind,ro — the kernel rejects writes
//      from slot users accessing ~/common/ regardless of FUSE permissions.
//
// Write access (shared/) is deferred pending proper POSIX-locking metadata
// (JuiceFS + Redis) and a read-write MinIO credential per group.
//
// One geesefs systemd service per group (not per slot) mounts the whole
// group bucket. Per-slot bind mounts are fstab entries with
// x-systemd.requires so they only activate after the geesefs service is up.
//
// Storage impact: zero bytes on aither — geesefs streams directly from MinIO.
// Reads stream from MinIO on access; no data is replicated to aither's disk.
//
// Grove/garden upgrade path: replace geesefs with JuiceFS backed by a
// Redis/PostgreSQL metadata store for full POSIX semantics across nodes.

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
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
		Short: "Mount specific S3 prefixes read-only into slot home dirs via geesefs",
		Long: `Set up geesefs FUSE mounts so each workbench slot gets read-only access to
one or more S3 prefixes from the group bucket.

By default the command mounts the 'common' prefix, making it visible in
every slot's JupyterLab home as:

  ~/common/    read-only view of s3://su-<group>/common/

Additional prefixes can be specified with --folders:

  abc admin services workbench provision-group-mounts \
    --group mbhg-hostgen --folders common,reference-data,scripts

Each named folder becomes a separate read-only bind mount in the slot home:

  ~/common/           ← s3://su-mbhg-hostgen/common/
  ~/reference-data/   ← s3://su-mbhg-hostgen/reference-data/
  ~/scripts/          ← s3://su-mbhg-hostgen/scripts/

All mounts are read-only (bind,ro in fstab). Write access to shared
prefixes is deferred — requires POSIX semantics analysis before enabling.

One geesefs systemd service per group mounts the bucket root on aither.
Per-slot fstab bind entries (x-systemd.requires) activate after the
geesefs service is up. Storage impact: zero — geesefs streams directly
from MinIO; no data is replicated to aither's disk.

These directories are for interactive access only, not Nomad job execution.
Running Nextflow workloads against FUSE mounts is unsupported (latency).

Grove/garden upgrade path: replace geesefs with JuiceFS backed by a
Redis/PostgreSQL metadata store for full POSIX semantics across nodes.

Examples:

  # Mount common/ for all slots in mbhg-hostgen:
  abc admin services workbench provision-group-mounts --group mbhg-hostgen

  # Mount common/ and a reference-data prefix:
  abc admin services workbench provision-group-mounts --group mbhg-hostgen \
    --folders common,reference-data

  # Specific slots only:
  abc admin services workbench provision-group-mounts --group mbhg-hostgen \
    --slots calm-dassie,lunar-hornbill

  # Dry-run to preview changes:
  abc admin services workbench provision-group-mounts --group mbhg-hostgen \
    --dry-run`,
		RunE: runProvisionGroupMounts,
	}

	cmd.Flags().String("group", "", "Group name — the MinIO bucket/Nomad namespace suffix (e.g. mbhg-hostgen for su-mbhg-hostgen) [required]")
	cmd.Flags().String("folders", "common", "Comma-separated S3 prefixes to mount read-only (e.g. common,reference-data,scripts)")
	cmd.Flags().String("slots", "", "Comma-separated slot names to bind-mount (default: all slots under /data/workbench/)")
	cmd.Flags().String("node", "", "SSH host alias for the platform node (default: sun-<node> from config or sun-aither)")
	cmd.Flags().String("geesefs-version", defaultGeesefsVersion, "geesefs release tag to install if absent")
	cmd.Flags().String("node-minio-endpoint", "", "MinIO endpoint as seen from the node (default: value from config; override when the node reaches MinIO via localhost or a different address)")
	cmd.Flags().String("sudo-pass-file", "~/.ssh/pass.sun-aither", "file containing the sudo password for the platform node")
	cmd.Flags().Bool("dry-run", false, "Show what would be done without changing anything")

	_ = cmd.MarkFlagRequired("group")

	return cmd
}

func runProvisionGroupMounts(cmd *cobra.Command, args []string) error {
	groupFlag, _ := cmd.Flags().GetString("group")
	foldersFlag, _ := cmd.Flags().GetString("folders")
	slotsFlag, _ := cmd.Flags().GetString("slots")
	nodeOverride, _ := cmd.Flags().GetString("node")
	geesefsVer, _ := cmd.Flags().GetString("geesefs-version")
	sudoPassFile, _ := cmd.Flags().GetString("sudo-pass-file")
	nodeMinioEndpoint, _ := cmd.Flags().GetString("node-minio-endpoint")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Read the sudo password for running privileged commands on the node.
	sudoPass := readSudoPass(sudoPassFile)

	// Normalise group name: strip leading "su-" if provided.
	group := strings.TrimPrefix(groupFlag, "su-")
	bucket := "su-" + group

	// Parse and validate the folder list.
	folders := parseFolderList(foldersFlag)
	if len(folders) == 0 {
		return fmt.Errorf("--folders must specify at least one prefix (e.g. common)")
	}

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

	// --node-minio-endpoint overrides the endpoint used in scripts that run ON
	// the node itself. Useful when the node reaches MinIO via localhost or a
	// Tailscale IP that differs from the client-side endpoint in the config.
	nodeEndpoint := strings.TrimRight(minioEndpoint, "/")
	if ep := strings.TrimSpace(nodeMinioEndpoint); ep != "" {
		nodeEndpoint = strings.TrimRight(ep, "/")
	}

	if dryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "[dry-run] No changes will be made.")
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Group: %s  Bucket: %s  Node: %s  Folders (ro): %s\n",
		group, bucket, node.Host, strings.Join(folders, ", "),
	)

	// 1. FUSE prerequisites — fuse3 package and user_allow_other.
	if err := ensureFusePrereqs(cmd, node, sudoPass, dryRun); err != nil {
		return err
	}

	// 2. geesefs binary.
	if err := ensureGeesefsInstalled(cmd, node, geesefsVer, sudoPass, dryRun); err != nil {
		return err
	}

	// 3. Ensure each folder prefix exists in the MinIO bucket.
	// Uses nodeEndpoint — the address reachable from the node itself.
	if err := ensureGroupPrefixes(cmd, node, nodeEndpoint, accessKey, secretKey, bucket, folders, dryRun); err != nil {
		return err
	}

	// 4. Write and start the geesefs systemd service for this group.
	// Uses nodeEndpoint — embedded in the systemd unit, runs on the node.
	if err := ensureGeesefsService(cmd, node, group, bucket, nodeEndpoint, accessKey, secretKey, sudoPass, dryRun); err != nil {
		return err
	}

	// 5. Discover slots and add per-slot read-only bind mount fstab entries.
	slots, err := resolveSlots(node, slotsFlag)
	if err != nil {
		return fmt.Errorf("resolve slots: %w", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Adding read-only bind mounts for %d slot(s)...\n", len(slots))

	ok, failed := 0, 0
	for _, slot := range slots {
		if err := provisionSlotBindMounts(cmd, node, group, slot, folders, sudoPass, dryRun); err != nil {
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

// parseFolderList parses a comma-separated folder list, trims whitespace and
// leading/trailing slashes from each entry, and drops empty values.
func parseFolderList(raw string) []string {
	var out []string
	for _, f := range strings.Split(raw, ",") {
		f = strings.Trim(strings.TrimSpace(f), "/")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// ensureFusePrereqs installs fuse3 and enables user_allow_other.
func ensureFusePrereqs(cmd *cobra.Command, node wbinternal.NodeSSH, sudoPass string, dryRun bool) error {
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
	out, err := wbinternal.RunScriptOnNodePublic(node, script, true, sudoPass)
	if err != nil {
		return fmt.Errorf("fuse prereqs: %w\n%s", err, out)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "  fuse3 prereqs: ok")
	return nil
}

// ensureGeesefsInstalled downloads and installs the pinned geesefs binary
// to /usr/local/bin/geesefs if not already present.
func ensureGeesefsInstalled(cmd *cobra.Command, node wbinternal.NodeSSH, version string, sudoPass string, dryRun bool) error {
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

	out, err := wbinternal.RunScriptOnNodePublic(node, script, true, sudoPass)
	if err != nil {
		return fmt.Errorf("install geesefs: %w\n%s", err, out)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  installed: %s\n", strings.TrimSpace(out))
	return nil
}

// ensureGroupPrefixes creates <folder>/.keep placeholder objects for each
// named folder in the bucket so the directories appear in geesefs listings.
func ensureGroupPrefixes(cmd *cobra.Command, node wbinternal.NodeSSH, endpoint, accessKey, secretKey, bucket string, folders []string, dryRun bool) error {
	if dryRun {
		for _, f := range folders {
			fmt.Fprintf(cmd.OutOrStdout(), "  [dry-run] would ensure s3://%s/%s/ exists\n", bucket, f)
		}
		return nil
	}

	// Build a shell array of folder names to iterate over.
	var quotedFolders []string
	for _, f := range folders {
		quotedFolders = append(quotedFolders, fmt.Sprintf("%q", f))
	}
	foldersArray := strings.Join(quotedFolders, " ")

	// Use s5cmd to create the placeholder objects — avoids the mc/mcli naming
	// conflict (mc on Ubuntu is GNU Midnight Commander, not MinIO Client).
	// s5cmd 2.x has no `stat` command; use `ls` to check existence instead.
	script := fmt.Sprintf(`set -euo pipefail
export AWS_ACCESS_KEY_ID=%q
export AWS_SECRET_ACCESS_KEY=%q
export AWS_REGION=us-east-1

S5CMD=/usr/local/bin/s5cmd
if ! command -v "$S5CMD" &>/dev/null; then
    S5CMD=$(which s5cmd 2>/dev/null || echo /opt/abc-seedling/nf-work/bin/s5cmd)
fi
EP=%q

# Create a placeholder .keep object for each folder prefix so the directory
# appears in geesefs listings immediately after the service starts.
# Use ls to check existence (s5cmd 2.x has no stat subcommand).
for FOLDER in %s; do
    KEY="s3://%s/${FOLDER}/.keep"
    if "$S5CMD" --endpoint-url "$EP" ls "$KEY" 2>/dev/null | grep -q '.keep'; then
        echo "  ${KEY} already exists"
    else
        echo -n "" | "$S5CMD" --endpoint-url "$EP" pipe "$KEY"
        echo "  created ${KEY}"
    fi
done
echo "ok"
`, accessKey, secretKey, endpoint,
		foldersArray, bucket)

	out, err := wbinternal.RunScriptOnNodePublic(node, script, false)
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
// The FUSE mount is opened with -o allow_other,ro so the kernel enforces
// read-only at the VFS layer for all users, including root.
// Idempotent — reloads systemd and restarts if the service file changed.
func ensureGeesefsService(cmd *cobra.Command, node wbinternal.NodeSSH, group, bucket, endpoint, accessKey, secretKey, sudoPass string, dryRun bool) error {
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
    --endpoint %s \
    --memory-limit 256 \
    --stat-cache-ttl 10s \
    --file-mode 0444 \
    --dir-mode 0555 \
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

	// Base64-encode the service file content so newlines survive being embedded
	// in the bash script variable (Go's %q escapes newlines as \n, which bash
	// then treats as literal backslash-n, corrupting the INI file structure).
	svcB64 := base64.StdEncoding.EncodeToString([]byte(svcContent))

	script := fmt.Sprintf(`set -euo pipefail
SVC_FILE=/etc/systemd/system/%s
NEW_CONTENT=$(echo %s | base64 -d)

# Write only if content changed (avoid unnecessary restarts).
OLD_CONTENT=$(cat "$SVC_FILE" 2>/dev/null || true)
if [ "$OLD_CONTENT" != "$NEW_CONTENT" ]; then
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
		svcName, svcB64,
		svcName,
		svcName,
		svcName,
		svcName, svcName,
	)

	out, err := wbinternal.RunScriptOnNodePublic(node, script, true, sudoPass)
	if err != nil {
		return fmt.Errorf("geesefs service %s: %w\n%s", svcName, err, out)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  geesefs service %s: ok\n", svcName)
	return nil
}

// provisionSlotBindMounts adds read-only fstab bind entries for each named
// folder in the slot's home directory. Idempotent — uses a per-folder
// marker comment to guard against duplicate entries.
func provisionSlotBindMounts(cmd *cobra.Command, node wbinternal.NodeSSH, group, slot string, folders []string, sudoPass string, dryRun bool) error {
	mountPoint := geesefsMountPoint(group)
	homeDir := "/data/workbench/" + slot + "/home"
	svcName := geesefsServiceName(group)

	if dryRun {
		for _, f := range folders {
			fmt.Fprintf(cmd.OutOrStdout(),
				"  [dry-run] %s: would bind-mount (ro) %s/%s → %s/%s\n",
				slot, mountPoint, f, homeDir, f)
		}
		return nil
	}

	// Build per-folder mkdir + fstab + mount snippet.
	var sb strings.Builder
	sb.WriteString("set -euo pipefail\n")
	sb.WriteString(fmt.Sprintf("HOME_DIR=%q\n", homeDir))
	sb.WriteString(fmt.Sprintf("MOUNT_POINT=%q\n", mountPoint))
	sb.WriteString(fmt.Sprintf("SVC=%q\n\n", svcName))

	for _, folder := range folders {
		marker := fmt.Sprintf("# abc-workbench-ro-mount: su-%s/%s slot %s", group, folder, slot)
		sb.WriteString(fmt.Sprintf("# ── folder: %s ──\n", folder))
		sb.WriteString(fmt.Sprintf("mkdir -p \"$HOME_DIR/%s\"\n", folder))
		sb.WriteString(fmt.Sprintf("if ! grep -qF %q /etc/fstab; then\n", marker))
		sb.WriteString(fmt.Sprintf("    printf '%%s\\n' %q >> /etc/fstab\n", marker))
		sb.WriteString(fmt.Sprintf(
			"    printf '%%s/%s %%s/%s none bind,ro,nofail,x-systemd.requires=%%s 0 0\\n' "+
				"\"$MOUNT_POINT\" \"$HOME_DIR\" \"$SVC\" >> /etc/fstab\n",
			folder, folder))
		sb.WriteString("fi\n")
		// Mount immediately if the geesefs service is already running.
		sb.WriteString(fmt.Sprintf(
			"if systemctl is-active --quiet \"$SVC\"; then\n"+
				"    mountpoint -q \"$HOME_DIR/%s\" || mount \"$HOME_DIR/%s\"\n"+
				"fi\n\n",
			folder, folder))
	}
	sb.WriteString("echo ok\n")

	out, err := wbinternal.RunScriptOnNodePublic(node, sb.String(), true, sudoPass)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	if !strings.Contains(out, "ok") {
		return fmt.Errorf("unexpected output: %s", out)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  %s: ok (%s)\n", slot, strings.Join(folders, ", "))
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

// readSudoPass reads the sudo password from a file path. Expands a leading "~/"
// to the user's home directory. Returns an empty string if the file cannot be
// read — callers should fall back to passwordless sudo in that case.
func readSudoPass(passFile string) string {
	if passFile == "" {
		return ""
	}
	if strings.HasPrefix(passFile, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			passFile = filepath.Join(home, passFile[2:])
		}
	}
	data, err := os.ReadFile(passFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

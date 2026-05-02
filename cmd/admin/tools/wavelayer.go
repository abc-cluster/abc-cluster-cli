package tools

// wavelayer.go — "abc admin tools wave-layer" command.
//
// Generates a one-off Nomad batch job HCL with all parameters baked in at
// submit time (arch, bucket, endpoint, tool list), registers it, and waits
// for it to complete.  One job per arch — no parameterized parent required.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

const (
	waveLayerDestPrefix = "wave_lite_layers"
	waveLayerTimeout    = 20 * time.Minute
)

// defaultWaveLayerArches is used when the caller doesn't specify any arches.
var defaultWaveLayerArches = []string{"amd64", "arm64"}

func newWaveLayerCmd() *cobra.Command {
	var (
		toolsFilter  string
		wait         bool
		registerOnly bool
		dryRun       bool
		namespace    string
		rustfsBase   string
	)

	cmd := &cobra.Command{
		Use:   "wave-layer [arch...]",
		Short: "Build and store Wave Lite layer tar.gz bundles on the cluster",
		Long: `Submit a one-off Nomad batch job that assembles a Wave Lite layer
tar.gz for each target architecture and uploads it to the cluster object store.

The job downloads tools.toml from the cluster binary store, identifies tools
marked wave_inject = true, downloads the arch-specific binaries, packs them
into usr/local/bin/<tool> inside a tar.gz, and stores the result at:

  abc-reserved/wave_lite_layers/wave-layer-linux-<arch>.tar.gz

PREREQUISITES
  1. Mark tools in tools.toml:
       [tools.samtools]
       wave_inject = true

  2. Push binaries and tools.toml to the cluster:
       abc admin tools push

  3. Store S3 write credentials in a Nomad Variable:
       abc admin services nomad cli -- var put nomad/jobs/abc-nodes-storage \
         access_key_id=<key> secret_access_key=<secret> endpoint=http://rustfs.aither

EXAMPLES
  # Build for both default arches (amd64 and arm64)
  abc admin tools wave-layer

  # One arch only, block until done
  abc admin tools wave-layer amd64 --wait

  # Rebuild only specific tools
  abc admin tools wave-layer --tools=samtools,bwa

  # Preview generated HCL without submitting
  abc admin tools wave-layer --dry-run`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWaveLayer(cmd.Context(), cmd.OutOrStdout(), args,
				toolsFilter, namespace, rustfsBase, wait, registerOnly, dryRun)
		},
	}

	cmd.Flags().StringVar(&toolsFilter, "tools", "",
		"Comma-separated tool names to inject (default: all wave_inject tools)")
	cmd.Flags().BoolVar(&wait, "wait", false,
		"Block until each build job completes")
	cmd.Flags().BoolVar(&registerOnly, "register-only", false,
		"Register the job without waiting for it to start (implies --wait=false)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Print generated HCL without submitting")
	cmd.Flags().StringVar(&namespace, "namespace", "abc-services",
		"Nomad namespace to submit the build job into")
	cmd.Flags().StringVar(&rustfsBase, "rustfs-base", "",
		"Override the RustFS base URL (default: read from active context endpoint)")
	return cmd
}

func runWaveLayer(ctx context.Context, w io.Writer, arches []string,
	toolsFilter, namespace, rustfsBaseFlag string,
	wait, registerOnly, dryRun bool) error {

	if len(arches) == 0 {
		arches = defaultWaveLayerArches
	}

	// ── Load config ───────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()

	nomadAddr := actx.NomadAddr()
	if nomadAddr == "" {
		return fmt.Errorf(
			"no Nomad address configured\n" +
				"  Set: abc config set contexts.<name>.admin.services.nomad.nomad_addr http://<host>:4646",
		)
	}

	// Resolve the RustFS base URL used inside the job script.
	// Flag > context endpoint (trim to base, e.g. http://rustfs.aither) > hard default.
	rustfsBase := rustfsBaseFlag
	if rustfsBase == "" {
		ep := strings.TrimRight(actx.ToolPushEndpoint(), "/")
		// ToolPushEndpoint is the full S3 API endpoint; use it directly.
		rustfsBase = ep
	}
	if rustfsBase == "" {
		rustfsBase = "http://rustfs.aither"
	}

	// ── Load tools.toml for preflight info ───────────────────────────────────
	tcfg, _, err := loadToolsConfig()
	if err != nil {
		return err
	}
	injectTools := tcfg.WaveInjectTools()
	if len(injectTools) == 0 {
		fmt.Fprintln(w, "[wave-layer] warning: no tools have wave_inject = true in tools.toml")
		fmt.Fprintln(w, "            Edit tools.toml, add wave_inject = true, then run: abc admin tools push")
	} else {
		names := make([]string, len(injectTools))
		for i, t := range injectTools {
			names[i] = t.Name
		}
		filter := toolsFilter
		if filter == "" {
			filter = "* (all)"
		}
		fmt.Fprintf(w, "[wave-layer] wave_inject tools: %s  (filter: %s)\n",
			strings.Join(names, ", "), filter)
	}

	bucket := tcfg.Push.Bucket
	srcPrefix := tcfg.Push.Prefix
	fmt.Fprintf(w, "[wave-layer] source:  %s/%s/%s/\n", rustfsBase, bucket, srcPrefix)
	fmt.Fprintf(w, "[wave-layer] dest:    %s/%s/%s/\n", rustfsBase, bucket, waveLayerDestPrefix)
	fmt.Fprintf(w, "[wave-layer] Nomad:   %s  namespace: %s\n", nomadAddr, namespace)
	fmt.Fprintf(w, "[wave-layer] arches:  %s\n\n", strings.Join(arches, ", "))

	nc := utils.NewNomadClient(nomadAddr, actx.NomadToken(), actx.NomadRegion())

	// ── Submit one job per arch ───────────────────────────────────────────────
	type buildJob struct {
		arch  string
		jobID string
	}
	var builds []buildJob

	for _, arch := range arches {
		jobID := "wave-layer-" + arch
		spec := waveLayerSpec{
			JobID:       jobID,
			Namespace:   namespace,
			Arch:        arch,
			RustfsBase:  rustfsBase,
			Bucket:      bucket,
			SrcPrefix:   srcPrefix,
			DestPrefix:  waveLayerDestPrefix,
			ToolsFilter: toolsFilter,
		}
		hcl := generateWaveLayerHCL(spec)

		if dryRun {
			fmt.Fprintf(w, "──────────── HCL for arch=%s ────────────\n", arch)
			fmt.Fprintln(w, hcl)
			continue
		}

		fmt.Fprintf(w, "[wave-layer] submitting job %s ...\n", jobID)
		jobJSON, err := nc.ParseHCL(ctx, hcl)
		if err != nil {
			return fmt.Errorf("parse HCL for arch=%s: %w", arch, err)
		}
		regResp, err := nc.RegisterJob(ctx, jobJSON)
		if err != nil {
			return fmt.Errorf("register %s: %w", jobID, err)
		}
		fmt.Fprintf(w, "  ✓ submitted  eval=%s\n", regResp.EvalID)
		builds = append(builds, buildJob{arch, jobID})
	}

	if dryRun || registerOnly || !wait {
		if !dryRun {
			fmt.Fprintf(w, "\n%d/%d build jobs submitted.\n", len(builds), len(arches))
			if !wait {
				fmt.Fprintln(w, "Track with:  abc job status wave-layer-<arch>")
			}
		}
		return nil
	}

	// ── Wait ──────────────────────────────────────────────────────────────────
	fmt.Fprintln(w)
	failed := 0
	for _, b := range builds {
		fmt.Fprintf(w, "[wave-layer] waiting for %s ...\n", b.jobID)
		if err := waitForBatchJob(ctx, w, nc, b.jobID, namespace, waveLayerTimeout); err != nil {
			fmt.Fprintf(w, "  ✗ %s: %v\n", b.arch, err)
			failed++
		} else {
			fmt.Fprintf(w, "  ✓ %s: complete\n", b.arch)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d/%d builds failed", failed, len(builds))
	}
	fmt.Fprintf(w, "\nAll %d builds complete.\n", len(builds))
	return nil
}

// waitForBatchJob polls until the job reaches a terminal state or timeout.
func waitForBatchJob(ctx context.Context, w io.Writer, nc *utils.NomadClient,
	jobID, namespace string, timeout time.Duration) error {

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		job, err := nc.GetJob(ctx, jobID, namespace)
		if err != nil {
			fmt.Fprintf(w, "    poll error: %v (retrying)\n", err)
			continue
		}
		fmt.Fprintf(w, "    status=%s  %s\n", job.Status, job.StatusDescription)
		if job.Status == "dead" {
			desc := strings.ToLower(job.StatusDescription)
			if strings.Contains(desc, "fail") || strings.Contains(desc, "error") {
				return fmt.Errorf("job %s failed: %s", jobID, job.StatusDescription)
			}
			return nil
		}
	}
}

// ── HCL generator ─────────────────────────────────────────────────────────────

type waveLayerSpec struct {
	JobID       string
	Namespace   string
	Arch        string
	RustfsBase  string
	Bucket      string
	SrcPrefix   string
	DestPrefix  string
	ToolsFilter string // "" means all wave_inject tools
}

// generateWaveLayerHCL returns a self-contained Nomad batch job HCL string
// with all operator-controlled values baked in as literals.  The job:
//   - fetches s5cmd via Nomad artifact stanza (public-read bucket, no auth)
//   - reads S3 write credentials from Nomad Variable nomad/jobs/abc-nodes-storage
//   - downloads tools.toml and arch binaries, packs and uploads the wave layer
func generateWaveLayerHCL(s waveLayerSpec) string {
	// $${VAR} in Go string → HCL emits ${VAR} → bash expands it.
	// $VAR (no braces) passes through HCL and bash unchanged.
	// Consul Template {{ env "K" }} is rendered by Nomad before the task starts.
	toolsFilter := s.ToolsFilter
	if toolsFilter == "" {
		toolsFilter = "*"
	}
	s5cmdURL := fmt.Sprintf("%s/%s/%s/s5cmd-linux-%s",
		s.RustfsBase, s.Bucket, s.SrcPrefix, s.Arch)
	tomlURL := fmt.Sprintf("%s/%s/%s/tools.toml",
		s.RustfsBase, s.Bucket, s.SrcPrefix)

	return fmt.Sprintf(`
job %q {
  namespace   = %q
  type        = "batch"
  region      = "global"
  datacenters = ["dc1", "default"]
  priority    = 40

  meta {
    abc_cluster_type   = "abc-nodes"
    managed_by         = "abc-cluster-cli"
    wave_layer_arch    = %q
    wave_layer_tools   = %q
  }

  group "build" {
    count = 1

    restart {
      attempts = 1
      delay    = "10s"
      mode     = "fail"
    }

    reschedule {
      attempts  = 0
      unlimited = false
    }

    task "build-layer" {
      driver = "raw_exec"

      config {
        command = "/bin/bash"
        args    = ["$${NOMAD_TASK_DIR}/build.sh"]
      }

      # s5cmd fetched directly by Nomad — no prestart task needed.
      # The abc-reserved bucket is public-read; no credentials required for download.
      artifact {
        source      = %q
        destination = "local/s5cmd"
        mode        = "file"
      }

      # S3 write credentials injected via Consul Template from a Nomad Variable.
      # Store once:
      #   abc admin services nomad cli -- var put nomad/jobs/abc-nodes-storage \
      #     access_key_id=<key> secret_access_key=<secret> endpoint=http://rustfs.aither
      template {
        data = <<-CREDS
{{- with nomadVar "nomad/jobs/abc-nodes-storage" -}}
AWS_ACCESS_KEY_ID={{ .access_key_id }}
AWS_SECRET_ACCESS_KEY={{ .secret_access_key }}
S3_ENDPOINT={{ .endpoint }}
{{- end }}
CREDS
        destination = "secrets/s3.env"
        env         = false
      }

      template {
        data        = <<-'SCRIPT'
#!/bin/bash
# wave-layer build script — values baked in by abc CLI at submit time.
set -euo pipefail

# ── Config (baked in) ─────────────────────────────────────────────
ARCH=%q
BUCKET=%q
SRC_PREFIX=%q
DEST_PREFIX=%q
TOOLS_FILTER=%q
TOML_URL=%q

# ── S3 credentials from Nomad Variable ───────────────────────────
# shellcheck disable=SC1091
source "$NOMAD_SECRETS_DIR/s3.env"
export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION="us-east-1"

# ── s5cmd (fetched as Nomad artifact) ────────────────────────────
S5="$NOMAD_TASK_DIR/s5cmd"
chmod +x "$S5"
echo "[wave-layer] s5cmd: $($S5 version)"

WORK="$NOMAD_TASK_DIR/work"
STAGE="$WORK/stage/usr/local/bin"
mkdir -p "$STAGE"

# ── Download tools.toml ───────────────────────────────────────────
TOML="$WORK/tools.toml"
echo "[wave-layer] downloading tools.toml ..."
curl -fsSL --connect-timeout 15 -o "$TOML" "$TOML_URL"

# ── Parse wave_inject tools ───────────────────────────────────────
# Pure-bash TOML parser for the simple [tools.<name>] / wave_inject pattern.
parse_wave_inject_tools() {
  local file="$1"
  local current_tool="" wave_inject_found=false

  while IFS= read -r line; do
    trimmed=$(echo "$line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')

    if [[ "$trimmed" =~ ^\[tools\.([^]]+)\]$ ]]; then
      [[ -n "$current_tool" && "$wave_inject_found" == "true" ]] && echo "$current_tool"
      current_tool="${BASH_REMATCH[1]}"
      wave_inject_found=false
      continue
    fi

    if [[ "$trimmed" =~ ^\[[^]]+\]$ ]]; then
      [[ -n "$current_tool" && "$wave_inject_found" == "true" ]] && echo "$current_tool"
      current_tool=""
      wave_inject_found=false
      continue
    fi

    [[ -n "$current_tool" && "$trimmed" =~ ^wave_inject[[:space:]]*=[[:space:]]*true ]] \
      && wave_inject_found=true

    [[ -n "$current_tool" && "$trimmed" =~ ^disabled[[:space:]]*=[[:space:]]*true ]] \
      && current_tool="" && wave_inject_found=false
  done < "$file"

  [[ -n "$current_tool" && "$wave_inject_found" == "true" ]] && echo "$current_tool"
}

mapfile -t ALL_INJECT < <(parse_wave_inject_tools "$TOML")

if [[ ${#ALL_INJECT[@]} -eq 0 ]]; then
  echo "[wave-layer] no wave_inject tools found in tools.toml — nothing to do."
  exit 0
fi

# ── Apply tools filter ────────────────────────────────────────────
if [[ "$TOOLS_FILTER" == "*" || -z "$TOOLS_FILTER" ]]; then
  INJECT_TOOLS=("${ALL_INJECT[@]}")
else
  IFS=',' read -ra FILTER_LIST <<< "$TOOLS_FILTER"
  INJECT_TOOLS=()
  for tool in "${ALL_INJECT[@]}"; do
    for wanted in "${FILTER_LIST[@]}"; do
      [[ "$tool" == "$wanted" ]] && INJECT_TOOLS+=("$tool") && break
    done
  done
fi

if [[ ${#INJECT_TOOLS[@]} -eq 0 ]]; then
  echo "[wave-layer] no tools matched filter '$TOOLS_FILTER'" >&2
  exit 1
fi

echo "[wave-layer] tools to inject: ${INJECT_TOOLS[*]}"

# ── Download binaries ─────────────────────────────────────────────
DOWNLOADED=()
for tool in "${INJECT_TOOLS[@]}"; do
  BIN_URL="$S3_ENDPOINT/$BUCKET/$SRC_PREFIX/$tool-linux-$ARCH"
  DEST="$STAGE/$tool"
  echo "[wave-layer] downloading $tool from $BIN_URL ..."
  if curl -fsSL --connect-timeout 30 -o "$DEST" "$BIN_URL"; then
    chmod 0755 "$DEST"
    SIZE=$(stat -c%%s "$DEST" 2>/dev/null || echo "?")
    echo "[wave-layer]   ✓ $tool  (${SIZE} bytes)"
    DOWNLOADED+=("$tool")
  else
    echo "[wave-layer]   ✗ $tool: download failed" >&2
  fi
done

if [[ ${#DOWNLOADED[@]} -eq 0 ]]; then
  echo "[wave-layer] no binaries downloaded — aborting." >&2
  exit 1
fi

# ── Pack tar.gz ───────────────────────────────────────────────────
LAYER_NAME="wave-layer-linux-$ARCH.tar.gz"
LAYER_PATH="$WORK/$LAYER_NAME"
echo "[wave-layer] packing $LAYER_NAME ..."
tar -czf "$LAYER_PATH" -C "$WORK/stage" usr/
echo "[wave-layer] packed $(stat -c%%s "$LAYER_PATH") bytes"

# ── Upload ────────────────────────────────────────────────────────
REMOTE_URI="s3://$BUCKET/$DEST_PREFIX/$LAYER_NAME"
echo "[wave-layer] uploading to $REMOTE_URI ..."
"$S5" --endpoint-url "$S3_ENDPOINT" cp "$LAYER_PATH" "$REMOTE_URI"
echo "[wave-layer] ✓ done — ${DOWNLOADED[*]} → $REMOTE_URI"
SCRIPT
        destination = "local/build.sh"
        perms       = "0755"
      }

      resources {
        cpu    = 500
        memory = 256
      }
    }
  }
}
`,
		s.JobID,
		s.Namespace,
		s.Arch,
		toolsFilter,
		s5cmdURL,
		s.Arch,
		s.Bucket,
		s.SrcPrefix,
		s.DestPrefix,
		toolsFilter,
		tomlURL,
	)
}

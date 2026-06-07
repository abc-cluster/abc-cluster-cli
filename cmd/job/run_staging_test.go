package job

// Tests for the job data-staging cluster seam (spec
// abc-job-data-staging-and-run-tags, acceptance A-D2-unit): resolveStaging must
// populate a non-empty StageEnv (MinIO creds + S3 endpoint) when the active
// context carries MinIO credentials, and leave it empty otherwise.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// stagingTestCmd builds a cobra command carrying the --in/--out flags
// resolveStaging reads, set to the given values.
func stagingTestCmd(t *testing.T, ins, outs []string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().StringArray("in", nil, "")
	cmd.Flags().StringArray("out", nil, "")
	for _, in := range ins {
		_ = cmd.Flags().Set("in", in)
	}
	for _, o := range outs {
		_ = cmd.Flags().Set("out", o)
	}
	return cmd
}

// writeStagingProject creates a temp project root (marked with
// .abc-project.toml) containing a local input file, and returns the root and the
// script path inside it.
func writeStagingProject(t *testing.T) (root, scriptPath string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".abc-project.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "in.csv"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath = filepath.Join(root, "train.py")
	if err := os.WriteFile(scriptPath, []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, scriptPath
}

func setStagingConfig(t *testing.T, yaml string) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABC_CLI_CONFIG_FILE", cfgPath)
	// Ensure common-mount env detection doesn't interfere.
	t.Setenv("ABC_STAGE_COMMON_PATH", "")
}

const stagingConfigWithMinio = `version: "1"
active_context: lab
contexts:
  lab:
    cluster_type: abc-cluster
    auth:
      whoami: calm_dassie
    admin:
      whoami: calm_dassie
      services:
        minio:
          endpoint: https://minio.seedling.example:9000
          access_key: AKIA_TEST
          secret_key: SECRET_TEST
`

const stagingConfigNoMinio = `version: "1"
active_context: lab
contexts:
  lab:
    cluster_type: abc-cluster
    auth:
      whoami: calm_dassie
    admin:
      whoami: calm_dassie
`

func TestResolveStaging_PopulatesStageEnvWithCreds(t *testing.T) {
	setStagingConfig(t, stagingConfigWithMinio)
	root, scriptPath := writeStagingProject(t)

	cmd := stagingTestCmd(t, []string{filepath.Join(root, "data", "in.csv")}, []string{"results/"})
	spec := &jobSpec{Namespace: "su-mbhg-hostgen"}

	plan, err := resolveStaging(cmd, scriptPath, "r-123", spec)
	if err != nil {
		t.Fatalf("resolveStaging: %v", err)
	}
	if plan == nil {
		t.Fatal("expected a non-nil staging plan when --in/--out are set")
	}

	if len(spec.StageEnv) == 0 {
		t.Fatal("expected non-empty StageEnv when the context carries MinIO creds")
	}
	wants := map[string]string{
		"AWS_ACCESS_KEY_ID":     "AKIA_TEST",
		"AWS_SECRET_ACCESS_KEY": "SECRET_TEST",
		"AWS_ENDPOINT_URL":      "https://minio.seedling.example:9000",
	}
	for k, want := range wants {
		if got := spec.StageEnv[k]; got != want {
			t.Errorf("StageEnv[%q] = %q, want %q", k, got, want)
		}
	}
	if spec.StageEnv["AWS_REGION"] == "" {
		t.Error("StageEnv[AWS_REGION] should be set")
	}
	// HTTPS endpoint → private-CA → skip TLS verification (mirrors pipeline path).
	if !spec.StageS5cmdSkipTLS {
		t.Error("expected StageS5cmdSkipTLS=true for an HTTPS (private-CA) endpoint")
	}
}

func TestResolveStaging_EmptyStageEnvWithoutCreds(t *testing.T) {
	setStagingConfig(t, stagingConfigNoMinio)
	root, scriptPath := writeStagingProject(t)

	cmd := stagingTestCmd(t, []string{filepath.Join(root, "data", "in.csv")}, []string{"results/"})
	spec := &jobSpec{Namespace: "su-mbhg-hostgen"}

	plan, err := resolveStaging(cmd, scriptPath, "r-123", spec)
	if err != nil {
		t.Fatalf("resolveStaging: %v", err)
	}
	if plan == nil {
		t.Fatal("expected a non-nil staging plan when --in/--out are set")
	}
	if len(spec.StageEnv) != 0 {
		t.Errorf("expected empty StageEnv when the context has no MinIO creds, got: %v", spec.StageEnv)
	}
	if spec.StageS5cmdSkipTLS {
		t.Error("expected StageS5cmdSkipTLS=false when no creds/endpoint resolved")
	}
}

// TestResolveStaging_RunPrefixUsesPathSegmentSlug guards FIX 2: the staged
// run prefix must use the full path-safe whoami segment (WhoamiPathSegment,
// e.g. "calm-dassie") that upload/pipeline use — NOT the abbreviated 5-char
// job-id slug (ActiveWhoamiSlug, e.g. "calda"). A divergence here would scatter
// staged data to user/<short>/… instead of the member's real home
// user/<full>/…, breaking discoverability and the upload <-> stage contract.
func TestResolveStaging_RunPrefixUsesPathSegmentSlug(t *testing.T) {
	setStagingConfig(t, stagingConfigWithMinio) // whoami: calm_dassie
	root, scriptPath := writeStagingProject(t)

	cmd := stagingTestCmd(t, []string{filepath.Join(root, "data", "in.csv")}, []string{"results/"})
	spec := &jobSpec{Namespace: "su-mbhg-hostgen"}

	plan, err := resolveStaging(cmd, scriptPath, "r-123", spec)
	if err != nil {
		t.Fatalf("resolveStaging: %v", err)
	}

	pathSeg := utils.WhoamiPathSegment("calm_dassie") // "calm-dassie"
	shortSlug := utils.WhoamiSlug("calm_dassie")       // "calda"
	if pathSeg == "" || shortSlug == "" || pathSeg == shortSlug {
		t.Fatalf("test precondition broken: pathSeg=%q shortSlug=%q", pathSeg, shortSlug)
	}

	wantSegment := "/user/" + pathSeg + "/"
	if !strings.Contains(plan.RunPrefix, wantSegment) {
		t.Errorf("RunPrefix = %q, want it to contain %q (WhoamiPathSegment slug)", plan.RunPrefix, wantSegment)
	}
	if strings.Contains(plan.RunPrefix, "/user/"+shortSlug+"/") {
		t.Errorf("RunPrefix = %q must NOT use the abbreviated ActiveWhoamiSlug %q", plan.RunPrefix, shortSlug)
	}
}

// TestResolveStaging_UploadSetEqualsLocalUploads verifies the upload-before-
// submit set (Plan.LocalUploads) covers exactly the local-only inputs for a
// mixed local / in-MinIO --in set (acceptance A-D2-unit, cmd/job half).
func TestResolveStaging_UploadSetEqualsLocalUploads(t *testing.T) {
	setStagingConfig(t, stagingConfigWithMinio)
	root, scriptPath := writeStagingProject(t)

	localIn := filepath.Join(root, "data", "in.csv")
	minioIn := "s3://su-mbhg-hostgen/user/calm_dassie/proj/inputs/ref.csv"

	cmd := stagingTestCmd(t, []string{localIn, minioIn}, []string{"results/"})
	spec := &jobSpec{Namespace: "su-mbhg-hostgen"}

	plan, err := resolveStaging(cmd, scriptPath, "r-456", spec)
	if err != nil {
		t.Fatalf("resolveStaging: %v", err)
	}

	ups := plan.LocalUploads()
	if len(ups) != 1 {
		t.Fatalf("expected exactly 1 local upload (the local-only input), got %d: %v", len(ups), ups)
	}
	// The local upload's source must be the local file; the in-MinIO input must
	// not be uploaded.
	if ups[0][0] != localIn {
		t.Errorf("upload src = %q, want %q", ups[0][0], localIn)
	}
	wantDest := plan.RunPrefix + "/inputs/data/in.csv"
	if ups[0][1] != wantDest {
		t.Errorf("upload dest = %q, want %q", ups[0][1], wantDest)
	}
}

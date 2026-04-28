package module_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	abcmodule "github.com/abc-cluster/abc-cluster-cli/cmd/module"
	"github.com/spf13/cobra"
)

func executeModuleCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	root := &cobra.Command{Use: "abc"}
	root.AddCommand(abcmodule.NewCmd())
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"module", "run"}, args...))
	_, err := root.ExecuteC()
	return buf.String(), err
}

func TestModuleRunDryRun_EmitsGenerateAndRunTasks(t *testing.T) {
	out, err := executeModuleCmd(
		t,
		"nf-core/fastqc",
		"--dry-run",
		"--github-token", "ghp_test_token",
		"--nomad-token", "nomad_test_token",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}

	checks := []string{
		`job "module-nf-core-fastqc"`,
		`task "generate"`,
		`hook    = "prestart"`,
		`task "nextflow"`,
		`nf-core/fastqc`,
		`nextflow run main.nf`,
		`-profile "test"`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in dry-run output\n%s", want, out)
		}
	}
}

func TestModuleRunDryRun_TestModeForcesTestProfileAndEnv(t *testing.T) {
	out, err := executeModuleCmd(
		t,
		"nf-core/seqkit/stats",
		"--test",
		"--dry-run",
		"--github-token", "ghp_test_token",
		"--nomad-token", "nomad_test_token",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}

	checks := []string{
		`job "module-nf-core-seqkit-stats"`,
		`ABC_MODULE_TEST_MODE = "1"`,
		`-profile "test"`,
		`nf-core/seqkit/stats`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in dry-run output\n%s", want, out)
		}
	}
}

func TestModuleRun_MissingGitHubTokenFails(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	out, err := executeModuleCmd(
		t,
		"nf-core/fastqc",
		"--dry-run",
		"--nomad-token", "nomad_test_token",
	)
	if err == nil {
		t.Fatalf("expected error for missing github token, output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "missing GitHub token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModuleRunHelp_DefaultHidesAdvanced(t *testing.T) {
	out, err := executeModuleCmd(t, "--help")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}

	mustContain := []string{"--params-file", "--config-file", "--profile", "--wait", "--logs", "--dry-run", "--help --advanced"}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in default help, got:\n%s", want, out)
		}
	}

	mustNotContain := []string{"--nf-version", "--cpu", "--memory", "--s3-endpoint", "--github-token", "--module-revision"}
	for _, banned := range mustNotContain {
		if strings.Contains(out, banned) {
			t.Fatalf("default help unexpectedly contains %q:\n%s", banned, out)
		}
	}
}

func TestModuleRunHelp_AdvancedShowsAll(t *testing.T) {
	out, err := executeModuleCmd(t, "--help", "--advanced")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}

	mustContain := []string{
		"--params-file", "--wait",
		"--nf-version", "--nf-plugin-version", "--cpu", "--memory",
		"--s3-endpoint", "--github-token", "--module-revision",
		"--pipeline-gen-repo", "--pipeline-gen-version", "--pipeline-gen-no-run-manifest",
		"--work-dir", "--output-prefix", "--datacenter",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in advanced help, got:\n%s", want, out)
		}
	}
}

func TestModuleRunDryRun_EmbedsProvidedParamsAndConfig(t *testing.T) {
	dir := t.TempDir()
	paramsPath := filepath.Join(dir, "params.yml")
	configPath := filepath.Join(dir, "module.config")
	if err := os.WriteFile(paramsPath, []byte("meta:\n  id: sample\nreads: data.fastq.gz\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("process { withName: FASTQC { cpus = 2 } }\n"), 0600); err != nil {
		t.Fatal(err)
	}

	out, err := executeModuleCmd(
		t,
		"nf-core/fastqc",
		"--dry-run",
		"--github-token", "ghp_test_token",
		"--params-file", paramsPath,
		"--config-file", configPath,
		"--module-revision", "123abc456def",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}

	checks := []string{
		`ABC_MODULE_PARAMS_B64`,
		`ABC_MODULE_CONFIG_B64`,
		`ABC_MODULE_REVISION`,
		`123abc456def`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in dry-run output\n%s", want, out)
		}
	}
}

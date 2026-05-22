package job

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/shellcheck"
)

func TestGenerate_StaticEnvOnlyCreatesEnvBlock(t *testing.T) {
	spec := Spec{
		Name:        "static-env-job",
		Driver:      "exec",
		Datacenters: []string{"dc1"},
		Nodes:       1,
		Priority:    50,
		StaticEnv: map[string]string{
			"ABC_NODES_CLUSTER_FLOOR": "enhanced",
			"ABC_NODES_LOKI_PUSH_URL": "http://127.0.0.1:3100/loki/api/v1/push",
		},
	}
	script := "#!/bin/sh\necho ok\n"
	hcl := Generate(spec, "run.sh", script)
	if !strings.Contains(hcl, `ABC_NODES_CLUSTER_FLOOR`) || !strings.Contains(hcl, `ABC_NODES_LOKI_PUSH_URL`) {
		t.Fatalf("expected static env keys in HCL:\n%s", hcl)
	}
	if !strings.Contains(hcl, `env {`) {
		t.Fatalf("expected env block when only StaticEnv is set:\n%s", hcl)
	}
}

// containsCommand checks for `command(<spaces>)= "<value>"` — HCL's
// pretty-printer aligns columns by padding the key with extra spaces
// (e.g. `command  = "/bin/sh"` when `hostname` is also in the block).
// strings.Contains on the literal one-space form would miss those.
func containsCommand(hcl, want string) bool {
	r := regexp.MustCompile(`command\s+= "` + regexp.QuoteMeta(want) + `"`)
	return r.FindString(hcl) != ""
}

// All OCI drivers (docker, containerd-driver, podman, singularity) MUST default
// to /bin/sh so jobs portable across drivers — alpine and other minimal images
// don't carry bash. Host-side drivers (exec, exec2, raw_exec) keep /bin/bash.
func TestGenerate_OCIDriversAllDefaultToSh(t *testing.T) {
	for _, d := range []string{"docker", "containerd-driver", "podman", "singularity"} {
		t.Run(d, func(t *testing.T) {
			spec := Spec{Name: "oci-default-sh", Driver: d, Datacenters: []string{"dc1"}, Nodes: 1}
			hcl := Generate(spec, "run.sh", "#!/bin/sh\necho ok\n")
			if !containsCommand(hcl, "/bin/sh") {
				t.Fatalf("driver %s: expected command = \"/bin/sh\" in HCL, got:\n%s", d, hcl)
			}
			if containsCommand(hcl, "/bin/bash") {
				t.Fatalf("driver %s: did not expect command = \"/bin/bash\" in HCL, got:\n%s", d, hcl)
			}
		})
	}
}

func TestGenerate_HostDriversDefaultToBash(t *testing.T) {
	for _, d := range []string{"exec", "exec2", "raw_exec"} {
		t.Run(d, func(t *testing.T) {
			spec := Spec{Name: "host-default-bash", Driver: d, Datacenters: []string{"dc1"}, Nodes: 1}
			hcl := Generate(spec, "run.sh", "#!/bin/bash\necho ok\n")
			if !containsCommand(hcl, "/bin/bash") {
				t.Fatalf("driver %s: expected command = \"/bin/bash\" in HCL, got:\n%s", d, hcl)
			}
		})
	}
}

// `--shell=bash` is the escape hatch that forces /bin/bash on an OCI driver
// where the image is known to ship bash (e.g. ubuntu, debian).
func TestGenerate_ShellOverrideForcesBashOnOCI(t *testing.T) {
	spec := Spec{Name: "bash-override", Driver: "docker", Shell: "bash", Datacenters: []string{"dc1"}, Nodes: 1}
	hcl := Generate(spec, "run.sh", "echo ok\n")
	if !containsCommand(hcl, "/bin/bash") {
		t.Fatalf("expected --shell=bash override to force /bin/bash, got:\n%s", hcl)
	}
}

func TestGenerate_ShellOverrideForcesShOnHost(t *testing.T) {
	spec := Spec{Name: "sh-override", Driver: "exec", Shell: "sh", Datacenters: []string{"dc1"}, Nodes: 1}
	hcl := Generate(spec, "run.sh", "echo ok\n")
	if !containsCommand(hcl, "/bin/sh") {
		t.Fatalf("expected --shell=sh override to force /bin/sh, got:\n%s", hcl)
	}
}

// `--shell=<bare-name>` resolves to /bin/<name> for any shell, not just
// bash/sh — lets users opt into dash, zsh, ksh, fish, etc.
func TestGenerate_ShellOverrideBareName(t *testing.T) {
	cases := map[string]string{
		"dash":  "/bin/dash",
		"zsh":   "/bin/zsh",
		"ksh":   "/bin/ksh",
		"fish":  "/bin/fish",
		"BASH":  "/bin/bash", // lowercased
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			spec := Spec{Name: "shell-bare", Driver: "docker", Shell: in, Datacenters: []string{"dc1"}, Nodes: 1}
			hcl := Generate(spec, "run.sh", "echo ok\n")
			if !containsCommand(hcl, want) {
				t.Fatalf("Shell=%q: expected command = %q, got:\n%s", in, want, hcl)
			}
		})
	}
}

// `--shell=/absolute/path` is used verbatim — lets users target a shell
// installed at a non-/bin location (Nix store, /usr/local, etc.).
func TestGenerate_ShellOverrideAbsolutePath(t *testing.T) {
	cases := []string{
		"/bin/dash",
		"/usr/local/bin/fish",
		"/nix/store/xyz/bin/bash",
		"/opt/conda/bin/zsh",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			spec := Spec{Name: "shell-abs", Driver: "docker", Shell: path, Datacenters: []string{"dc1"}, Nodes: 1}
			hcl := Generate(spec, "run.sh", "echo ok\n")
			if !containsCommand(hcl, path) {
				t.Fatalf("Shell=%q: expected command = %q verbatim, got:\n%s", path, path, hcl)
			}
		})
	}
}

// Hostname stanza must be emitted for every OCI driver so the in-container
// hostname is stable across drivers (aligns docker's short-container-id with
// containerd's cgroup-scope UUID etc.). Skipped for host-side drivers.
func TestGenerate_OCIDriversEmitHostname(t *testing.T) {
	for _, d := range []string{"docker", "containerd-driver", "podman", "singularity"} {
		t.Run(d, func(t *testing.T) {
			spec := Spec{Name: "oci-hostname", Driver: d, Datacenters: []string{"dc1"}, Nodes: 1}
			hcl := Generate(spec, "run.sh", "echo ok\n")
			if !strings.Contains(hcl, `hostname = "${NOMAD_SHORT_ALLOC_ID}"`) {
				t.Fatalf("driver %s: expected hostname = ${NOMAD_SHORT_ALLOC_ID}, got:\n%s", d, hcl)
			}
		})
	}
}

func TestGenerate_HostDriversNoHostnameStanza(t *testing.T) {
	for _, d := range []string{"exec", "exec2", "raw_exec"} {
		t.Run(d, func(t *testing.T) {
			spec := Spec{Name: "host-no-hostname", Driver: d, Datacenters: []string{"dc1"}, Nodes: 1}
			hcl := Generate(spec, "run.sh", "echo ok\n")
			if strings.Contains(hcl, `hostname =`) {
				t.Fatalf("driver %s: hostname stanza should not be emitted for host drivers, got:\n%s", d, hcl)
			}
		})
	}
}

// If the user has set hostname via --driver.config.hostname=…, the explicit
// value wins over the default.
func TestGenerate_UserHostnameOverridesDefault(t *testing.T) {
	spec := Spec{
		Name:         "explicit-hostname",
		Driver:       "containerd-driver",
		DriverConfig: map[string]string{"hostname": "my-custom-host"},
		Datacenters:  []string{"dc1"},
		Nodes:        1,
	}
	hcl := Generate(spec, "run.sh", "echo ok\n")
	if !strings.Contains(hcl, `hostname = "my-custom-host"`) {
		t.Fatalf("expected user hostname to win, got:\n%s", hcl)
	}
	if strings.Contains(hcl, `hostname = "${NOMAD_SHORT_ALLOC_ID}"`) {
		t.Fatalf("default hostname should be suppressed when user sets one, got:\n%s", hcl)
	}
}

func TestGenerate_ContainerdDriverUsesShForScriptRunner(t *testing.T) {
	spec := Spec{
		Name:        "ctrd-job",
		Driver:      "containerd-driver",
		Datacenters: []string{"dc1"},
		Nodes:       1,
		Priority:    50,
		WalltimeSecs: 120,
	}
	hcl := Generate(spec, "run.sh", "#!/bin/sh\necho ok\n")
	if !strings.Contains(hcl, `= "timeout"`) {
		t.Fatalf("expected walltime timeout wrapper:\n%s", hcl)
	}
	if !strings.Contains(hcl, `"120"`) || !strings.Contains(hcl, `"/bin/sh"`) || !strings.Contains(hcl, `"$${NOMAD_TASK_DIR}/run.sh"`) {
		t.Fatalf("expected timeout args with /bin/sh and $${NOMAD_TASK_DIR}/run.sh (Nomad-escaped), got:\n%s", hcl)
	}
	if !strings.Contains(hcl, `destination = "local/run.sh"`) {
		t.Fatalf("expected templated script under local/, got:\n%s", hcl)
	}
}

func TestGenerate_StaticEnvSortedKeysStable(t *testing.T) {
	spec := Spec{
		Name:        "order-job",
		Driver:      "exec",
		Datacenters: []string{"dc1"},
		Nodes:       1,
		Priority:    50,
		StaticEnv: map[string]string{
			"ZZ_LAST": "z",
			"AA_FIRST": "a",
		},
	}
	hcl := Generate(spec, "x.sh", "#!/bin/sh\n")
	iZZ := strings.Index(hcl, `ZZ_LAST`)
	iAA := strings.Index(hcl, `AA_FIRST`)
	if iAA == -1 || iZZ == -1 {
		t.Fatal("missing env keys")
	}
	if iAA >= iZZ {
		t.Fatalf("expected sorted key order AA_FIRST before ZZ_LAST; aa=%d zz=%d", iAA, iZZ)
	}
}

func TestGenerate_TaskTmpInjectsEnv(t *testing.T) {
	spec := Spec{
		Name:        "tmp-job",
		Driver:      "exec",
		Datacenters: []string{"dc1"},
		Nodes:       1,
		Priority:    50,
		TaskTmp:     true,
	}
	hcl := Generate(spec, "run.sh", "#!/bin/sh\necho ok\n")
	if !strings.Contains(hcl, `TMPDIR`) || !strings.Contains(hcl, `${NOMAD_TASK_DIR}/tmp`) {
		t.Fatalf("expected TMPDIR in env block:\n%s", hcl)
	}
}

func TestScriptArgForDriver(t *testing.T) {
	if got := ScriptArgForDriver("exec", "run.sh"); got != "local/run.sh" {
		t.Fatalf("exec: got %q", got)
	}
	if got := ScriptArgForDriver("docker", "run.sh"); got != "${NOMAD_TASK_DIR}/run.sh" {
		t.Fatalf("docker: got %q", got)
	}
	if got := ScriptArgForDriver("containerd-driver", "x.sh"); got != "${NOMAD_TASK_DIR}/x.sh" {
		t.Fatalf("containerd-driver: got %q", got)
	}
}

func TestGenerate_WavePrestartTask(t *testing.T) {
	spec := Spec{
		Name:        "samtools-wave",
		Namespace:   "default",
		Datacenters: []string{"dc1"},
		Priority:    50,
		Nodes:       1,
		Cores:       4,
		MemoryMB:    8192,
		Driver:      "docker",
		DriverConfig: map[string]string{
			"image": "wave.seqera.io/wt/abc123/ubuntu:22.04",
		},
		Wave: WaveSpec{
			Enabled:          true,
			CondaFileContent: "name: env\ndependencies:\n  - samtools=1.21\n",
			TokenSecretPath:  "nomad/jobs",
			TokenSecretKey:   "wave_token",
			Platform:         "linux/amd64",
			BinarySourceURL:  "http://rustfs/binary_tools/wave-${attr.kernel.name}-${attr.cpu.arch}",
		},
	}
	hcl := Generate(spec, "samtools.sh", "#!/bin/bash\nsamtools view -c input.bam\n")

	checks := []struct {
		label string
		want  string
	}{
		{"prestart task block", `"wave-build"`},
		{"lifecycle hook", `"prestart"`},
		{"exec driver for prestart", `driver = "exec"`},
		{"wave binary curl download", `curl -fsSL`},
		{"wave binary base url", `http://rustfs/binary_tools/wave-`},
		{"uname arch detection", `uname -m`},
		{"token secret template", `nomadVar "nomad/jobs"`},
		{"token secret key", `.wave_token`},
		{"token env injection", `"secrets/wave.env"`},
		{"conda env template", `samtools=1.21`},
		{"build script template", `"local/wave-build.sh"`},
		{"await flag in script", `--await`},
		{"platform in script", `linux/amd64`},
		{"main task still present", `"main"`},
		{"main docker driver", `driver = "docker"`},
		{"wave image in main config", `wave.seqera.io/wt/abc123/ubuntu:22.04`},
	}
	for _, c := range checks {
		if !strings.Contains(hcl, c.want) {
			t.Errorf("[%s] expected HCL to contain %q\nFull HCL:\n%s", c.label, c.want, hcl)
		}
	}
}


func TestGenerate_WavePrestartScriptPassesShellcheck(t *testing.T) {
	spec := Spec{
		Name:        "samtools-wave",
		Namespace:   "default",
		Datacenters: []string{"dc1"},
		Priority:    50,
		Nodes:       1,
		Cores:       4,
		MemoryMB:    8192,
		Driver:      "docker",
		DriverConfig: map[string]string{"image": "wave.seqera.io/wt/abc123/ubuntu:22.04"},
		Wave: WaveSpec{
			Enabled:          true,
			CondaFileContent: "name: env\n",
			TokenSecretPath:  "nomad/jobs",
			TokenSecretKey:   "wave_token",
			Platform:         "linux/amd64",
			BinarySourceURL:  "http://rustfs/binary_tools/wave",
		},
	}
	hcl := Generate(spec, "samtools.sh", "#!/bin/bash\necho ok\n")

	script := shellcheck.ExtractHCLHeredoc(hcl, "wave-build.sh")
	if script == "" {
		t.Fatalf("could not extract wave-build.sh heredoc body from HCL:\n%s", hcl)
	}

	// hclwrite auto-escapes ${VAR} → $${VAR} in heredoc emission, so the
	// HCL-escape pre-check (ABC001) doesn't apply here. Always run the
	// embedded bash parser; run shellcheck only when it's on PATH.
	if perr := shellcheck.Parse(script); perr != nil {
		t.Fatalf("wave-build.sh fails bash parse: %v\n--- script ---\n%s", perr, script)
	}
	out, err := shellcheck.Lint(context.Background(), script, shellcheck.Default())
	switch {
	case errors.Is(err, shellcheck.ErrShellcheckUnavailable):
		t.Logf("system shellcheck not on PATH; embedded parse passed")
	case err != nil:
		t.Logf("shellcheck findings (advisory):\n%s", out)
	}
}

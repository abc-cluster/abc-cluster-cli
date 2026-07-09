package job

import (
	"strings"
	"testing"
)

// A --tools / --volume mount must attach the host volume to the MAIN task so an
// otherwise-chrooted exec task can reach node-provided tools (abc-tools: s5cmd,
// mc, …). Without this the generated exec HCL had no volume_mount at all.
func TestGenerate_ToolsMount(t *testing.T) {
	spec := Spec{
		Name: "toolsjob", Driver: "exec", Nodes: 1, Priority: 50,
		Mounts: []VolumeMount{{Volume: "abc-tools", Dest: "/opt/abc-tools", ReadOnly: true}},
	}
	out := Generate(spec, "run.sh", "s5cmd version")
	norm := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		`volume "abc-tools"`,   // group-level declaration
		`source = "abc-tools"`, // registered host_volume NAME
		`volume_mount {`,       // on the main task
		`volume = "abc-tools"`,
		`destination = "/opt/abc-tools"`,
	} {
		if !strings.Contains(norm, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// A tools-only mount must not drag in staging lifecycle tasks.
	if strings.Contains(out, "stage-in") {
		t.Errorf("unexpected stage task for a tools-only mount:\n%s", out)
	}
}

// Staging and a --tools mount both need the abc-tools volume; the group-level
// `volume "abc-tools"` block must be declared exactly once (declaring it twice
// is invalid HCL).
func TestGenerate_MountDedupWithStaging(t *testing.T) {
	spec := Spec{
		Name: "both", Driver: "exec", Nodes: 1, Priority: 50,
		Mounts: []VolumeMount{{Volume: "abc-tools", Dest: "/opt/abc-tools", ReadOnly: true}},
		Staging: StagingSpec{
			Enabled:          true,
			StageInManifest:  "cp s3://b/x.csv x.csv\n",
			StageOutManifest: "cp out.txt s3://b/out.txt\n",
			DestRoot:         "$NOMAD_ALLOC_DIR/data/r1",
			S5cmdPath:        "/opt/abc-tools/bin/s5cmd",
			HostVolumeName:   "abc-tools",
			HostVolumeMount:  "/opt/abc-tools",
		},
	}
	out := Generate(spec, "run.sh", "echo hi")
	if n := strings.Count(out, `volume "abc-tools"`); n != 1 {
		t.Errorf("abc-tools volume should be declared exactly once (deduped), got %d:\n%s", n, out)
	}
}

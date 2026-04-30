package data

import (
	"strings"
	"testing"
)

func TestBuildDataDownloadJobRunArgs(t *testing.T) {
	script := "/tmp/x.sh"
	img := "docker.io/library/busybox:1.36"
	t.Run("containerd", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("containerd", img, "dl-1", nil, script)
		want := []string{"job", "run", "--submit", "--name", "dl-1", "--driver", "containerd-driver", "--driver.config", "image=" + img, script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("docker", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("docker", img, "", nil, script)
		want := []string{"job", "run", "--submit", "--driver", "docker", "--driver.config", "image=" + img, script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("exec", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("exec", img, "n", nil, script)
		want := []string{"job", "run", "--submit", "--name", "n", "--driver", "exec", script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("raw_exec", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("raw_exec", img, "re", nil, script)
		want := []string{"job", "run", "--submit", "--name", "re", "--driver", "raw_exec", script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("containerd_driver_explicit", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("containerd-driver", img, "x", nil, script)
		want := []string{"job", "run", "--submit", "--name", "x", "--driver", "containerd-driver", "--driver.config", "image=" + img, script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("exec_with_artifact", func(t *testing.T) {
		artURL := "http://rustfs.aither/releases/s5cmd/v2.1.0/s5cmd_linux_amd64"
		arts := []downloadArtifact{{URL: artURL}}
		got := buildDataDownloadJobRunArgs("exec", img, "dl", arts, script)
		want := []string{"job", "run", "--submit", "--name", "dl", "--artifact", artURL, "--driver", "exec", script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("exec_with_artifact_dest_mode", func(t *testing.T) {
		artURL := "http://rustfs.aither/abc-reserved/binary_tools/s5cmd-${attr.kernel.name}-${attr.cpu.arch}"
		arts := []downloadArtifact{{URL: artURL, Dest: "local/s5cmd", Mode: "file"}}
		got := buildDataDownloadJobRunArgs("exec", img, "dl", arts, script)
		encoded := artURL + "|local/s5cmd|file"
		want := []string{"job", "run", "--submit", "--name", "dl", "--artifact", encoded, "--driver", "exec", script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("exec_with_two_artifacts", func(t *testing.T) {
		url1 := "http://rustfs.aither/abc-reserved/binary_tools/rclone-${attr.kernel.name}-${attr.cpu.arch}"
		url2 := "http://rustfs.aither/abc-reserved/binary_tools/s5cmd-${attr.kernel.name}-${attr.cpu.arch}"
		arts := []downloadArtifact{
			{URL: url1, Dest: "local/rclone", Mode: "file"},
			{URL: url2, Dest: "local/s5cmd", Mode: "file"},
		}
		got := buildDataDownloadJobRunArgs("exec", img, "dl", arts, script)
		want := []string{
			"job", "run", "--submit", "--name", "dl",
			"--artifact", url1 + "|local/rclone|file",
			"--artifact", url2 + "|local/s5cmd|file",
			"--driver", "exec", script,
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
}

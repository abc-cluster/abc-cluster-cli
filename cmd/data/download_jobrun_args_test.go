package data

import (
	"strings"
	"testing"
)

func TestBuildDataDownloadJobRunArgs(t *testing.T) {
	script := "/tmp/x.sh"
	img := "docker.io/library/busybox:1.36"
	t.Run("containerd", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("containerd", img, "dl-1", script)
		want := []string{"job", "run", "--submit", "--name", "dl-1", "--driver", "containerd-driver", "--driver.config", "image=" + img, script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("docker", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("docker", img, "", script)
		want := []string{"job", "run", "--submit", "--driver", "docker", "--driver.config", "image=" + img, script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("exec", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("exec", img, "n", script)
		want := []string{"job", "run", "--submit", "--name", "n", "--driver", "exec", script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("raw_exec", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("raw_exec", img, "re", script)
		want := []string{"job", "run", "--submit", "--name", "re", "--driver", "raw_exec", script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("containerd_driver_explicit", func(t *testing.T) {
		got := buildDataDownloadJobRunArgs("containerd-driver", img, "x", script)
		want := []string{"job", "run", "--submit", "--name", "x", "--driver", "containerd-driver", "--driver.config", "image=" + img, script}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %q want %q", got, want)
		}
	})
}

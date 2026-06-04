package data

import (
	"testing"
	"time"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func TestCourierMaxDays(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"1h", 1},     // sub-day rounds up to 1
		{"23h", 1},    // still under a day
		{"24h", 1},    // exactly one day
		{"25h", 2},    // just over → ceil to 2
		{"168h", 7},   // 7 days
		{"200h", 9},   // 8.33d → ceil 9
		{"30m", 1},    // floor at 1
	}
	for _, c := range cases {
		d, err := time.ParseDuration(c.in)
		if err != nil {
			t.Fatalf("parse %q: %v", c.in, err)
		}
		if got := courierMaxDays(d); got != c.want {
			t.Errorf("courierMaxDays(%s) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestDeriveTransferFromBase(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"workbench base", "https://workbench.seedling.abc-cluster.cloud", "https://transfer.seedling.abc-cluster.cloud"},
		{"nomad base", "https://nomad.seedling.abc-cluster.cloud", "https://transfer.seedling.abc-cluster.cloud"},
		{"api base", "https://api.seedling.abc-cluster.cloud", "https://transfer.seedling.abc-cluster.cloud"},
		{"bare base", "https://seedling.abc-cluster.cloud", "https://transfer.seedling.abc-cluster.cloud"},
		{"already transfer", "https://transfer.seedling.abc-cluster.cloud", "https://transfer.seedling.abc-cluster.cloud"},
		{"trailing slash", "https://workbench.seedling.abc-cluster.cloud/", "https://transfer.seedling.abc-cluster.cloud"},
		{"empty", "", ""},
		{"bare ip+port (no DNS)", "http://100.70.185.46:4646", ""},
		{"single-label host", "https://localhost", ""},
	}
	for _, c := range cases {
		if got := deriveTransferFromBase(c.in); got != c.want {
			t.Errorf("%s: deriveTransferFromBase(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestResolveCourierEndpoint_Precedence(t *testing.T) {
	actx := abccfg.Context{
		AuthEndpoint: "https://workbench.seedling.abc-cluster.cloud",
		Endpoint:     "https://nomad.seedling.abc-cluster.cloud",
	}

	// 1) explicit flag wins (trailing slash trimmed)
	cmd := newCourierCmd()
	got, err := resolveCourierEndpoint(cmd, "https://transfer.example.test/", actx)
	if err != nil {
		t.Fatalf("flag: %v", err)
	}
	if got != "https://transfer.example.test" {
		t.Errorf("flag precedence: got %q", got)
	}

	// 2) env var (when no flag)
	t.Setenv("ABC_TRANSFER_ENDPOINT", "https://transfer.env.test")
	got, err = resolveCourierEndpoint(cmd, "", actx)
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	if got != "https://transfer.env.test" {
		t.Errorf("env precedence: got %q", got)
	}
	t.Setenv("ABC_TRANSFER_ENDPOINT", "")

	// 3) derived from AuthEndpoint (preferred over Endpoint)
	got, err = resolveCourierEndpoint(cmd, "", actx)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got != "https://transfer.seedling.abc-cluster.cloud" {
		t.Errorf("derive precedence: got %q", got)
	}

	// 4) no context, no flag, no env → error
	_, err = resolveCourierEndpoint(cmd, "", abccfg.Context{})
	if err == nil {
		t.Errorf("expected error when no endpoint resolvable")
	}
}

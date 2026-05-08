package compute

import (
	"strings"
	"testing"
)

func TestRenderProbeScheduleArgs_Default(t *testing.T) {
	got := renderProbeScheduleArgs("")
	wantSubs := []string{
		`"--quiet"`, `"--evaluate"`, `"--nomad-mode"`,
		`"--jurisdiction=${NOMAD_META_jurisdiction}"`,
		`"--history-file=${NOMAD_META_history_file}"`,
		`"--push-prometheus=${NOMAD_META_pushgateway}"`,
		`"--timeout=2m"`,
	}
	for _, s := range wantSubs {
		if !strings.Contains(got, s) {
			t.Errorf("args missing %q\ngot: %s", s, got)
		}
	}
	if strings.Contains(got, "--skip-categories") {
		t.Error("default args should not include --skip-categories")
	}
}

func TestRenderProbeScheduleArgs_SkipCategories(t *testing.T) {
	got := renderProbeScheduleArgs("smart,security")
	if !strings.Contains(got, `"--skip-categories=smart,security"`) {
		t.Errorf("args missing --skip-categories: %s", got)
	}
}

func TestRenderProbeScheduleHCL(t *testing.T) {
	p := probeScheduleParams{
		JobID:           "abc-node-probe-periodic",
		Datacenter:      "dc1",
		Namespace:       "abc-automations",
		Cron:            "0 3 * * *",
		TimeZone:        "UTC",
		ProhibitOverlap: true,
		BinaryPath:      "/opt/nomad/abc-node-probe",
		HistoryFile:     "/var/lib/abc/probe-history.jsonl",
		Pushgateway:     "http://pushgateway.service.consul:9091",
		Jurisdiction:    "ZA",
		ArgsHCL:         renderProbeScheduleArgs(""),
		CPU:             200,
		Memory:          256,
	}
	hcl, err := renderProbeScheduleHCL(p)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	wantSubs := []string{
		`job "abc-node-probe-periodic"`,
		`type        = "sysbatch"`,
		`namespace   = "abc-automations"`,
		`crons             = ["0 3 * * *"]`,
		`time_zone         = "UTC"`,
		`prohibit_overlap  = true`,
		`binary_path  = "/opt/nomad/abc-node-probe"`,
		`history_file = "/var/lib/abc/probe-history.jsonl"`,
		`pushgateway  = "http://pushgateway.service.consul:9091"`,
		`jurisdiction = "ZA"`,
		`command = "${NOMAD_META_binary_path}"`,
		`cpu    = 200`,
		`memory = 256`,
	}
	for _, s := range wantSubs {
		if !strings.Contains(hcl, s) {
			t.Errorf("HCL missing %q\nhcl:\n%s", s, hcl)
		}
	}
}

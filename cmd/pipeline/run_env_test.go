package pipeline

import (
	"strings"
	"testing"
)

// ExtraEnv (from --env / --git-token) must land in the generated head-job HCL
// env block so a private-repo GITHUB_TOKEN reaches the head task.
func TestGenerateHeadJobHCL_ExtraEnv(t *testing.T) {
	spec := &PipelineSpec{
		Repository:  "https://github.com/dylangrblr/Vinotype",
		Datacenters: []string{"seedling-prod"},
		ExtraEnv:    map[string]string{"GITHUB_TOKEN": "ghp_unit_test_token"},
	}
	spec.defaults()
	hcl := generateHeadJobHCL(spec, "http://127.0.0.1:4646", "token", "run-uuid")
	if !strings.Contains(hcl, "GITHUB_TOKEN") || !strings.Contains(hcl, "ghp_unit_test_token") {
		t.Fatalf("ExtraEnv (GITHUB_TOKEN) not present in generated head-job HCL")
	}
}

func TestParseHeadEnv(t *testing.T) {
	t.Setenv("ABC_TEST_TOKEN", "from-env-123")

	out, err := parseHeadEnv([]string{
		"FOO=bar",
		"EMPTY=",         // empty value is allowed
		"WITH=a=b=c",     // only the first '=' splits
		"ABC_TEST_TOKEN", // bare key → read from environment
		"  SPACED = v ",  // trims around the key
	})
	if err != nil {
		t.Fatalf("parseHeadEnv: %v", err)
	}
	want := map[string]string{
		"FOO":            "bar",
		"EMPTY":          "",
		"WITH":           "a=b=c",
		"ABC_TEST_TOKEN": "from-env-123",
		"SPACED":         " v ",
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("key %q = %q, want %q", k, out[k], v)
		}
	}
}

func TestParseHeadEnv_BareKeyUnset(t *testing.T) {
	if _, err := parseHeadEnv([]string{"DEFINITELY_NOT_SET_XYZ"}); err == nil {
		t.Fatal("bare key with no environment value should error")
	}
}

func TestParseHeadEnv_EmptyKey(t *testing.T) {
	if _, err := parseHeadEnv([]string{"=value"}); err == nil {
		t.Fatal("empty key should error")
	}
}

// mergeSpec must carry ExtraEnv from override onto base (override wins).
func TestMergeSpec_ExtraEnv(t *testing.T) {
	base := &PipelineSpec{ExtraEnv: map[string]string{"KEEP": "1", "OVER": "old"}}
	override := &PipelineSpec{ExtraEnv: map[string]string{"OVER": "new", "ADD": "2"}}
	merged := mergeSpec(base, override)
	if merged.ExtraEnv["KEEP"] != "1" || merged.ExtraEnv["OVER"] != "new" || merged.ExtraEnv["ADD"] != "2" {
		t.Fatalf("ExtraEnv merge wrong: %#v", merged.ExtraEnv)
	}
}

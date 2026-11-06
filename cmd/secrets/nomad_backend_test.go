package secrets

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// The alloc-read policy must (a) nest its variables rule in a namespace block
// — Nomad's strict policy parser rejects a top-level `variables` block — and
// (b) carry a JobACL so Nomad attaches it to the namespace's job workload
// identities; without it allocations never receive the grant.
func TestAllocReadPolicyBody(t *testing.T) {
	body := allocReadPolicyBody("su-everest-nf")

	if got := body["Name"]; got != "abc-secrets-alloc-read-su-everest-nf" {
		t.Errorf("policy name = %v, want per-namespace name", got)
	}
	jobACL, ok := body["JobACL"].(map[string]string)
	if !ok || jobACL["Namespace"] != "su-everest-nf" || len(jobACL) != 1 {
		t.Errorf("JobACL = %#v, want {Namespace: su-everest-nf}", body["JobACL"])
	}

	rules, _ := body["Rules"].(string)
	f, diags := hclsyntax.ParseConfig([]byte(rules), "policy.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("rules do not parse: %v\n%s", diags, rules)
	}
	top := f.Body.(*hclsyntax.Body)
	if len(top.Attributes) != 0 || len(top.Blocks) != 1 {
		t.Fatalf("want exactly one top-level block, got %d blocks/%d attrs:\n%s", len(top.Blocks), len(top.Attributes), rules)
	}
	ns := top.Blocks[0]
	if ns.Type != "namespace" || len(ns.Labels) != 1 || ns.Labels[0] != "su-everest-nf" {
		t.Fatalf("top-level block = %s %v, want namespace \"su-everest-nf\"", ns.Type, ns.Labels)
	}
	if len(ns.Body.Blocks) != 1 || ns.Body.Blocks[0].Type != "variables" {
		t.Fatalf("namespace block must contain one variables block:\n%s", rules)
	}
	vars := ns.Body.Blocks[0]
	if len(vars.Body.Blocks) != 1 || vars.Body.Blocks[0].Type != "path" ||
		vars.Body.Blocks[0].Labels[0] != "abc/secrets/su-everest-nf/*" {
		t.Fatalf("variables block must hold path \"abc/secrets/su-everest-nf/*\":\n%s", rules)
	}
	capAttr, ok := vars.Body.Blocks[0].Body.Attributes["capabilities"]
	if !ok {
		t.Fatalf("path block has no capabilities:\n%s", rules)
	}
	v, d := capAttr.Expr.Value(nil)
	if d.HasErrors() || v.LengthInt() != 1 || v.Index(cty.NumberIntVal(0)).AsString() != "read" {
		t.Errorf("capabilities = %#v, want [\"read\"] only", v)
	}
}

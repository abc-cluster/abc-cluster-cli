// Package gendoc renders the canonical env-var reference Markdown from
// the envvars.Registry. Output goes to docs/reference/env-vars.md.
//
// Usage:
//
//	go run ./internal/envvars/gendoc -out docs/reference/env-vars.md
//
// Or via `go generate ./internal/envvars/...` (see internal/envvars/gen.go).
//
// Spec: $ABC_UNIVERSE/specs/active/abc-cli-env-resolution.md §C.2
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
)

const docTemplate = `---
sidebar_position: 99
---

<!--
  GENERATED FILE — DO NOT EDIT BY HAND.
  Source: internal/envvars/registry.go
  Regenerate: go generate ./internal/envvars/... (or "just gen")
  Drift check: "just gen-check"
-->

# Environment variables

The abc CLI uses a single registry-backed resolver for every environment
variable it consults. Names are scope-prefixed so the surface stays
predictable:

- ` + "`ABC_API_*`" + ` — transport, auth, identity (talks to the abc-cluster
  control plane)
- ` + "`ABC_CLI_*`" + ` — local CLI configuration, behaviour, output, debug
- ` + "`ABC_<COMPONENT>_*`" + ` — component-scoped overrides
  (controller, khan, node-probe, tool binaries)
- ` + "`ABC_<RESOURCE>`" + ` — cluster-resource selectors only:
  ` + "`ABC_WORKSPACE`" + `, ` + "`ABC_REGION`" + `, ` + "`ABC_NAMESPACE`" + `,
  ` + "`ABC_ORG`" + `, ` + "`ABC_CLUSTER`" + `, ` + "`ABC_PROJECT`" + `,
  ` + "`ABC_INVESTIGATION`" + `

## Resolution precedence

For any registered variable, the resolver walks:

` + "```" + `
flag  >  ABC env  >  vendor env  >  active context config  >  default
` + "```" + `

- **flag** — explicit ` + "`--<name>`" + ` on the command line
- **ABC env** — ` + "`ABC_*`" + ` set in the environment (via ` + "`os.LookupEnv`" + ` —
  explicit empty counts)
- **vendor env** — last-resort fallback for a handful of ABC vars that
  have a vendor namesake (e.g. ` + "`ABC_REGION`" + ` falls back to
  ` + "`NOMAD_REGION`" + `). Emits a one-time warning when no ABC context is
  configured.
- **active context** — value persisted under ` + "`contexts.<name>.<key>`" + `
  in ` + "`~/.abc/config.yaml`" + `
- **default** — registry-declared default (or empty)

Use ` + "`abc admin env show <NAME>`" + ` to see which source won for any
variable in your current shell.

## Variables by scope

{{range .Groups}}
### {{.Title}}

{{if .Description}}{{.Description}}

{{end -}}
| Name | Purpose | Flag | Context key | Vendor fallback | Default |
|---|---|---|---|---|---|
{{range .Entries -}}
| ` + "`{{.Name}}`" + `{{if .Secret}} 🔒{{end}} | {{.Purpose}} | {{if .FlagName}}` + "`--{{.FlagName}}`" + `{{else}}—{{end}} | {{if .ContextKey}}` + "`{{.ContextKey}}`" + `{{else}}—{{end}} | {{if .VendorFallback}}` + "`{{.VendorFallback}}`" + `{{else}}—{{end}} | {{if .Default}}` + "`{{.Default}}`" + `{{else}}—{{end}} |
{{end}}
{{end}}

## Forbidden patterns

The registry rejects (and ` + "`abc admin env validate`" + ` flags) these
patterns in the environment:

- ` + "`ABC_DISABLE_*`" + ` — use ` + "`ABC_<SCOPE>_NO_<FEATURE>`" + ` instead
- ` + "`ABC_*_OFF`" + ` — same
- ` + "`ABC_GROVE_*`" + ` / ` + "`ABC_SEEDLING_*`" + ` / ` + "`ABC_CLOUD_*`" + ` —
  env vars are tier-neutral; they do not know which abc-cluster tier
  they're running against

## Subprocess injection

When the CLI shells out to ` + "`nomad`" + `, ` + "`vault`" + `, ` + "`rclone`" + `,
` + "`s5cmd`" + `, or ` + "`nextflow`" + `, it **constructs** the relevant vendor
env vars from the active context and injects them into the child process.
You do not need to set ` + "`NOMAD_ADDR`" + `, ` + "`AWS_ACCESS_KEY_ID`" + ` etc.
in your shell — the abstraction handles it. In ` + "`abc admin services <tool> cli`" + `
passthrough commands, parent-shell vendor env vars are preserved so
operators can target alternate endpoints.

🔒 = redacted in ` + "`abc admin env list`" + ` output.

_This page is generated from
[` + "`internal/envvars/registry.go`" + `](https://github.com/abc-cluster/abc-cluster-cli/blob/main/internal/envvars/registry.go)
— edits there propagate here via ` + "`go generate ./internal/envvars/...`" + `._
`

type entryView struct {
	Name           string
	Purpose        string
	FlagName       string
	ContextKey     string
	VendorFallback string
	Default        string
	Secret         bool
}

type group struct {
	Title       string
	Description string
	Entries     []entryView
}

type docData struct {
	Groups []group
}

func main() {
	out := flag.String("out", "docs/reference/env-vars.md", "output Markdown path")
	flag.Parse()

	tmpl, err := template.New("envdoc").Parse(docTemplate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse template: %v\n", err)
		os.Exit(1)
	}

	groups := buildGroups()
	data := docData{Groups: groups}

	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", *out, err)
		os.Exit(1)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		fmt.Fprintf(os.Stderr, "render: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d entries across %d groups)\n",
		*out, totalEntries(groups), len(groups))
}

func buildGroups() []group {
	type spec struct {
		bucket envvars.Bucket
		title  string
		desc   string
	}
	specs := []spec{
		{envvars.BucketABCAPI, "abc-cluster API", "Transport and auth for the abc-cluster control plane."},
		{envvars.BucketABCCLI, "CLI configuration & behaviour", "Local CLI state: config file location, output format, telemetry, modes."},
		{envvars.BucketABCResource, "Cluster resource selectors", "Resource identifiers a command operates against."},
		{envvars.BucketABCComponent, "Component-scoped", "Component overrides for capability cache, upload, crypt, node bootstrap."},
		{envvars.BucketToolBinary, "Tool binary overrides", "Paths to subprocess binaries the CLI shells out to. Operator territory."},
		{envvars.BucketDebugTest, "Debug & test (internal)", "Reserved namespace. Not for end-user use."},
		{envvars.BucketVendorFallback, "Vendor fallback (last resort)", "Vendor env vars read silently when the canonical ABC name and active context are both unset. Emits a one-time warning if no ABC context exists."},
		{envvars.BucketSubprocessOut, "Subprocess injection", "The CLI constructs these for child processes from the active context. You do not set them."},
	}

	var out []group
	for _, s := range specs {
		entries := envvars.ByBucket(s.bucket)
		if len(entries) == 0 {
			continue
		}
		views := make([]entryView, 0, len(entries))
		for _, e := range entries {
			views = append(views, entryView{
				Name:           e.Name,
				Purpose:        escapeMD(e.Purpose),
				FlagName:       e.FlagName,
				ContextKey:     e.ContextKey,
				VendorFallback: e.VendorFallback,
				Default:        e.Default,
				Secret:         e.Secret,
			})
		}
		out = append(out, group{Title: s.title, Description: s.desc, Entries: views})
	}
	return out
}

func totalEntries(gs []group) int {
	n := 0
	for _, g := range gs {
		n += len(g.Entries)
	}
	return n
}

// escapeMD escapes pipe characters in cell text to avoid breaking Markdown tables.
func escapeMD(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

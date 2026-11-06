package job_test

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// renderedTemplate is one task template stanza decoded from generated HCL.
type renderedTemplate struct {
	Data, Perms, ChangeMode string
	Env                     bool
}

// templatesByDestination parses the generated job HCL and returns its task
// template stanzas keyed by destination. Decoding (rather than substring
// matching) proves the {{ }} refs survive HCL escaping byte-for-byte.
func templatesByDestination(t *testing.T, out string) map[string]renderedTemplate {
	t.Helper()
	start := strings.Index(out, "job \"")
	if start < 0 {
		t.Fatalf("no job block in output:\n%s", out)
	}
	f, diags := hclsyntax.ParseConfig([]byte(out[start:]), "job.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("generated HCL does not parse: %v\n%s", diags, out)
	}
	got := map[string]renderedTemplate{}
	var walk func(b *hclsyntax.Body)
	walk = func(b *hclsyntax.Body) {
		for _, blk := range b.Blocks {
			if blk.Type == "template" {
				var rt renderedTemplate
				dest := ""
				for name, attr := range blk.Body.Attributes {
					v, d := attr.Expr.Value(nil)
					if d.HasErrors() {
						t.Fatalf("template attr %s: %v", name, d)
					}
					switch name {
					case "destination":
						dest = v.AsString()
					case "data":
						rt.Data = v.AsString()
					case "perms":
						rt.Perms = v.AsString()
					case "change_mode":
						rt.ChangeMode = v.AsString()
					case "env":
						rt.Env = v.True()
					}
				}
				got[dest] = rt
			}
			walk(blk.Body)
		}
	}
	walk(f.Body.(*hclsyntax.Body))
	return got
}

func TestJobRun_SecretDirectivesRenderTemplates(t *testing.T) {
	script := `#!/bin/sh
#ABC --name=stage
#ABC --namespace=lab
#ABC --driver=docker
#ABC --driver.config.image=rclone/rclone:1.68.2
#ABC --secret=AWS_SECRET_ACCESS_KEY=acacia-sk
#ABC --secret=S3_ENDPOINT=secret://acacia-endpoint
#ABC --secret-file=rclone.conf=acacia-rclone
#ABC --secret-file=secret://ca-bundle
echo "$S3_ENDPOINT"
`
	out, err := executeCmd(t, writeTempScript(t, "stage.sh", script))
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	tmpls := templatesByDestination(t, out)

	env, ok := tmpls["secrets/abc-secrets.env"]
	if !ok {
		t.Fatalf("no secrets/abc-secrets.env template; got %v", keys(tmpls))
	}
	wantEnv := `AWS_SECRET_ACCESS_KEY={{ with nomadVar "abc/secrets/lab/acacia-sk" }}{{ index . "value" | printf "%s" | toJSON }}{{ end }}` + "\n" +
		`S3_ENDPOINT={{ with nomadVar "abc/secrets/lab/acacia-endpoint" }}{{ index . "value" | printf "%s" | toJSON }}{{ end }}` + "\n"
	if env.Data != wantEnv {
		t.Errorf("env template data:\n got %q\nwant %q", env.Data, wantEnv)
	}
	if !env.Env || env.Perms != "0400" || env.ChangeMode != "noop" {
		t.Errorf("env template flags: %+v (want env=true perms=0400 change_mode=noop)", env)
	}

	for dest, name := range map[string]string{
		"secrets/rclone.conf": "acacia-rclone",
		"secrets/ca-bundle":   "ca-bundle",
	} {
		ft, ok := tmpls[dest]
		if !ok {
			t.Errorf("no %s template; got %v", dest, keys(tmpls))
			continue
		}
		want := `{{ with nomadVar "abc/secrets/lab/` + name + `" }}{{ index . "value" }}{{ end }}`
		if ft.Data != want {
			t.Errorf("%s data:\n got %q\nwant %q", dest, ft.Data, want)
		}
		if ft.Env || ft.Perms != "0400" || ft.ChangeMode != "noop" {
			t.Errorf("%s flags: %+v (want env=false perms=0400 change_mode=noop)", dest, ft)
		}
	}
}

func TestJobRun_SecretDefaultsToDefaultNamespace(t *testing.T) {
	script := "#!/bin/sh\n#ABC --name=j\n#ABC --driver=docker\n#ABC --secret-file=tok\necho hi\n"
	out, err := executeCmd(t, writeTempScript(t, "j.sh", script))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := templatesByDestination(t, out)["secrets/tok"].Data; !strings.Contains(got, `"abc/secrets/default/tok"`) {
		t.Errorf("expected default-namespace path, got %q", got)
	}
}

func TestJobRun_SecretFlagsOverridePreamble(t *testing.T) {
	script := "#!/bin/sh\n#ABC --name=j\n#ABC --namespace=lab\n#ABC --driver=docker\n#ABC --secret=TOKEN=old-name\necho hi\n"
	out, err := executeCmd(t, writeTempScript(t, "j.sh", script),
		"--secret", "TOKEN=secret://new-name", "--secret-file", "cfg=rclone-conf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tmpls := templatesByDestination(t, out)
	env := tmpls["secrets/abc-secrets.env"].Data
	if !strings.Contains(env, "abc/secrets/lab/new-name") || strings.Contains(env, "old-name") {
		t.Errorf("CLI --secret should replace the preamble binding for TOKEN, got %q", env)
	}
	if _, ok := tmpls["secrets/cfg"]; !ok {
		t.Errorf("expected secrets/cfg from --secret-file flag; got %v", keys(tmpls))
	}
}

func TestJobRun_NoSecretsNoSecretTemplates(t *testing.T) {
	out, err := executeCmd(t, writeTempScript(t, "j.sh", "#!/bin/sh\n#ABC --name=j\n#ABC --driver=docker\necho hi\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "nomadVar") || strings.Contains(out, "abc-secrets.env") {
		t.Errorf("unexpected secret template in output:\n%s", out)
	}
}

func TestJobRun_SecretDirectiveErrors(t *testing.T) {
	cases := map[string]string{
		"missing env":       "#ABC --secret=just-a-name",
		"bad env name":      "#ABC --secret=1BAD=name",
		"empty name":        "#ABC --secret=ENV=secret://",
		"slash in name":     "#ABC --secret=ENV=team/name",
		"template in name":  "#ABC --secret-file={{x}}",
		"slash in file":     "#ABC --secret-file=dir/f=name",
		"reserved env file": "#ABC --secret-file=abc-secrets.env=name",
		"no value":          "#ABC --secret",
		"no file value":     "#ABC --secret-file",
		"slurm unsupported": "#ABC --driver=slurm\n#ABC --secret=ENV=name",
		"pbs unsupported":   "#ABC --driver=pbs\n#ABC --secret-file=name",
	}
	for label, dirs := range cases {
		t.Run(label, func(t *testing.T) {
			script := "#!/bin/sh\n#ABC --name=j\n" + dirs + "\necho hi\n"
			if _, err := executeCmd(t, writeTempScript(t, "j.sh", script)); err == nil {
				t.Fatalf("expected an error for %q", dirs)
			}
		})
	}
}

func keys(m map[string]renderedTemplate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

package appgen

import (
	"strings"
	"testing"
)

func TestScaffoldYAML_StreamlitDefaults(t *testing.T) {
	got := ScaffoldYAML(ScaffoldOptions{Framework: "streamlit", Name: "demo", Project: "demo"})
	// The scaffold must round-trip through the parser + validator (after the
	// placeholder image is taken as-is — it is a valid registry/repo:tag form).
	for _, frag := range []string{
		"name: demo",
		"project: demo",
		"framework: streamlit",
		"port: 8501",
		"health: /_stcore/health",
		"access: team",
		"# data:",
		"# env:",
		"# resources:",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("streamlit scaffold missing %q:\n%s", frag, got)
		}
	}
}

func TestScaffoldYAML_ParsesAndValidates(t *testing.T) {
	for _, fw := range []string{"streamlit", "shiny", "pode"} {
		t.Run(fw, func(t *testing.T) {
			yaml := ScaffoldYAML(ScaffoldOptions{Framework: fw, Name: "demo", Project: "demo"})
			s, err := Parse([]byte(yaml))
			if err != nil {
				t.Fatalf("scaffold did not parse: %v\n%s", err, yaml)
			}
			if err := s.Validate(); err != nil {
				t.Fatalf("scaffold did not validate: %v", err)
			}
			s.ApplyDefaults()
			if s.Port != frameworkDefaults[fw].port {
				t.Errorf("port: got %d want %d", s.Port, frameworkDefaults[fw].port)
			}
		})
	}
}

func TestScaffoldYAML_Placeholders(t *testing.T) {
	got := ScaffoldYAML(ScaffoldOptions{Framework: "shiny"})
	if !strings.Contains(got, "name: my-shiny-app") {
		t.Errorf("expected placeholder name, got:\n%s", got)
	}
	if !strings.Contains(got, "project: my-project") {
		t.Errorf("expected placeholder project, got:\n%s", got)
	}
}

func TestScaffoldDockerfile_BindContract(t *testing.T) {
	cases := map[string][]string{
		"streamlit": {"FROM python", "--server.address=0.0.0.0", "--server.port=8501", "--server.headless=true"},
		"shiny":     {"shiny.host='0.0.0.0'", "shiny.port=3838"},
		"pode":      {"PODE_HOST=0.0.0.0", "PODE_PORT=8085"},
		"custom":    {"0.0.0.0:8080"},
	}
	for fw, frags := range cases {
		t.Run(fw, func(t *testing.T) {
			port := 8080
			if def, ok := frameworkDefaults[fw]; ok && def.port != 0 {
				port = def.port
			}
			df := ScaffoldDockerfile(ScaffoldOptions{Framework: fw})
			for _, frag := range frags {
				if !strings.Contains(df, frag) {
					t.Errorf("%s Dockerfile missing %q:\n%s", fw, frag, df)
				}
			}
			if !strings.Contains(df, "EXPOSE") {
				t.Errorf("%s Dockerfile missing EXPOSE", fw)
			}
			_ = port
		})
	}
}

func TestIsProtectedEnvKey(t *testing.T) {
	protected := []string{
		"ABC_APP_NAME", "ABC_PROJECT", "ABC_MINIO_ENDPOINT",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"abc_app_url", "aws_session_token", // case-insensitive
	}
	for _, k := range protected {
		if !IsProtectedEnvKey(k) {
			t.Errorf("expected %q to be protected", k)
		}
	}
	allowed := []string{"LOG_LEVEL", "SUCURI_ENVIRONMENT", "MY_VAR", "PORT"}
	for _, k := range allowed {
		if IsProtectedEnvKey(k) {
			t.Errorf("expected %q to be allowed", k)
		}
	}
}

func TestResolvedSummary(t *testing.T) {
	s := validSucuriSpec()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.ApplyDefaults()
	out := s.ResolvedSummary(AppsDoors{})
	for _, frag := range []string{
		"name        sucuri",
		"framework   pode",
		"port        8085",
		"health      /health/ready",
		"job         app-abc-platform-sucuri",
		"url         https://abc-platform-sucuri.apps.seedling.abc-cluster.cloud",
		"SUCURI_ENVIRONMENT=production",
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("ResolvedSummary missing %q:\n%s", frag, out)
		}
	}
}

// static must scaffold a Dockerfile whose server already binds 0.0.0.0:8080,
// so the platform's bind contract holds without editing nginx.conf.
func TestScaffoldDockerfile_Static(t *testing.T) {
	got := ScaffoldDockerfile(ScaffoldOptions{Framework: "static", Name: "report"})
	for _, frag := range []string{"nginx-unprivileged", "/usr/share/nginx/html/", "EXPOSE 8080"} {
		if !strings.Contains(got, frag) {
			t.Errorf("static Dockerfile missing %q:\n%s", frag, got)
		}
	}
}

// A static app has no session to pin, so it must not be marked stateful.
func TestFrameworkDefaults_StaticIsStateless(t *testing.T) {
	def, ok := frameworkDefaults["static"]
	if !ok {
		t.Fatal("static missing from frameworkDefaults")
	}
	if !def.supported {
		t.Error("static should be supported")
	}
	if def.stateful {
		t.Error("static has no session or WebSocket, so it must not be stateful")
	}
	if def.port != 8080 || def.health != "/" {
		t.Errorf("unexpected defaults: port=%d health=%q", def.port, def.health)
	}
}

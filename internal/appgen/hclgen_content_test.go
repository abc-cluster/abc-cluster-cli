package appgen

import (
	"regexp"
	"strings"
	"testing"
)

// normaliseWS collapses runs of spaces so assertions do not depend on the
// column alignment hclwrite chooses, which shifts with the longest key present.
var wsRe = regexp.MustCompile(`[ \t]+`)

func normaliseWS(s string) string { return wsRe.ReplaceAllString(s, " ") }

// A content app must run the platform's file server against the fetched
// artifact, never a user image.
func TestGenerate_ContentUsesServerImageAndArtifact(t *testing.T) {
	s := &Spec{
		Version: CurrentSpecVersion, Name: "rep", Project: "demo",
		Framework: "static", Content: "./report.html",
		Port: 8080, Health: "/", Access: "team",
		Expose: ExposePlanes{"private"}, Replicas: 1,
		Resources: Resources{CPU: 200, Memory: 128},
	}
	got := Generate(s, JobParams{
		Namespace: "abc-apps", NodePool: "platform",
		MinIOEndpoint: "http://minio:9000",
		ContentDigest: "deadbeef",
	})

	got = normaliseWS(got)
	for _, want := range []string{
		`image = "caddy:alpine"`,
		`command = "caddy"`,
		`"file-server"`,
		`"--root"`,
		`"/local"`,
		`artifact {`,
		`s3::http://minio:9000/abc-reserved/app-content/demo/rep/deadbeef/`,
		`destination = "local/"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated job missing %q:\n%s", want, got)
		}
	}
}

// Without a digest there is nothing to fetch, so no artifact should be emitted.
func TestGenerate_NoDigestNoArtifact(t *testing.T) {
	s := &Spec{
		Version: CurrentSpecVersion, Name: "rep", Project: "demo",
		Framework: "static", Content: "./report.html",
		Port: 8080, Health: "/", Access: "team",
		Expose: ExposePlanes{"private"}, Replicas: 1,
		Resources: Resources{CPU: 200, Memory: 128},
	}
	if got := Generate(s, JobParams{Namespace: "abc-apps"}); strings.Contains(got, "artifact {") {
		t.Errorf("no digest should mean no artifact stanza:\n%s", got)
	}
}

// An image-based app must be untouched by any of this.
func TestGenerate_ImageAppUnaffected(t *testing.T) {
	s := &Spec{
		Version: CurrentSpecVersion, Name: "app", Project: "demo",
		Framework: "streamlit", Image: "ghcr.io/org/app:1",
		Port: 8501, Health: "/_stcore/health", Access: "team",
		Expose: ExposePlanes{"private"}, Replicas: 1,
		Resources: Resources{CPU: 500, Memory: 1024},
	}
	got := normaliseWS(Generate(s, JobParams{Namespace: "abc-apps"}))
	if !strings.Contains(got, `image = "ghcr.io/org/app:1"`) {
		t.Errorf("image app must keep its image:\n%s", got)
	}
	if strings.Contains(got, "artifact {") || strings.Contains(got, "caddy") {
		t.Errorf("image app must not gain artifact/caddy:\n%s", got)
	}
}

// Remote content is served straight from its prefix, with no digest involved.
func TestGenerate_RemoteContentUsesPrefixDirectly(t *testing.T) {
	s := &Spec{
		Version: CurrentSpecVersion, Name: "rep", Project: "demo",
		Framework: "static", Content: "s3://nf-work/demo-results/multiqc/",
		Port: 8080, Health: "/", Access: "team",
		Expose: ExposePlanes{"private"}, Replicas: 1,
		Resources: Resources{CPU: 200, Memory: 128},
	}
	got := normaliseWS(Generate(s, JobParams{
		Namespace: "abc-apps", MinIOEndpoint: "http://minio:9000",
		AWSAccessKey: "AK", AWSSecretKey: "SK",
	}))
	for _, want := range []string{
		`s3::http://minio:9000/nf-work/demo-results/multiqc/`,
		`aws_access_key_id = "AK"`,
		`aws_access_key_secret = "SK"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "app-content") {
		t.Error("remote content must not be routed through the upload prefix")
	}
}

package appgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A single file is published as index.html, so a bare report is served at the
// app's root without the author renaming anything.
func TestWalkContent_SingleFileBecomesIndex(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "multiqc_report.html")
	if err := os.WriteFile(p, []byte("<html>report</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, total, err := WalkContent(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Rel != "index.html" {
		t.Fatalf("expected one file served as index.html, got %+v", files)
	}
	if total != 19 {
		t.Errorf("unexpected total %d", total)
	}
}

// Identical content must hash identically, so an unchanged redeploy is a no-op.
func TestContentDigest_StableAcrossIdenticalTrees(t *testing.T) {
	mk := func() string {
		d := t.TempDir()
		os.WriteFile(filepath.Join(d, "a.html"), []byte("A"), 0o644)
		os.WriteFile(filepath.Join(d, "b.css"), []byte("B"), 0o644)
		return d
	}
	f1, _, _ := WalkContent(mk())
	f2, _, _ := WalkContent(mk())
	d1, err := ContentDigest(f1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := ContentDigest(f2)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("identical trees must share a digest: %s != %s", d1, d2)
	}
}

func TestContentDigest_ChangesWithContent(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "x.html")
	os.WriteFile(p, []byte("one"), 0o644)
	f1, _, _ := WalkContent(p)
	d1, _ := ContentDigest(f1)
	os.WriteFile(p, []byte("two"), 0o644)
	f2, _, _ := WalkContent(p)
	d2, _ := ContentDigest(f2)
	if d1 == d2 {
		t.Error("changed content must change the digest")
	}
}

// content is static-only, and mutually exclusive with image.
func TestValidate_ContentRules(t *testing.T) {
	dir := t.TempDir()
	html := filepath.Join(dir, "r.html")
	os.WriteFile(html, []byte("<html/>"), 0o644)

	base := func() *Spec {
		return &Spec{Version: CurrentSpecVersion, Name: "rep", Project: "demo",
			Framework: "static", Content: html, Access: "team"}
	}

	if err := base().Validate(); err != nil {
		t.Errorf("static + content should validate, got: %v", err)
	}

	s := base()
	s.Framework = "streamlit"
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "framework: static") {
		t.Errorf("content on a non-static framework must be rejected, got: %v", err)
	}

	s = base()
	s.Image = "ghcr.io/org/app:1"
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("content + image must be rejected, got: %v", err)
	}

	s = base()
	s.Content = ""
	s.Image = ""
	if err := s.Validate(); err == nil {
		t.Error("neither content nor image must be rejected")
	}
}

func TestContentArtifactSource(t *testing.T) {
	got := ContentArtifactSource("http://minio:9000/", "demo", "rep", "abc123")
	want := "s3::http://minio:9000/abc-reserved/app-content/demo/rep/abc123/"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

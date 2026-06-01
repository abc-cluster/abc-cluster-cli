package jobstage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "PROJECT_ROOT.txt"), []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "analysis", "data", "01_raw", "011_external")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindProjectRoot(deep)
	if err != nil {
		t.Fatal(err)
	}
	// macOS /var → /private/var symlink: compare resolved paths.
	gotR, _ := filepath.EvalSymlinks(got)
	wantR, _ := filepath.EvalSymlinks(root)
	if gotR != wantR {
		t.Errorf("FindProjectRoot = %s, want %s", gotR, wantR)
	}
}

func TestFindProjectRoot_NotFound(t *testing.T) {
	if _, err := FindProjectRoot(t.TempDir()); err == nil {
		t.Error("expected error when no sentinel present")
	}
}

func TestClassifyInput(t *testing.T) {
	root := t.TempDir()
	common := filepath.Join(root, "common-mount") // pretend geesefs mount
	if err := os.MkdirAll(filepath.Join(common, "datasets", "palmerpenguins"), 0o755); err != nil {
		t.Fatal(err)
	}
	penguins := filepath.Join(common, "datasets", "palmerpenguins", "penguins.csv")
	os.WriteFile(penguins, []byte("x"), 0o644)

	// A symlink under the project, resolving into the common mount.
	if err := os.MkdirAll(filepath.Join(root, "analysis", "data", "external"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "analysis", "data", "external", "penguins.csv")
	if err := os.Symlink(penguins, link); err != nil {
		t.Fatal(err)
	}

	// A plain local file under the project.
	localF := filepath.Join(root, "analysis", "data", "02_intermediate", "clean.parquet")
	os.MkdirAll(filepath.Dir(localF), 0o755)
	os.WriteFile(localF, []byte("y"), 0o644)

	cm := CommonMount{Path: common, Bucket: "su-mbhg-hostgen", Prefix: "common/"}

	// s3:// URI → in-minio, referenced verbatim.
	si, err := ClassifyInput("s3://su-mbhg-hostgen/user/calm_dassie/proj/inputs/x.csv", root, "su-mbhg-hostgen", "user/calm_dassie", cm)
	if err != nil || si.Kind != KindInMinIO || si.BucketURI != "s3://su-mbhg-hostgen/user/calm_dassie/proj/inputs/x.csv" {
		t.Errorf("s3 input: kind=%v uri=%s err=%v", si.Kind, si.BucketURI, err)
	}

	// common symlink → KindCommon mapped to the common bucket URI.
	ci, err := ClassifyInput(link, root, "su-mbhg-hostgen", "user/calm_dassie", cm)
	if err != nil {
		t.Fatal(err)
	}
	if ci.Kind != KindCommon {
		t.Errorf("common symlink: kind=%v want common", ci.Kind)
	}
	wantURI := "s3://su-mbhg-hostgen/common/datasets/palmerpenguins/penguins.csv"
	if ci.BucketURI != wantURI {
		t.Errorf("common URI = %s, want %s", ci.BucketURI, wantURI)
	}
	if ci.RelPath != filepath.Join("analysis", "data", "external", "penguins.csv") {
		t.Errorf("common RelPath = %s", ci.RelPath)
	}

	// local file → KindLocalOnly.
	li, err := ClassifyInput(localF, root, "su-mbhg-hostgen", "user/calm_dassie", cm)
	if err != nil {
		t.Fatal(err)
	}
	if li.Kind != KindLocalOnly || li.BucketURI != "" {
		t.Errorf("local file: kind=%v uri=%s want local/empty", li.Kind, li.BucketURI)
	}

	// common detection disabled (empty mount) → symlink treated as local.
	li2, _ := ClassifyInput(link, root, "su-mbhg-hostgen", "user/calm_dassie", CommonMount{})
	if li2.Kind != KindLocalOnly {
		t.Errorf("disabled common: kind=%v want local", li2.Kind)
	}
}

func TestClassifyInput_OutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.csv")
	os.WriteFile(outside, []byte("z"), 0o644)
	if _, err := ClassifyInput(outside, root, "b", "user/s", CommonMount{}); err == nil {
		t.Error("expected error for input outside project root")
	}
}

func TestManifestsAndUploads(t *testing.T) {
	p := Plan{
		ProjectRoot: "/proj",
		RunPrefix:   "s3://su-mbhg-hostgen/user/calm_dassie/penguins/jobs/r123",
		DestRoot:    "/local/r123",
		Inputs: []StagedInput{
			{RelPath: "analysis/data/02_intermediate/clean.parquet", AbsPath: "/proj/analysis/data/02_intermediate/clean.parquet", Kind: KindLocalOnly},
			{RelPath: "analysis/data/external/penguins.csv", Kind: KindCommon, BucketURI: "s3://su-mbhg-hostgen/common/datasets/palmerpenguins/penguins.csv"},
		},
		Outputs: []StagedOutput{
			{RelPath: "analysis/data/06_models/rf.pkl"},
			{RelPath: "analysis/data/07_model_output/"},
		},
	}

	// Local-only input must be uploaded; common input must not.
	ups := p.LocalUploads()
	if len(ups) != 1 {
		t.Fatalf("expected 1 local upload, got %d", len(ups))
	}
	if ups[0][1] != "s3://su-mbhg-hostgen/user/calm_dassie/penguins/jobs/r123/inputs/analysis/data/02_intermediate/clean.parquet" {
		t.Errorf("upload dest URI wrong: %s", ups[0][1])
	}

	in := p.StageInManifest()
	// local-only fetched from per-run inputs prefix; preserves rel tree under DestRoot.
	if !strings.Contains(in, "cp s3://su-mbhg-hostgen/user/calm_dassie/penguins/jobs/r123/inputs/analysis/data/02_intermediate/clean.parquet /local/r123/analysis/data/02_intermediate/clean.parquet") {
		t.Errorf("stage-in missing local-only fetch:\n%s", in)
	}
	// common fetched from its existing bucket URI.
	if !strings.Contains(in, "cp s3://su-mbhg-hostgen/common/datasets/palmerpenguins/penguins.csv /local/r123/analysis/data/external/penguins.csv") {
		t.Errorf("stage-in missing common fetch:\n%s", in)
	}

	out := p.StageOutManifest()
	if !strings.Contains(out, "cp /local/r123/analysis/data/06_models/rf.pkl s3://su-mbhg-hostgen/user/calm_dassie/penguins/jobs/r123/outputs/analysis/data/06_models/rf.pkl") {
		t.Errorf("stage-out missing file upload:\n%s", out)
	}
	// directory output → recursive glob.
	if !strings.Contains(out, "cp /local/r123/analysis/data/07_model_output/* s3://su-mbhg-hostgen/user/calm_dassie/penguins/jobs/r123/outputs/analysis/data/07_model_output/") {
		t.Errorf("stage-out missing dir upload:\n%s", out)
	}

	// run-manifest captures input sources + output dests as lineage.
	m := p.RunManifest("r123", "scripts/python/rf_train.py", "pixi.toml", "inv-abc", []string{"model=rf", "notebook=rf_train"})
	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, want := range []string{`"run_id": "r123"`, `"model=rf"`, "common/datasets/palmerpenguins/penguins.csv", "jobs/r123/outputs/analysis/data/06_models/rf.pkl"} {
		if !strings.Contains(js, want) {
			t.Errorf("run-manifest missing %q:\n%s", want, js)
		}
	}
}

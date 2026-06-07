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

// TestLocalUploads_MixedInputSet builds a Plan from a mixed --in set (one
// local-only file, one already-in-MinIO URI, one common-mount symlink) via
// ClassifyInput and asserts LocalUploads() returns exactly the local-only
// input — the upload-before-submit set (spec acceptance A-D2-unit, A2).
func TestLocalUploads_MixedInputSet(t *testing.T) {
	root := t.TempDir()

	// Common mount + a symlink under the project resolving into it.
	common := filepath.Join(root, "common-mount")
	if err := os.MkdirAll(filepath.Join(common, "datasets"), 0o755); err != nil {
		t.Fatal(err)
	}
	commonFile := filepath.Join(common, "datasets", "ref.csv")
	os.WriteFile(commonFile, []byte("r"), 0o644)
	if err := os.MkdirAll(filepath.Join(root, "data", "external"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "data", "external", "ref.csv")
	if err := os.Symlink(commonFile, link); err != nil {
		t.Fatal(err)
	}

	// A plain local file under the project.
	localF := filepath.Join(root, "data", "clean.parquet")
	os.WriteFile(localF, []byte("y"), 0o644)

	cm := CommonMount{Path: common, Bucket: "su-mbhg-hostgen", Prefix: "common/"}
	bucket, slotPrefix := "su-mbhg-hostgen", "user/calm_dassie"

	ins := []string{
		localF, // local-only → uploaded
		"s3://su-mbhg-hostgen/user/calm_dassie/proj/inputs/already.csv", // in-minio → not uploaded
		link, // common symlink → not uploaded
	}
	p := Plan{
		ProjectRoot: root,
		RunPrefix:   "s3://su-mbhg-hostgen/user/calm_dassie/proj/jobs/r1",
		DestRoot:    "$NOMAD_ALLOC_DIR/data/r1",
	}
	for _, in := range ins {
		si, err := ClassifyInput(in, root, bucket, slotPrefix, cm)
		if err != nil {
			t.Fatalf("ClassifyInput(%q): %v", in, err)
		}
		p.Inputs = append(p.Inputs, si)
	}

	ups := p.LocalUploads()
	if len(ups) != 1 {
		t.Fatalf("expected exactly 1 local upload for a mixed set, got %d: %v", len(ups), ups)
	}
	if ups[0][0] != localF {
		t.Errorf("upload src = %q, want %q", ups[0][0], localF)
	}
	wantDest := p.RunPrefix + "/inputs/data/clean.parquet"
	if ups[0][1] != wantDest {
		t.Errorf("upload dest = %q, want %q", ups[0][1], wantDest)
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
	// local-only fetched from per-run inputs prefix → RELATIVE dest (CWD = DestRoot).
	if !strings.Contains(in, "cp s3://su-mbhg-hostgen/user/calm_dassie/penguins/jobs/r123/inputs/analysis/data/02_intermediate/clean.parquet analysis/data/02_intermediate/clean.parquet") {
		t.Errorf("stage-in missing local-only fetch:\n%s", in)
	}
	// common fetched from its existing bucket URI → RELATIVE dest.
	if !strings.Contains(in, "cp s3://su-mbhg-hostgen/common/datasets/palmerpenguins/penguins.csv analysis/data/external/penguins.csv") {
		t.Errorf("stage-in missing common fetch:\n%s", in)
	}

	out := p.StageOutManifest()
	// RELATIVE source (CWD = DestRoot) → per-run outputs prefix.
	if !strings.Contains(out, "cp analysis/data/06_models/rf.pkl s3://su-mbhg-hostgen/user/calm_dassie/penguins/jobs/r123/outputs/analysis/data/06_models/rf.pkl") {
		t.Errorf("stage-out missing file upload:\n%s", out)
	}
	// directory output → recursive glob (relative source).
	if !strings.Contains(out, "cp analysis/data/07_model_output/* s3://su-mbhg-hostgen/user/calm_dassie/penguins/jobs/r123/outputs/analysis/data/07_model_output/") {
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

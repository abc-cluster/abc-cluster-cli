package pipeline

import "testing"

func TestValidatePipelineRef(t *testing.T) {
	t.Parallel()

	accept := []struct {
		name string
		ref  string
	}{
		{"short github form", "nf-core/demo"},
		{"short github with owner dash", "nextflow-io/hello"},
		{"full https url", "https://github.com/nf-core/demo"},
		{"full http url", "http://github.example.internal/nf-core/demo"},
		{"saved pipeline name", "rnaseq"},
		{"saved pipeline name with dashes", "my-rnaseq-prod"},
		{"github url with revision", "https://github.com/nf-core/demo@3.3.0"},
		{"short form with revision", "nf-core/demo@3.3.0"},
	}

	for _, tc := range accept {
		tc := tc
		t.Run("accept/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validatePipelineRef(tc.ref); err != nil {
				t.Errorf("validatePipelineRef(%q) returned unexpected error: %v", tc.ref, err)
			}
		})
	}

	reject := []struct {
		name string
		ref  string
	}{
		{"absolute path", "/home/user/pipeline"},
		{"absolute nf file", "/home/user/main.nf"},
		{"relative path dot slash", "./main.nf"},
		{"relative path dot dot", "../pipeline/main.nf"},
		{"relative dir no ext", "./my-pipeline"},
		{"nf extension", "main.nf"},
		{"sh extension", "run.sh"},
		{"python script", "workflow.py"},
		{"snakemake file", "Snakefile.smk"},
		{"wdl script", "analysis.wl"},
		{"r script lowercase", "analysis.r"},
		{"R script uppercase", "analysis.R"},
		{"git protocol", "git://github.com/nf-core/demo"},
		{"ssh protocol", "ssh://git@github.com/nf-core/demo"},
		{"file protocol", "file:///home/user/pipeline"},
		{"bare github hostname", "github.com/nf-core/demo"},
		{"bare gitlab hostname", "gitlab.com/owner/repo"},
		{"bare bitbucket hostname", "bitbucket.org/owner/repo"},
		{"bare hostname no path", "github.com"},
		{"saved name with dot", "my.pipeline"},
	}

	for _, tc := range reject {
		tc := tc
		t.Run("reject/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validatePipelineRef(tc.ref); err == nil {
				t.Errorf("validatePipelineRef(%q) expected error but got nil", tc.ref)
			}
		})
	}
}

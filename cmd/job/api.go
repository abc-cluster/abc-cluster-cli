package job

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ScriptHCLOptions configures BuildScriptHCL. Zero values leave the
// corresponding preamble directive or env-var setting unchanged.
type ScriptHCLOptions struct {
	Name      string
	Namespace string
	Cores     int
	MemoryMB  int
	Conda     string
	Runtime   string
	From      string
	TaskTmp   bool
}

// ScriptHCLResult holds the output of BuildScriptHCL.
type ScriptHCLResult struct {
	HCL       string
	JobName   string
	Namespace string
}

// BuildScriptHCL parses an annotated script file and returns the corresponding
// Nomad HCL. ScriptHCLOptions fields override preamble directives; zero values
// are ignored.
func BuildScriptHCL(scriptPath string, opts ScriptHCLOptions) (*ScriptHCLResult, error) {
	f, err := os.Open(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open script %q: %w", scriptPath, err)
	}
	defer f.Close()

	scriptBytes, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("cannot read script %q: %w", scriptPath, err)
	}

	abcDirs, nomadDirs, slurmDirs, pbsDirs, err := parsePreamble(bytes.NewReader(scriptBytes))
	if err != nil {
		return nil, fmt.Errorf("parsing preamble in %q: %w", scriptPath, err)
	}

	scriptBase := filepath.Base(scriptPath)
	defaultName := strings.TrimSuffix(scriptBase, filepath.Ext(scriptBase))

	scriptSpec, err := resolveSpec(abcDirs, nomadDirs, slurmDirs, pbsDirs, defaultName, preambleModeAuto)
	if err != nil {
		return nil, err
	}

	spec := mergeSpec(scriptSpec, readNomadEnvVars())

	if opts.Name != "" {
		spec.Name = opts.Name
	}
	if opts.Namespace != "" {
		spec.Namespace = opts.Namespace
	}
	if opts.Cores != 0 {
		spec.Cores = opts.Cores
	}
	if opts.MemoryMB != 0 {
		spec.MemoryMB = opts.MemoryMB
	}
	if opts.Conda != "" {
		spec.Conda = opts.Conda
		if spec.Meta == nil {
			spec.Meta = map[string]string{}
		}
		spec.Meta["abc_conda"] = opts.Conda
	}
	if opts.Runtime != "" {
		spec.Runtime = opts.Runtime
	}
	if opts.From != "" {
		spec.From = opts.From
	}
	if opts.TaskTmp {
		spec.TaskTmp = true
	}
	syncStackMetaKeys(spec)
	syncTaskTmpMeta(spec)

	if spec.Meta == nil {
		spec.Meta = map[string]string{}
	}
	submissionID := newSubmissionID()
	spec.Meta["abc_submission_id"] = submissionID
	spec.Meta["abc_submission_time"] = time.Now().UTC().Format(time.RFC3339)
	if spec.Name != "" {
		base := spec.Name
		if !strings.HasPrefix(base, "script-job-") {
			base = "script-job-" + base
		}
		spec.Name = fmt.Sprintf("%s-%s", base, submissionID[:8])
	}

	scriptBody := string(scriptBytes)
	scriptBody, err = FinalizeJobScript(spec, scriptBase, scriptBody)
	if err != nil {
		return nil, err
	}

	hcl := generateHCL(spec, scriptBase, scriptBody)
	return &ScriptHCLResult{
		HCL:       hcl,
		JobName:   spec.Name,
		Namespace: spec.Namespace,
	}, nil
}

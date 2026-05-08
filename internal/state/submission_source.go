package state

import (
	"os"
	"strings"
)

// SubmissionSourceClassifier resolves the submission_source string for a
// fresh runs row. Inputs come from the verb's flag set:
//
//   - templateID: from --template=<id> (or equivalent), if the verb
//     supports it. Empty when no template.
//   - rerun:      true when the verb is `abc job rerun` /
//     `abc pipeline rerun` / `abc module rerun`. None of those verbs
//     exist today; reserved for the rerun work.
//
// Resolution precedence:
//   1. rerun=true                                → "rerun"
//   2. ABC_AUTOMATION=1 in env                    → "automation"
//   3. templateID != ""                           → "template:<id>"
//   4. otherwise                                  → "handwritten"
//
// Spec abc-report.md §B + migration 0010.
type SubmissionSourceClassifier struct {
	TemplateID string
	Rerun      bool
}

// Resolve returns the submission_source value for this submit invocation.
// Always returns a non-empty string; callers can pass the result straight
// into AutoAttachRequest.SubmissionSource.
func (c SubmissionSourceClassifier) Resolve() string {
	if c.Rerun {
		return "rerun"
	}
	if strings.TrimSpace(os.Getenv("ABC_AUTOMATION")) == "1" {
		return "automation"
	}
	if c.TemplateID != "" {
		return "template:" + c.TemplateID
	}
	return "handwritten"
}

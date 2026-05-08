// Package editor invokes $EDITOR with a template, returning the edited body.
package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const annotationTemplate = `# Annotation — write your note below.
# The first line starting with "tag:" sets the annotation tag (optional).
tag: %s

`

// AnnotationEditExisting opens $EDITOR pre-filled with currentBody under a
// front-matter `tag:` line, and returns the parsed (body, tag) result.
// Used by `abc project investigation annotation edit` for in-place edits.
func AnnotationEditExisting(currentTag, currentBody string) (string, string, error) {
	return runEditor(fmt.Sprintf(annotationTemplate, currentTag) + currentBody + "\n")
}

// AnnotationEditor opens $EDITOR (or nano) and returns (body, tag).
// Body is everything below the front-matter; comment lines starting with # are stripped.
func AnnotationEditor(initialTag string) (string, string, error) {
	return runEditor(fmt.Sprintf(annotationTemplate, initialTag))
}

func runEditor(seed string) (string, string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano"
	}
	if _, err := exec.LookPath(strings.Fields(editor)[0]); err != nil {
		return "", "", fmt.Errorf("editor %q not found on PATH; set $EDITOR or use --note", editor)
	}
	tmp, err := os.CreateTemp("", "abc-annotation-*.md")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	_, _ = tmp.WriteString(seed)
	tmp.Close()

	parts := strings.Fields(editor)
	parts = append(parts, tmpPath)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("editor %s exited: %w", filepath.Base(parts[0]), err)
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", "", err
	}
	tag := ""
	body := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "tag:") {
			tag = strings.TrimSpace(strings.TrimPrefix(trim, "tag:"))
			continue
		}
		body = append(body, line)
	}
	return strings.TrimSpace(strings.Join(body, "\n")), tag, nil
}

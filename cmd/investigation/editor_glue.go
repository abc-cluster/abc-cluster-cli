package investigation

import "github.com/abc-cluster/abc-cluster-cli/internal/editor"

// openEditorTemplate is a thin wrapper that lets tests replace the editor
// invocation. Returns (body, tag).
var openEditorTemplate = func(initialTag string) (string, string, error) {
	return editor.AnnotationEditor(initialTag)
}

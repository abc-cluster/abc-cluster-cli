// Annotation management subgroup: `abc project investigation annotation`.
//
// Stage 1 placeholder; Stage 2 fills in list/show/edit/tag/move/withdraw/restore
// once migrations 0006 (annotation_revisions) and 0007 (annotations.tags_json
// + withdrawn_at + updated_at) land.
package investigation

import "github.com/spf13/cobra"

// newAnnotationCmd registers the `annotation` subgroup. Stage 2 will append
// list/show/edit/tag/move/withdraw/restore subcommands.
func newAnnotationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "annotation",
		Short: "Manage annotations on an investigation (list, show, edit, tag, move, withdraw, restore)",
	}
	return cmd
}

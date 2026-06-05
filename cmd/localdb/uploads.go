package localdb

// uploads.go — `abc localdb uploads`: browse the local registry of files
// recorded by `abc data upload`. Relocated from `abc data uploads` (it reads
// the local SQLite, so it belongs with the other localdb inspectors, not under
// the user-facing data verbs).

import (
	"errors"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// newUploadsCmd returns the "uploads" subcommand group under "abc localdb".
// It groups three read-only registry commands: list, show, path.
func newUploadsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uploads",
		Short: "Browse files recorded by 'abc data upload'",
		Long: `Browse the upload registry — files recorded by 'abc data upload' on
successful completion. The registry lives in the local database and tracks
filename, storage path, size, checksum, upload time, and the active context.

Use 'abc localdb uploads path <filename>' to retrieve the storage path for use
in pipeline --input parameters or job scripts:

  abc pipeline run nf-core/rnaseq \
    --input "$(abc localdb uploads path samples.csv)" \
    --genome GRCh38`,
	}
	cmd.AddCommand(newUploadsListCmd())
	cmd.AddCommand(newUploadsShowCmd())
	cmd.AddCommand(newUploadsPathCmd())
	return cmd
}

func newUploadsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List uploaded files",
		Long: `Print a table of files uploaded via 'abc data upload', newest first.

  abc localdb uploads list
  abc localdb uploads list --context seedling
  abc localdb uploads list --limit 100`,
		RunE: runUploadsList,
	}
	cmd.Flags().String("context", "", "Filter to a specific context name (default: all contexts)")
	cmd.Flags().Int("limit", 50, "Maximum number of rows to show (0 = unlimited)")
	return cmd
}

func newUploadsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <filename>",
		Short: "Show details for an uploaded file",
		Long: `Print the storage path, size, checksum, and upload time for the most
recent upload matching <filename>.

  abc localdb uploads show samples.csv`,
		Args: cobra.ExactArgs(1),
		RunE: runUploadsShow,
	}
}

func newUploadsPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path <filename>",
		Short: "Print the storage path for an uploaded file",
		Long: `Print the canonical storage path for the most recent upload matching
<filename>. Designed for scripting — outputs a bare path with no decoration.

  abc localdb uploads path samples.csv

  abc pipeline run nf-core/rnaseq \
    --input "$(abc localdb uploads path samples.csv)"`,
		Args: cobra.ExactArgs(1),
		RunE: runUploadsPath,
	}
}

func runUploadsList(cmd *cobra.Command, _ []string) error {
	ctxFilter, _ := cmd.Flags().GetString("context")
	limit, _ := cmd.Flags().GetInt("limit")

	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local DB: %w", err)
	}

	uploads, err := state.ListUploads(db, ctxFilter)
	if err != nil {
		return fmt.Errorf("list uploads: %w", err)
	}

	if len(uploads) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  No uploads recorded.")
		fmt.Fprintln(cmd.OutOrStdout(), "  Run 'abc data upload <file>' to upload a file and register it here.")
		return nil
	}

	shown := uploads
	if limit > 0 && len(uploads) > limit {
		shown = uploads[:limit]
	}

	out := cmd.OutOrStdout()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  FILENAME\tSIZE\tUPLOADED\tCONTEXT\tSTORAGE PATH")
	fmt.Fprintln(w, "  ────────────────────\t────────\t─────────────────────\t─────────────\t────────────────────────────────────────────────")
	for _, u := range shown {
		path := u.S3Path
		if path == "" {
			path = "(unknown)"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n",
			u.Filename,
			formatBytes(u.SizeBytes),
			u.UploadedAt.Format("2006-01-02 15:04:05"),
			u.ContextName,
			path,
		)
	}
	w.Flush()

	if limit > 0 && len(uploads) > limit {
		fmt.Fprintf(out, "\n  (showing %d of %d; use --limit to adjust)\n", limit, len(uploads))
	}
	return nil
}

func runUploadsShow(cmd *cobra.Command, args []string) error {
	filename := args[0]
	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local DB: %w", err)
	}

	u, err := state.FindUploadByFilename(db, filename, "")
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("no upload recorded for %q\n  Run 'abc data upload %s' first, or 'abc localdb uploads list' to browse all uploads", filename, filename)
		}
		return fmt.Errorf("find upload: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  File:         %s\n", u.Filename)
	if u.S3Path != "" {
		fmt.Fprintf(out, "  Storage path: %s\n", u.S3Path)
	} else {
		fmt.Fprintf(out, "  Storage path: (unknown — context lacked storage info at upload time)\n")
	}
	fmt.Fprintf(out, "  Size:         %s\n", formatBytes(u.SizeBytes))
	if u.Checksum != "" {
		fmt.Fprintf(out, "  Checksum:     %s\n", u.Checksum)
	}
	fmt.Fprintf(out, "  Uploaded:     %s\n", u.UploadedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(out, "  Context:      %s\n", u.ContextName)
	if u.OriginalPath != "" {
		fmt.Fprintf(out, "  Local path:   %s\n", u.OriginalPath)
	}
	fmt.Fprintln(out)
	return nil
}

func runUploadsPath(cmd *cobra.Command, args []string) error {
	filename := args[0]
	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local DB: %w", err)
	}

	u, err := state.FindUploadByFilename(db, filename, "")
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("no upload recorded for %q\n  Run 'abc data upload %s' first, or 'abc localdb uploads list' to browse all uploads", filename, filename)
		}
		return fmt.Errorf("find upload: %w", err)
	}

	if u.S3Path == "" {
		return fmt.Errorf("storage path unknown for %q\n  The active context lacked storage info when this file was uploaded.\n  Run 'abc data ls' to browse the store directly.", filename)
	}

	fmt.Fprintln(cmd.OutOrStdout(), u.S3Path)
	return nil
}

// formatBytes renders a byte count as a human-readable size.
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

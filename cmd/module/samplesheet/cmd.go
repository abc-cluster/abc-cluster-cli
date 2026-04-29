package samplesheet

import (
	"github.com/spf13/cobra"
)

// NewCmd returns the `module samplesheet` cobra group. Registered under
// the `module` command in cmd/module/cmd.go.
//
// Today the only verb is `emit`. The matching `validate` operation is
// folded into `abc module run --samplesheet PATH` (it runs cluster-side
// in the prestart task) so users get one obvious place to wire a
// samplesheet through. We can grow a standalone `validate` here if a
// cheap-no-submit dry-run becomes valuable.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "samplesheet",
		Short: "Tools for module-driver samplesheets",
		Long: `Tools for working with samplesheets that feed an nf-core module
driver run.

Subcommands:
  emit    Submit a small Nomad batch job that scaffolds a starter CSV
          from the module's bundled tests/main.nf.test fixtures and
          downloads it locally for editing.

To validate an edited samplesheet, pass it through the normal run flow:
  abc module run nf-core/<mod> --samplesheet ./my.csv
The prestart task validates it against the module's meta.yml before
generating the driver, so a malformed sheet fails fast.`,
	}
	cmd.AddCommand(newEmitCmd())
	return cmd
}

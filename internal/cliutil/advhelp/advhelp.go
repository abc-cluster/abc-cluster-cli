// Package advhelp adds progressive disclosure to a cobra command's --help.
//
// Default `--help` hides flags listed as advanced and appends a footer
// pointing the user at `--help --advanced`, which prints the full set.
//
// Hidden flags still parse normally — only their visibility in help and
// shell completion changes. If shell completion of advanced flags becomes
// important, replace MarkHidden with a custom completion func.
package advhelp

import (
	"fmt"

	"github.com/spf13/cobra"
)

const (
	advancedFlag = "advanced"
	footer       = "\nRun with --help --advanced to see all flags.\n"
)

// Register hides the named flags from default help and installs a custom
// HelpFunc that unhides them when --advanced is set. Safe to call with an
// empty list (no-op). Panics if the cmd already defines an --advanced flag,
// to surface collisions at startup rather than silently shadowing.
func Register(cmd *cobra.Command, advancedFlagNames []string) {
	if len(advancedFlagNames) == 0 {
		return
	}
	if cmd.Flags().Lookup(advancedFlag) != nil {
		panic(fmt.Sprintf("advhelp: command %q already defines --%s", cmd.Name(), advancedFlag))
	}

	for _, name := range advancedFlagNames {
		if err := cmd.Flags().MarkHidden(name); err != nil {
			panic(fmt.Sprintf("advhelp: cannot hide flag --%s on %q: %v", name, cmd.Name(), err))
		}
	}

	cmd.Flags().Bool(advancedFlag, false, "Show advanced flags in --help output")
	_ = cmd.Flags().MarkHidden(advancedFlag)

	orig := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		showAdvanced, _ := c.Flags().GetBool(advancedFlag)
		if !showAdvanced {
			for _, a := range args {
				if a == "--"+advancedFlag {
					showAdvanced = true
					break
				}
			}
		}

		if showAdvanced {
			restore := unhide(c, advancedFlagNames)
			defer restore()
			orig(c, args)
			return
		}

		orig(c, args)
		fmt.Fprint(c.OutOrStderr(), footer)
	})
}

func unhide(cmd *cobra.Command, names []string) func() {
	prev := make(map[string]bool, len(names))
	for _, n := range names {
		f := cmd.Flags().Lookup(n)
		if f == nil {
			continue
		}
		prev[n] = f.Hidden
		f.Hidden = false
	}
	return func() {
		for n, hidden := range prev {
			if f := cmd.Flags().Lookup(n); f != nil {
				f.Hidden = hidden
			}
		}
	}
}

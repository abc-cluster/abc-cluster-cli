package portal

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Short:   "List all portals with their URLs",
		Aliases: []string{"list"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			ctx := cfg.ActiveCtx()
			urls, err := DeriveURLs(ctx)
			if err != nil {
				return err
			}
			printPortalTable(urls)
			return nil
		},
	}
}

func printPortalTable(urls PortalURLs) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "PORTAL\tSERVICE\tURL\tAUTH")
	for _, p := range allPortals {
		u, _ := urls.URL(p.Name)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name, p.Service, u, p.AuthHow)
	}
	w.Flush()
	fmt.Fprintf(os.Stderr, "\nOpen any portal with: abc portal open <name>\n")
}

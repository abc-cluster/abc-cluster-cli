package config

import (
	"fmt"
	"os"
	"time"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// absorbedEntry records what happened to one absorbed context.
type absorbedEntry struct {
	original string
	final    string
}

// absorbConfigs merges all contexts (and eligible aliases) from incoming into dest.
// It returns one entry per absorbed context describing any rename that occurred.
// dest is modified in place; the caller is responsible for saving it.
func absorbConfigs(dest, incoming *cfg.Config) []absorbedEntry {
	today := time.Now().Format("20060102")
	var entries []absorbedEntry

	// Build a map of original→final names so we can fix up alias targets later.
	nameMap := make(map[string]string) // incoming primary name -> final name in dest

	for name, ctx := range incoming.Contexts {
		finalName := name
		if dest.HasDefinedContext(name) {
			// Find first free <name>-<n>-<date>
			for n := 1; ; n++ {
				candidate := fmt.Sprintf("%s-%d-%s", name, n, today)
				if !dest.HasDefinedContext(candidate) {
					finalName = candidate
					break
				}
			}
		}
		nameMap[name] = finalName
		dest.Contexts[finalName] = ctx
		entries = append(entries, absorbedEntry{original: name, final: finalName})
	}

	// Absorb aliases from incoming that point at a context we just absorbed.
	// Skip any alias key that already exists (as a primary name or alias key) in dest.
	for aliasKey, targetName := range incoming.ContextAliases {
		// Only absorb this alias if the target was one of the absorbed contexts.
		finalTarget, ok := nameMap[targetName]
		if !ok {
			continue
		}
		// Skip if the key already exists in dest as a primary name or alias.
		if dest.HasDefinedContext(aliasKey) {
			continue
		}
		if _, exists := dest.ContextAliases[aliasKey]; exists {
			continue
		}
		dest.ContextAliases[aliasKey] = finalTarget
	}

	return entries
}

func newAbsorbCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "absorb <new-config.yaml>",
		Short: "Merge contexts from another config file into the current config",
		Long: `Read contexts from <new-config.yaml> and add them to the current config.

If a context name already exists in the current config, the incoming context is
renamed to <name>-<n>-<date> (where <date> is YYYYMMDD and <n> increments until
the name is free). active_context in the current config is never changed.

Context aliases from the incoming file are absorbed only when their target
context was itself absorbed and when the alias key does not already exist.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			incomingPath := args[0]

			incoming, err := cfg.LoadFrom(incomingPath)
			if err != nil {
				return fmt.Errorf("absorb: %w", err)
			}
			if err := incoming.Validate(); err != nil {
				return fmt.Errorf("absorb: incoming config is invalid: %w", err)
			}

			dest, err := cfg.Load()
			if err != nil {
				return fmt.Errorf("absorb: %w", err)
			}

			entries := absorbConfigs(dest, incoming)

			if err := dest.Save(); err != nil {
				return fmt.Errorf("absorb: %w", err)
			}

			for _, e := range entries {
				if e.original == e.final {
					fmt.Fprintf(os.Stderr, "  ✓ absorbed context %q → saved as %q\n", e.original, e.final)
				} else {
					fmt.Fprintf(os.Stderr, "  ✓ absorbed context %q → saved as %q (renamed)\n", e.original, e.final)
				}
			}

			return nil
		},
	}
}

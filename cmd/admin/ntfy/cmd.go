// Package ntfy provides the "abc admin services ntfy" subcommand group.
package ntfy

import (
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

// NewCmd returns the "ntfy" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ntfy",
		Short: "ntfy push notification helpers",
		Long: `Commands for managing push notifications via the ntfy server on an abc-nodes cluster.

  abc admin services ntfy send abc-jobs "Maintenance window at 22:00" --title "Cluster Notice"
  abc admin services ntfy list abc-jobs --since 2h
  abc admin services ntfy topics
  abc admin services ntfy cli -- pub abc-jobs "hello"`,
	}
	cmd.AddCommand(newSendCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newTopicsCmd())
	cmd.AddCommand(newCLICmd())
	return cmd
}

func ntfyClient(cfg *config.Config) (*floor.NtfyClient, string, error) {
	ctx := cfg.ActiveCtx()
	ntfyHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "ntfy", "http")
	if !ok || ntfyHTTP == "" {
		return nil, "", fmt.Errorf(
			"ntfy URL not configured for context %q\n"+
				"  Run: abc cluster capabilities sync",
			cfg.ActiveContext,
		)
	}
	return floor.NewNtfyClient(ntfyHTTP), ntfyHTTP, nil
}

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <topic> <message>",
		Short: "Publish a message to an ntfy topic",
		Args:  cobra.ExactArgs(2),
		RunE:  runSend,
	}
	cmd.Flags().String("title", "", "Notification title")
	cmd.Flags().Int("priority", 3, "Priority 1 (min) to 5 (urgent)")
	cmd.Flags().StringSlice("tags", nil, "Comma-separated tags (e.g. warning,nomad)")
	return cmd
}

func runSend(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	nc, ntfyHTTP, err := ntfyClient(cfg)
	if err != nil {
		return err
	}

	topic := args[0]
	message := args[1]
	title, _ := cmd.Flags().GetString("title")
	priority, _ := cmd.Flags().GetInt("priority")
	tags, _ := cmd.Flags().GetStringSlice("tags")

	if err := nc.Publish(cmd.Context(), topic, message, title, priority, tags); err != nil {
		return fmt.Errorf("ntfy send: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "  Sent to %s/%s\n", strings.TrimRight(ntfyHTTP, "/"), topic)
	return nil
}

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <topic>",
		Short: "List recent messages from an ntfy topic",
		Args:  cobra.ExactArgs(1),
		RunE:  runList,
	}
	cmd.Flags().String("since", "1h", "Show messages since this time: duration (e.g. 2h) or unix timestamp")
	return cmd
}

func runList(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	nc, _, err := ntfyClient(cfg)
	if err != nil {
		return err
	}

	topic := args[0]
	since, _ := cmd.Flags().GetString("since")

	messages, err := nc.ListMessages(cmd.Context(), topic, since)
	if err != nil {
		return fmt.Errorf("ntfy list: %w", err)
	}

	if len(messages) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  No messages in %s (since %s)\n", topic, since)
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  %d message(s) in %s:\n\n", len(messages), topic)
	for _, m := range messages {
		ts := m.Time.Format("2006-01-02 15:04:05")
		title := m.Title
		if title == "" {
			title = "(no title)"
		}
		tags := ""
		if len(m.Tags) > 0 {
			tags = " [" + strings.Join(m.Tags, ",") + "]"
		}
		fmt.Fprintf(out, "  %s  p%d  %s%s\n  %s\n\n", ts, m.Priority, title, tags, m.Message)
	}
	return nil
}

func newTopicsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "topics",
		Short: "Print the ntfy base URL and known abc-nodes topics",
		RunE:  runTopics,
	}
}

func runTopics(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	_, ntfyHTTP, err := ntfyClient(cfg)
	if err != nil {
		return err
	}

	base := strings.TrimRight(ntfyHTTP, "/")
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  ntfy server: %s\n\n", base)
	fmt.Fprintf(out, "  %-20s %s\n", "TOPIC", "URL")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 60))
	for _, t := range []string{"abc-jobs", "abc-pipelines", "abc-alerts"} {
		fmt.Fprintf(out, "  %-20s %s/%s\n", t, base, t)
	}
	fmt.Fprintf(out, "\n  Subscribe with the ntfy app (iOS/Android/Desktop) or:\n")
	fmt.Fprintf(out, "    curl -s %s/abc-jobs/json?poll=1\n\n", base)
	return nil
}

package storage

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

type sizeOptions struct {
	servers    bool
	buckets    bool
	all        bool
	nomadAddr  string
	nomadToken string
	region     string
	namespace  string
}

func newSizeCmd() *cobra.Command {
	opts := &sizeOptions{}

	cmd := &cobra.Command{
		Use:   "size",
		Short: "Show storage size and usage",
		Long:  "Display local server and bucket storage size, usage, and availability.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSize(cmd, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.servers, "servers", false, "Show server-local storage sizes")
	cmd.Flags().BoolVar(&opts.buckets, "buckets", false, "Show bucket storage sizes")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Show all storage categories")
	cmd.Flags().StringVar(&opts.nomadAddr, "nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"), "Nomad API address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.Flags().StringVar(&opts.nomadToken, "nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"), "Nomad ACL token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.Flags().StringVar(&opts.region, "region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"), "Nomad region (or set ABC_REGION/NOMAD_REGION)")
	cmd.Flags().StringVar(&opts.namespace, "namespace", "", "Nomad namespace")

	return cmd
}

func runSize(cmd *cobra.Command, opts *sizeOptions) error {
	if !opts.servers && !opts.buckets && !opts.all {
		opts.all = true
	}

	if opts.servers || opts.all {
		if err := printServerSizes(cmd.Context(), opts); err != nil {
			return err
		}
	}

	if opts.buckets || opts.all {
		if err := printBucketSizes(cmd.Context(), opts); err != nil {
			return err
		}
	}

	return nil
}

func printServerSizes(ctx context.Context, opts *sizeOptions) error {
	nc := utils.NewNomadClient(opts.nomadAddr, opts.nomadToken, opts.region)

	nodes, err := nc.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	if len(nodes) == 0 {
		fmt.Println("No nodes found")
		return nil
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})

	fmt.Println("SERVER STORAGE:")
	for _, stub := range nodes {
		node, err := nc.GetNode(ctx, stub.ID)
		if err != nil {
			fmt.Printf("%s: failed to fetch node detail: %v\n", stub.Name, err)
			continue
		}

		var totalFill, reserved int
		if node.NodeResources != nil {
			totalFill = node.NodeResources.DiskMB
		}
		if node.ReservedResources != nil {
			reserved = node.ReservedResources.DiskMB
		}

		if totalFill == 0 {
			fmt.Printf("%s: capacity unknown\n", node.Name)
			continue
		}

		free := totalFill - reserved
		if free < 0 {
			free = 0
		}

		fmt.Printf("%s: %s used / %s total (%s free)\n", node.Name,
			formatMB(int64(reserved)), formatMB(int64(totalFill)), formatMB(int64(free)))
	}
	return nil
}

func printBucketSizes(ctx context.Context, opts *sizeOptions) error {
	nc := utils.NewNomadClient(opts.nomadAddr, opts.nomadToken, opts.region)
	vars, err := nc.ListVariables(ctx, "storage/buckets", opts.namespace)
	if err != nil {
		return fmt.Errorf("failed to list bucket vars: %w", err)
	}

	if len(vars) == 0 {
		fmt.Println("No buckets configured in Nomad variables path 'storage/buckets'")
		return nil
	}

	fmt.Println("BUCKET STORAGE:")
	for _, stub := range vars {
		v, err := nc.GetVariable(ctx, stub.Path, opts.namespace)
		if err != nil {
			fmt.Printf("%s: failed to load variable: %v\n", stub.Path, err)
			continue
		}

		name := stub.Path
		if parts := strings.Split(stub.Path, "/"); len(parts) > 0 {
			name = parts[len(parts)-1]
		}

		sizeStr, usedStr := v.Items["size"], v.Items["used"]
		if sizeStr == "" {
			fmt.Printf("%s: no size metadata (items=%v)\n", name, v.Items)
			continue
		}

		sizeMB, _ := parseMB(sizeStr)
		usedMB, _ := parseMB(usedStr)

		fmt.Printf("%s: %s used / %s total\n", name, formatMB(usedMB), formatMB(sizeMB))
	}
	return nil
}

func formatMB(mb int64) string {
	if mb < 1024 {
		return fmt.Sprintf("%d MB", mb)
	}
	if mb < 1024*1024 {
		return fmt.Sprintf("%.1f GB", float64(mb)/1024.0)
	}
	return fmt.Sprintf("%.2f TB", float64(mb)/(1024.0*1024.0))
}

func parseMB(v string) (int64, error) {
	if v == "" {
		return 0, nil
	}
	v = strings.TrimSpace(v)
	if strings.HasSuffix(v, "TB") {
		p, err := strconv.ParseFloat(strings.TrimSuffix(v, "TB"), 64)
		if err != nil {
			return 0, err
		}
		return int64(p * 1024 * 1024), nil
	}
	if strings.HasSuffix(v, "GB") {
		p, err := strconv.ParseFloat(strings.TrimSuffix(v, "GB"), 64)
		if err != nil {
			return 0, err
		}
		return int64(p * 1024), nil
	}
	if strings.HasSuffix(v, "MB") {
		p, err := strconv.ParseFloat(strings.TrimSuffix(v, "MB"), 64)
		if err != nil {
			return 0, err
		}
		return int64(p), nil
	}
	return strconv.ParseInt(v, 10, 64)
}

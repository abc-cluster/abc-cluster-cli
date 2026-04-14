package compute

import (
	"bytes"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

var (
	defaultDockerExtraLabels = []string{
		"job_name",
		"job_id",
		"task_group_name",
		"task_name",
		"namespace",
		"node_name",
		"node_id",
	}

	// HashiCorp-supported external task driver plugins from Nomad task-driver docs.
	hashicorpTaskDriverPlugins = []string{
		"exec2",
		"podman",
		"virt",
	}

	// Community task drivers from Nomad community plugin docs reference.
	communityTaskDriverPlugins = []string{
		"containerd",
		"firecracker-task-driver",
		"jail-task-driver",
		"lightrun",
		"pledge",
		"pot",
		"rookout",
		"singularity",
		"nspawn",
		"iis",
		"nomad-iis",
	}
)

// NomadHostVolume represents a Nomad client host_volume block.
type NomadHostVolume struct {
	Name     string
	Path     string
	ReadOnly bool
}

// NodeConfig holds the parameters for generating a Nomad client HCL config.
type NodeConfig struct {
	Datacenter                    string
	DataDir                       string
	PluginDir                     string
	AdditionalDriverPlugins       []string
	NodeClass                     string
	NetworkInterface              string
	CNIPath                       string
	ServerJoin                    []string // --server-join addresses → client/server server_join.retry_join
	HostVolumes                   []NomadHostVolume
	EnableContainerdDriver        bool
	EnableExec2Driver             bool
	EnableJavaDriver              bool
	ContainerdDriverRuntime       string
	ContainerdDriverStatsInterval string
	Encrypt                       string
	ACL                           bool
	Address                       string
	Advertise                     string
	CAFile                        string
	CertFile                      string
	KeyFile                       string
	ServerMode                    bool // also enable server stanza (advanced)
}

// GenerateClientHCL emits a Nomad client configuration file using hclwrite.
// Follows the same hclwrite pattern as cmd/job/hclgen.go.
func GenerateClientHCL(cfg NodeConfig) string {
	if cfg.DataDir == "" {
		cfg.DataDir = "/opt/nomad/data"
	}
	if cfg.Datacenter == "" {
		cfg.Datacenter = "default"
	}

	f := hclwrite.NewEmptyFile()
	root := f.Body()

	root.SetAttributeValue("datacenter", cty.StringVal(cfg.Datacenter))
	root.SetAttributeValue("data_dir", cty.StringVal(cfg.DataDir))
	if cfg.PluginDir != "" {
		root.SetAttributeValue("plugin_dir", cty.StringVal(cfg.PluginDir))
	}
	root.AppendNewline()

	// Optional bind addresses
	if cfg.Address != "" {
		addrBody := root.AppendNewBlock("addresses", nil).Body()
		addrBody.SetAttributeValue("http", cty.StringVal(cfg.Address))
		addrBody.SetAttributeValue("rpc", cty.StringVal(cfg.Address))
		addrBody.SetAttributeValue("serf", cty.StringVal(cfg.Address))
		root.AppendNewline()
	}

	// Optional advertise addresses (needed when behind NAT)
	if cfg.Advertise != "" {
		advBody := root.AppendNewBlock("advertise", nil).Body()
		advBody.SetAttributeValue("http", cty.StringVal(cfg.Advertise))
		advBody.SetAttributeValue("rpc", cty.StringVal(cfg.Advertise))
		advBody.SetAttributeValue("serf", cty.StringVal(cfg.Advertise))
		root.AppendNewline()
	}

	// Client stanza (always enabled for client mode)
	clientBody := root.AppendNewBlock("client", nil).Body()
	clientBody.SetAttributeValue("enabled", cty.BoolVal(true))
	if cfg.NodeClass != "" {
		clientBody.SetAttributeValue("node_class", cty.StringVal(cfg.NodeClass))
	}
	if cfg.NetworkInterface != "" {
		clientBody.SetAttributeValue("network_interface", cty.StringVal(cfg.NetworkInterface))
	}
	if cfg.CNIPath != "" {
		clientBody.SetAttributeValue("cni_path", cty.StringVal(cfg.CNIPath))
	}
	if len(cfg.ServerJoin) > 0 {
		addrs := make([]cty.Value, len(cfg.ServerJoin))
		for i, a := range cfg.ServerJoin {
			addrs[i] = cty.StringVal(a)
		}
		sjBody := clientBody.AppendNewBlock("server_join", nil).Body()
		sjBody.SetAttributeValue("retry_join", cty.ListVal(addrs))
	}
	for _, v := range cfg.HostVolumes {
		name := strings.TrimSpace(v.Name)
		path := strings.TrimSpace(v.Path)
		if name == "" || path == "" {
			continue
		}
		hostVol := clientBody.AppendNewBlock("host_volume", []string{name}).Body()
		hostVol.SetAttributeValue("path", cty.StringVal(path))
		hostVol.SetAttributeValue("read_only", cty.BoolVal(v.ReadOnly))
	}
	root.AppendNewline()

	// Server stanza (advanced — omitted for pure client nodes)
	if cfg.ServerMode {
		serverBody := root.AppendNewBlock("server", nil).Body()
		serverBody.SetAttributeValue("enabled", cty.BoolVal(true))
		if len(cfg.ServerJoin) > 0 {
			addrs := make([]cty.Value, len(cfg.ServerJoin))
			for i, a := range cfg.ServerJoin {
				addrs[i] = cty.StringVal(a)
			}
			sjBody := serverBody.AppendNewBlock("server_join", nil).Body()
			sjBody.SetAttributeValue("retry_join", cty.ListVal(addrs))
		}
		root.AppendNewline()
	}

	// Native task drivers that can be explicitly configured in generated config.
	appendDefaultTaskDriverConfig(root, cfg)

	// Gossip encryption key
	if cfg.Encrypt != "" {
		root.SetAttributeValue("encrypt", cty.StringVal(cfg.Encrypt))
		root.AppendNewline()
	}

	// ACL
	if cfg.ACL {
		aclBody := root.AppendNewBlock("acl", nil).Body()
		aclBody.SetAttributeValue("enabled", cty.BoolVal(true))
		root.AppendNewline()
	}

	// TLS — populated from --ca-file / --cert-file / --key-file
	if cfg.CAFile != "" || cfg.CertFile != "" || cfg.KeyFile != "" {
		tlsBody := root.AppendNewBlock("tls", nil).Body()
		tlsBody.SetAttributeValue("http", cty.BoolVal(true))
		tlsBody.SetAttributeValue("rpc", cty.BoolVal(true))
		if cfg.CAFile != "" {
			tlsBody.SetAttributeValue("ca_file", cty.StringVal(cfg.CAFile))
		}
		if cfg.CertFile != "" {
			tlsBody.SetAttributeValue("cert_file", cty.StringVal(cfg.CertFile))
		}
		if cfg.KeyFile != "" {
			tlsBody.SetAttributeValue("key_file", cty.StringVal(cfg.KeyFile))
		}
	}
	base := string(bytes.TrimRight(f.Bytes(), "\n")) + "\n"
	return base + "\n" + externalTaskDriverNotes()
}

func hostVolumePaths(volumes []NomadHostVolume) []string {
	uniq := make(map[string]struct{}, len(volumes))
	for _, v := range volumes {
		path := strings.TrimSpace(v.Path)
		if path == "" {
			continue
		}
		uniq[path] = struct{}{}
	}
	paths := make([]string, 0, len(uniq))
	for path := range uniq {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func appendDefaultTaskDriverConfig(root *hclwrite.Body, cfg NodeConfig) {
	dockerPlugin := root.AppendNewBlock("plugin", []string{"docker"}).Body()
	dockerCfg := dockerPlugin.AppendNewBlock("config", nil).Body()
	dockerGC := dockerCfg.AppendNewBlock("gc", nil).Body()
	dockerGC.SetAttributeValue("image_delay", cty.StringVal("48h"))
	dockerCfg.SetAttributeValue("allow_privileged", cty.BoolVal(true))
	dockerVolumes := dockerCfg.AppendNewBlock("volumes", nil).Body()
	dockerVolumes.SetAttributeValue("enabled", cty.BoolVal(true))
	dockerCfg.SetAttributeValue("extra_labels", stringListValue(defaultDockerExtraLabels))
	root.AppendNewline()

	rawExecPlugin := root.AppendNewBlock("plugin", []string{"raw_exec"}).Body()
	rawExecCfg := rawExecPlugin.AppendNewBlock("config", nil).Body()
	rawExecCfg.SetAttributeValue("enabled", cty.BoolVal(true))
	root.AppendNewline()

	if cfg.EnableContainerdDriver {
		runtime := strings.TrimSpace(cfg.ContainerdDriverRuntime)
		if runtime == "" {
			runtime = defaultContainerdDriverRuntime
		}
		statsInterval := strings.TrimSpace(cfg.ContainerdDriverStatsInterval)
		if statsInterval == "" {
			statsInterval = defaultContainerdStatsInterval
		}
		containerdPlugin := root.AppendNewBlock("plugin", []string{"containerd-driver"}).Body()
		containerdCfg := containerdPlugin.AppendNewBlock("config", nil).Body()
		containerdCfg.SetAttributeValue("enabled", cty.BoolVal(true))
		containerdCfg.SetAttributeValue("containerd_runtime", cty.StringVal(runtime))
		containerdCfg.SetAttributeValue("stats_interval", cty.StringVal(statsInterval))
		root.AppendNewline()
	}
	if cfg.EnableExec2Driver {
		root.AppendNewBlock("plugin", []string{"nomad-driver-exec2"})
		root.AppendNewline()
	}
	if cfg.EnableJavaDriver {
		root.AppendNewBlock("plugin", []string{"java"})
		root.AppendNewline()
	}
	seen := map[string]struct{}{
		"containerd-driver":  {},
		"nomad-driver-exec2": {},
		"java":               {},
	}
	for _, pluginName := range cfg.AdditionalDriverPlugins {
		name := strings.TrimSpace(pluginName)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		root.AppendNewBlock("plugin", []string{name})
		root.AppendNewline()
	}
}

func stringListValue(values []string) cty.Value {
	out := make([]cty.Value, 0, len(values))
	for _, v := range values {
		out = append(out, cty.StringVal(v))
	}
	return cty.ListVal(out)
}

func externalTaskDriverNotes() string {
	lines := []string{
		"# Optional external task drivers (not auto-enabled by abc node add):",
		"# HashiCorp plugin drivers listed in Nomad task-driver docs:",
	}
	for _, name := range hashicorpTaskDriverPlugins {
		lines = append(lines, "# - "+name)
	}
	lines = append(lines, "# Community task drivers from Nomad community plugin docs:")
	for _, name := range communityTaskDriverPlugins {
		lines = append(lines, "# - "+name)
	}
	lines = append(lines, "# - containerd can be enabled experimentally via --community-driver=containerd with --exp (post-join setup).")
	lines = append(lines, "# - exec2 can be enabled experimentally via --community-driver=exec2 with --exp (post-join setup).")
	lines = append(lines, "# - java driver + JDK can be configured via --java-driver and --jdk-version (post-join setup).")
	lines = append(lines, "# Install plugin binaries in Nomad plugin_dir and add plugin blocks manually to enable them.")
	return strings.Join(lines, "\n") + "\n"
}

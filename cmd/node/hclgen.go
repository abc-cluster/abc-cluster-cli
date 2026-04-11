package node

import (
	"bytes"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// NodeConfig holds the parameters for generating a Nomad client HCL config.
type NodeConfig struct {
	Datacenter string
	DataDir    string
	NodeClass  string
	ServerJoin []string // --server-join addresses → server_join.retry_join
	Encrypt    string
	ACL        bool
	Address    string
	Advertise  string
	CAFile     string
	CertFile   string
	KeyFile    string
	ServerMode bool // also enable server stanza (advanced)
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
	root.AppendNewline()

	// Server stanza (advanced — omitted for pure client nodes)
	if cfg.ServerMode {
		serverBody := root.AppendNewBlock("server", nil).Body()
		serverBody.SetAttributeValue("enabled", cty.BoolVal(true))
		root.AppendNewline()
	}

	// server_join — populated from --server-join flag(s)
	// The flag is named --server-join to match the HCL stanza name;
	// internally maps to server_join.retry_join.
	if len(cfg.ServerJoin) > 0 {
		addrs := make([]cty.Value, len(cfg.ServerJoin))
		for i, a := range cfg.ServerJoin {
			addrs[i] = cty.StringVal(a)
		}
		sjBody := root.AppendNewBlock("server_join", nil).Body()
		sjBody.SetAttributeValue("retry_join", cty.ListVal(addrs))
		root.AppendNewline()
	}

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

	return string(bytes.TrimRight(f.Bytes(), "\n")) + "\n"
}

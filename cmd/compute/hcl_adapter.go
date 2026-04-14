package compute

import hclcompute "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/compute"

type NomadHostVolume = hclcompute.NomadHostVolume

type NodeConfig = hclcompute.NodeConfig

func GenerateClientHCL(cfg NodeConfig) string {
	return hclcompute.Generate(cfg)
}

func nomadClientServerAddr(addr string) string {
	return hclcompute.NomadClientServerAddr(addr)
}

func hostVolumePaths(volumes []NomadHostVolume) []string {
	return hclcompute.HostVolumePaths(volumes)
}

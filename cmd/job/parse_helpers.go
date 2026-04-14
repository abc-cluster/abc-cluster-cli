package job

import "github.com/abc-cluster/abc-cluster-cli/cmd/utils"

func parseMemoryMB(s string) (int, error)     { return utils.ParseMemoryMB(s) }
func walltimeToSeconds(t string) (int, error) { return utils.WalltimeToSeconds(t) }

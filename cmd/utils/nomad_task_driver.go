package utils

import "strings"

// NormalizeNomadTaskDriver maps user-facing driver labels to the names Nomad
// registers on the client. In particular, Roblox's nomad-driver-containerd
// registers as "containerd-driver" (see `nomad node status -json` → Drivers,
// and agent plugin block `plugin "containerd-driver"`).
func NormalizeNomadTaskDriver(driver string) string {
	d := strings.ToLower(strings.TrimSpace(driver))
	if d == "containerd" {
		return "containerd-driver"
	}
	return d
}

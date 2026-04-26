package jurist

import (
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// ResolveLocally resolves a single auto-* driver hint using the node capabilities
// already stored in the config (populated by "abc cluster capabilities sync").
//
// It returns an error if no node in the cluster has any driver from the priority list.
// It sets Resolution.Warning when a low-isolation driver (raw_exec) wins.
func ResolveLocally(hint string, nodes []config.NodeCapability, priority config.DriverPriority) (*Resolution, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf(
			"no node capability data in config for driver resolution\n" +
				"  Run: abc cluster capabilities sync",
		)
	}

	// Build a set of drivers available on at least one node.
	available := make(map[string]bool)
	for _, n := range nodes {
		for _, d := range n.Drivers {
			available[strings.ToLower(d)] = true
		}
	}

	var priorityList []string
	switch hint {
	case DriverAutoContainer:
		priorityList = priority.ContainerPriority
	case DriverAutoExec:
		priorityList = priority.ExecPriority
	default:
		return nil, fmt.Errorf("unknown auto-driver hint %q", hint)
	}

	for _, d := range priorityList {
		key := strings.ToLower(d)
		if !available[key] {
			continue
		}
		// Collect the UUIDs of every node that has this driver healthy+detected.
		// Used for placement constraint; avoids ${driver.NAME} which is invalid in
		// Nomad's constraint attribute syntax for hyphenated driver names (e.g. containerd-driver).
		var ids []string
		for _, n := range nodes {
			for _, nd := range n.Drivers {
				if strings.EqualFold(nd, d) {
					ids = append(ids, n.ID)
					break
				}
			}
		}
		res := &Resolution{
			OriginalDriver:  hint,
			ResolvedDriver:  d,
			EligibleNodeIDs: ids,
			Reason:          fmt.Sprintf("first driver in %s priority list present on ≥1 node", hint),
		}
		if d == "raw_exec" && hint == DriverAutoExec {
			res.Warning = "raw_exec has no sandbox isolation; consider installing exec or exec2 on cluster nodes"
		}
		return res, nil
	}

	return nil, fmt.Errorf(
		"no driver from the %s priority list %v is available on any cluster node\n"+
			"  Available drivers: %s\n"+
			"  Update priority with: abc config set contexts.<name>.job.driver.%s [<driver1>,<driver2>,...]\n"+
			"  Or install a supported driver on cluster nodes and re-run: abc cluster capabilities sync",
		hint, priorityList,
		formatDriverSet(available),
		driverPriorityConfigKey(hint),
	)
}

func formatDriverSet(m map[string]bool) string {
	var out []string
	for d := range m {
		out = append(out, d)
	}
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, ", ")
}

func driverPriorityConfigKey(hint string) string {
	switch hint {
	case DriverAutoContainer:
		return "container_priority"
	case DriverAutoExec:
		return "exec_priority"
	}
	return "container_priority"
}

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// jobDriversWire is the minimal Nomad job JSON shape needed to read task drivers.
type jobDriversWire struct {
	TaskGroups []struct {
		Tasks []struct {
			Driver string `json:"Driver"`
		} `json:"Tasks"`
	} `json:"TaskGroups"`
}

// ExtractJobTaskDrivers returns unique, non-empty task driver names from parsed
// Nomad job JSON (POST /v1/jobs/parse output).
func ExtractJobTaskDrivers(jobJSON json.RawMessage) ([]string, error) {
	var wire jobDriversWire
	if err := json.Unmarshal(jobJSON, &wire); err != nil {
		return nil, fmt.Errorf("parse job JSON for task drivers: %w", err)
	}
	seen := make(map[string]struct{})
	var out []string
	for _, tg := range wire.TaskGroups {
		for _, t := range tg.Tasks {
			d := strings.TrimSpace(t.Driver)
			if d == "" {
				continue
			}
			if _, ok := seen[d]; ok {
				continue
			}
			seen[d] = struct{}{}
			out = append(out, d)
		}
	}
	return out, nil
}

const driverPreflightMaxConcurrent = 12

// PreflightJobTaskDrivers checks that each required task driver is reported as
// detected and healthy on at least one scheduling-eligible Nomad client node.
// It is a best-effort fingerprint (it does not evaluate job constraints); it
// catches missing plugins and broken daemons (e.g. docker.sock down) quickly
// before RegisterJob or Plan.
func (c *NomadClient) PreflightJobTaskDrivers(ctx context.Context, jobJSON json.RawMessage, status io.Writer) error {
	if status == nil {
		status = io.Discard
	}
	drivers, err := ExtractJobTaskDrivers(jobJSON)
	if err != nil {
		return err
	}
	if len(drivers) == 0 {
		return nil
	}

	stubs, err := c.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("list nomad nodes for driver preflight: %w", err)
	}
	eligible := filterEligibleClientNodes(stubs)
	if len(eligible) == 0 {
		fmt.Fprintf(status, "  [abc] driver preflight: no eligible Nomad client nodes — skipping driver check\n")
		return nil
	}

	fmt.Fprintf(status, "  Checking task drivers on %d Nomad client node(s)...\n", len(eligible))

	ids := make([]string, len(eligible))
	for i, s := range eligible {
		ids[i] = s.ID
	}
	nodes, err := parallelGetNodes(ctx, c, ids)
	if err != nil {
		return err
	}

	satisfied := make(map[string]bool, len(drivers))
	for _, d := range drivers {
		satisfied[d] = false
	}
	notes := make(map[string][]string)

	for _, n := range nodes {
		if n == nil {
			continue
		}
		for _, d := range drivers {
			if satisfied[d] {
				continue
			}
			info, ok := n.Drivers[d]
			if !ok {
				appendNote(notes, d, fmt.Sprintf("node %q: driver not registered", n.Name))
				continue
			}
			if info.Detected && info.Healthy {
				satisfied[d] = true
				continue
			}
			msg := describeDriverProblem(n.Name, info)
			appendNote(notes, d, msg)
		}
	}

	var missing []string
	for _, d := range drivers {
		if !satisfied[d] {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "nomad task driver preflight failed: no eligible client node has a healthy install for %s\n", strings.Join(missing, ", "))
	for _, d := range missing {
		for _, line := range notes[d] {
			fmt.Fprintf(&b, "  • %s: %s\n", d, line)
		}
		if len(notes[d]) == 0 {
			fmt.Fprintf(&b, "  • %s: no client reported this driver\n", d)
		}
	}
	fmt.Fprintf(&b, "Install or repair the driver on Nomad agents, then retry. Inspect clients with:\n  abc admin services nomad cli -- node status -json\n")
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

func appendNote(m map[string][]string, driver, line string) {
	const maxNotes = 4
	lines := m[driver]
	if len(lines) >= maxNotes {
		return
	}
	for _, existing := range lines {
		if existing == line {
			return
		}
	}
	m[driver] = append(lines, line)
}

func describeDriverProblem(nodeName string, info NomadDriverInfo) string {
	switch {
	case !info.Detected:
		return fmt.Sprintf("node %q: driver not detected", nodeName)
	case !info.Healthy && strings.TrimSpace(info.HealthDescription) != "":
		return fmt.Sprintf("node %q: unhealthy — %s", nodeName, strings.TrimSpace(info.HealthDescription))
	default:
		return fmt.Sprintf("node %q: unhealthy", nodeName)
	}
}

func filterEligibleClientNodes(stubs []NomadNodeStub) []NomadNodeStub {
	var out []NomadNodeStub
	for _, s := range stubs {
		if !strings.EqualFold(s.Status, "ready") {
			continue
		}
		if strings.EqualFold(s.SchedulingEligibility, "ineligible") {
			continue
		}
		if s.Drain {
			continue
		}
		out = append(out, s)
	}
	return out
}

func parallelGetNodes(ctx context.Context, c *NomadClient, ids []string) ([]*NomadNode, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	nodes := make([]*NomadNode, len(ids))
	sem := make(chan struct{}, driverPreflightMaxConcurrent)
	var wg sync.WaitGroup
	errOnce := sync.Once{}
	var firstErr error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for i, id := range ids {
		i, id := i, id
		wg.Add(1)
		go func(idx int, nodeID string) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			n, err := c.GetNode(ctx, nodeID)
			if err != nil {
				errOnce.Do(func() {
					firstErr = fmt.Errorf("inspect nomad node %s: %w", nodeID, err)
					cancel()
				})
				return
			}
			nodes[idx] = n
		}(i, id)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return nodes, nil
}

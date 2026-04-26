package config

// DriverPriority lists preferred Nomad task drivers in order of preference.
// The resolver picks the first entry present as healthy on ≥1 eligible node.
// Falls back to built-in defaults when empty.
type DriverPriority struct {
	ContainerPriority []string `yaml:"container_priority,omitempty"`
	ExecPriority      []string `yaml:"exec_priority,omitempty"`
}

// ContextJob holds context-level job submission policy.
type ContextJob struct {
	Driver DriverPriority `yaml:"driver,omitempty"`
}

// DefaultContainerPriority is the built-in fallback container driver order.
var DefaultContainerPriority = []string{"containerd-driver", "docker", "podman", "singularity"}

// DefaultExecPriority is the built-in fallback exec driver order.
var DefaultExecPriority = []string{"exec2", "exec", "raw_exec"}

// JobDriverPriority returns the DriverPriority for this context, merging
// with built-in defaults for any unset priority list.
func (ctx *Context) JobDriverPriority() DriverPriority {
	if ctx.Job == nil {
		return DriverPriority{
			ContainerPriority: DefaultContainerPriority,
			ExecPriority:      DefaultExecPriority,
		}
	}
	p := ctx.Job.Driver
	if len(p.ContainerPriority) == 0 {
		p.ContainerPriority = DefaultContainerPriority
	}
	if len(p.ExecPriority) == 0 {
		p.ExecPriority = DefaultExecPriority
	}
	return p
}

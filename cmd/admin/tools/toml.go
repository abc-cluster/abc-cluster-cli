package tools

// toml.go — read/write helpers for ~/.abc/binaries/tools.toml.
//
// Reading:  github.com/pelletier/go-toml/v2 (full TOML compliance).
// Writing:  targeted line-level replacement so user comments and formatting
//           are preserved when abc writes back resolved versions or endpoints.

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ── Schema types ──────────────────────────────────────────────────────────────

// ToolSpec describes one [tools.<name>] entry.
type ToolSpec struct {
	Name     string // derived from the map key, not the TOML value
	Repo     string `toml:"repo"`
	Version  string `toml:"version"`
	Disabled bool   `toml:"disabled"`
}

// LocalSpec describes one [local.<name>] entry — a locally built artifact.
//
// Use Path for arch-agnostic artifacts (e.g. JARs, Python wheels).
// Use Paths for architecture-specific binaries (e.g. Go or Rust binaries).
//
// Remote naming convention:
//   - Path  → <prefix>/<basename(Path)>          e.g. nf-pipeline-gen-1.0.jar
//   - Paths → <prefix>/<name>-<key>              e.g. abc-node-probe-linux-amd64
type LocalSpec struct {
	Name     string            // derived from the map key
	Path     string            `toml:"path"`  // single arch-agnostic path
	Paths    map[string]string `toml:"paths"` // key: "os-arch", value: filesystem path
	Disabled bool              `toml:"disabled"`
}

// EngineSpec describes the [engine] section.
type EngineSpec struct {
	Repo    string `toml:"repo"`
	Version string `toml:"version"`
}

// PushSpec describes the [push] section — bucket and prefix only.
// Credentials, endpoint, and context_service live in config.yaml
// under admin.tools.* of the active context.
type PushSpec struct {
	Bucket string `toml:"bucket"`
	Prefix string `toml:"prefix"`
}

// rawConfig mirrors the TOML structure for unmarshalling.
type rawConfig struct {
	Engine EngineSpec              `toml:"engine"`
	Push   PushSpec                `toml:"push"`
	Tools  map[string]rawTool      `toml:"tools"`
	Local  map[string]rawLocalSpec `toml:"local"`
}

type rawTool struct {
	Repo     string `toml:"repo"`
	Version  string `toml:"version"`
	Disabled bool   `toml:"disabled"`
}

type rawLocalSpec struct {
	Path     string            `toml:"path"`
	Paths    map[string]string `toml:"paths"`
	Disabled bool              `toml:"disabled"`
}

// ToolsConfig is the parsed, usable representation of tools.toml.
// It also holds the raw lines for comment-preserving write-back.
// Push credentials and endpoint are NOT stored here — they live in
// config.yaml under admin.tools.* of the active context.
type ToolsConfig struct {
	Engine EngineSpec
	Push   PushSpec
	Tools  []ToolSpec  // ordered by appearance in the file
	Local  []LocalSpec // ordered by appearance in the file

	raw  []string // original file lines, kept for targeted write-back
	path string   // path this was read from
}

// ── Read ──────────────────────────────────────────────────────────────────────

// ReadToolsConfig reads and parses the tools.toml at path.
func ReadToolsConfig(path string) (*ToolsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var raw rawConfig
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Apply defaults for fields that may be empty in older files.
	if raw.Push.Bucket == "" {
		raw.Push.Bucket = "abc-reserved"
	}
	if raw.Push.Prefix == "" {
		raw.Push.Prefix = "binary_tools"
	}
	if raw.Engine.Repo == "" {
		raw.Engine.Repo = "zyedidia/eget"
	}
	if raw.Engine.Version == "" {
		raw.Engine.Version = "v1.3.3"
	}

	// Preserve ordering by scanning the raw lines for section headers.
	var orderedToolNames []string
	var orderedLocalNames []string
	seenTool := map[string]bool{}
	seenLocal := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[tools.") && strings.HasSuffix(line, "]") {
			name := line[len("[tools.") : len(line)-1]
			if !seenTool[name] {
				orderedToolNames = append(orderedToolNames, name)
				seenTool[name] = true
			}
		}
		if strings.HasPrefix(line, "[local.") && strings.HasSuffix(line, "]") {
			name := line[len("[local.") : len(line)-1]
			if !seenLocal[name] {
				orderedLocalNames = append(orderedLocalNames, name)
				seenLocal[name] = true
			}
		}
	}

	var tools []ToolSpec
	for _, name := range orderedToolNames {
		rt, ok := raw.Tools[name]
		if !ok {
			continue
		}
		tools = append(tools, ToolSpec{
			Name:     name,
			Repo:     rt.Repo,
			Version:  rt.Version,
			Disabled: rt.Disabled,
		})
	}

	var locals []LocalSpec
	for _, name := range orderedLocalNames {
		rl, ok := raw.Local[name]
		if !ok {
			continue
		}
		locals = append(locals, LocalSpec{
			Name:     name,
			Path:     rl.Path,
			Paths:    rl.Paths,
			Disabled: rl.Disabled,
		})
	}

	// Collect raw lines for write-back.
	var rawLines []string
	scanner2 := bufio.NewScanner(bytes.NewReader(data))
	for scanner2.Scan() {
		rawLines = append(rawLines, scanner2.Text())
	}

	return &ToolsConfig{
		Engine: raw.Engine,
		Push:   raw.Push,
		Tools:  tools,
		Local:  locals,
		raw:    rawLines,
		path:   path,
	}, nil
}

// ── Write-back ────────────────────────────────────────────────────────────────
//
// We patch specific keys in the raw lines so user comments and formatting
// survive. Full marshal is intentionally avoided.

// SetToolVersion updates [tools.<name>] version in memory and raw lines.
func (cfg *ToolsConfig) SetToolVersion(name, version string) {
	for i := range cfg.Tools {
		if cfg.Tools[i].Name == name {
			cfg.Tools[i].Version = version
			break
		}
	}
	cfg.raw = patchKeyInSection(cfg.raw, "tools."+name, "version", version)
}

// WriteBack writes the (possibly patched) raw lines back to the original path.
func (cfg *ToolsConfig) WriteBack() error {
	content := strings.Join(cfg.raw, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(cfg.path, []byte(content), 0o644)
}

// patchKeyInSection replaces key = "value" inside [section] preserving all
// other lines. If the key already exists under [section] it is updated in
// place. If it is missing it is appended just before the next section header
// (or at the end of the file).
func patchKeyInSection(lines []string, section, key, value string) []string {
	header := "[" + section + "]"
	quoted := `"` + value + `"`

	inSection := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == header {
			inSection = true
			continue
		}

		if inSection {
			// Hit the next section — key was not found; insert before it.
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				insert := key + " = " + quoted
				out := make([]string, 0, len(lines)+1)
				out = append(out, lines[:i]...)
				out = append(out, insert)
				out = append(out, lines[i:]...)
				return out
			}
			// Try to match the key (ignore comments and blank lines).
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			eqIdx := strings.IndexByte(trimmed, '=')
			if eqIdx > 0 && strings.TrimSpace(trimmed[:eqIdx]) == key {
				lines[i] = key + " = " + quoted
				return lines
			}
		}
	}

	// Section was the last one (or key not found) — append.
	if inSection {
		lines = append(lines, key+" = "+quoted)
	}
	return lines
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// EnabledTools returns only non-disabled tools in file order.
func (cfg *ToolsConfig) EnabledTools() []ToolSpec {
	out := make([]ToolSpec, 0, len(cfg.Tools))
	for _, t := range cfg.Tools {
		if !t.Disabled {
			out = append(out, t)
		}
	}
	return out
}

// EnabledLocals returns only non-disabled local artifact specs in file order.
func (cfg *ToolsConfig) EnabledLocals() []LocalSpec {
	out := make([]LocalSpec, 0, len(cfg.Local))
	for _, l := range cfg.Local {
		if !l.Disabled {
			out = append(out, l)
		}
	}
	return out
}

// ToolByName returns the ToolSpec for name regardless of disabled state.
func (cfg *ToolsConfig) ToolByName(name string) (ToolSpec, bool) {
	for _, t := range cfg.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return ToolSpec{}, false
}

// Path returns the file path this config was loaded from.
func (cfg *ToolsConfig) Path() string { return cfg.path }

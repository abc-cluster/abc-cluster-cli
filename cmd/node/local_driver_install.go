package node

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

const localDriverMaxSearchDepth = 4

type localDriverInstallConfig struct {
	Drivers []localDriverSpec
}

type localDriverSpec struct {
	PluginName string
	SourcePath string
	BinaryPath string
}

func (c localDriverInstallConfig) Requested() bool {
	return len(c.Drivers) > 0
}

func localDriverInstallConfigFromFlags(cmd *cobra.Command) (localDriverInstallConfig, error) {
	rawSpecs, _ := cmd.Flags().GetStringArray("local-driver")
	specs, err := parseLocalDriverSpecs(rawSpecs)
	if err != nil {
		return localDriverInstallConfig{}, err
	}
	return localDriverInstallConfig{Drivers: specs}, nil
}

func parseLocalDriverSpecs(raw []string) ([]localDriverSpec, error) {
	specs := make([]localDriverSpec, 0, len(raw))
	seenPluginNames := make(map[string]struct{})
	for _, entry := range raw {
		for _, token := range strings.Split(entry, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			spec, err := parseLocalDriverSpec(token)
			if err != nil {
				return nil, err
			}
			if _, exists := seenPluginNames[spec.PluginName]; exists {
				return nil, fmt.Errorf("duplicate --local-driver plugin name %q", spec.PluginName)
			}
			seenPluginNames[spec.PluginName] = struct{}{}
			specs = append(specs, spec)
		}
	}
	return specs, nil
}

func parseLocalDriverSpec(raw string) (localDriverSpec, error) {
	pluginName, sourcePath := splitLocalDriverSpec(raw)
	if sourcePath == "" {
		return localDriverSpec{}, fmt.Errorf("--local-driver value %q is missing a path", raw)
	}

	absSourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return localDriverSpec{}, fmt.Errorf("resolve local driver path %q: %w", sourcePath, err)
	}

	info, err := os.Stat(absSourcePath)
	if err != nil {
		return localDriverSpec{}, fmt.Errorf("local driver path %q: %w", sourcePath, err)
	}

	resolvedBinaryPath := absSourcePath
	if info.IsDir() {
		resolvedBinaryPath, err = resolveLocalDriverBinaryFromDir(absSourcePath, pluginName)
		if err != nil {
			return localDriverSpec{}, err
		}
	} else if !info.Mode().IsRegular() {
		return localDriverSpec{}, fmt.Errorf("local driver path %q is not a regular file", sourcePath)
	}

	if pluginName == "" {
		pluginName = filepath.Base(resolvedBinaryPath)
	}
	if err := validateLocalDriverPluginName(pluginName); err != nil {
		return localDriverSpec{}, fmt.Errorf("invalid local driver plugin name %q: %w", pluginName, err)
	}

	return localDriverSpec{
		PluginName: pluginName,
		SourcePath: absSourcePath,
		BinaryPath: resolvedBinaryPath,
	}, nil
}

func splitLocalDriverSpec(raw string) (pluginName, sourcePath string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	idx := strings.Index(raw, "=")
	if idx <= 0 {
		return "", raw
	}
	left := strings.TrimSpace(raw[:idx])
	right := strings.TrimSpace(raw[idx+1:])
	if left == "" || right == "" {
		return "", raw
	}
	if strings.ContainsAny(left, `/\`) {
		return "", raw
	}
	return left, right
}

func validateLocalDriverPluginName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '-', '_', '.':
			continue
		default:
			return fmt.Errorf("allowed characters are letters, digits, '-', '_' and '.'")
		}
	}
	return nil
}

func resolveLocalDriverBinaryFromDir(dir, pluginHint string) (string, error) {
	orderedCandidateNames := preferredLocalDriverBinaryNames(dir, pluginHint)
	searchRoots := []string{
		dir,
		filepath.Join(dir, "bin"),
		filepath.Join(dir, "dist"),
		filepath.Join(dir, "build"),
		filepath.Join(dir, "out"),
	}
	for _, root := range searchRoots {
		for _, name := range orderedCandidateNames {
			candidate := filepath.Join(root, name)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				return candidate, nil
			}
		}
	}

	candidates := make([]string, 0)
	preferredNameSet := make(map[string]struct{}, len(orderedCandidateNames))
	for _, name := range orderedCandidateNames {
		preferredNameSet[name] = struct{}{}
	}
	trimmedRoot := strings.TrimRight(dir, string(os.PathSeparator))
	rootDepth := strings.Count(trimmedRoot, string(os.PathSeparator))
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		currentDepth := strings.Count(path, string(os.PathSeparator)) - rootDepth
		if d.IsDir() && currentDepth > localDriverMaxSearchDepth {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if _, ok := preferredNameSet[name]; ok {
			candidates = append(candidates, path)
			return nil
		}
		if strings.HasPrefix(name, "nomad-driver-") {
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("scan local driver directory %q: %w", dir, err)
	}
	sort.Strings(candidates)
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("could not find a driver binary in %q; provide an explicit binary path with --local-driver [plugin_name=]/path/to/binary", dir)
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("multiple driver binaries found in %q (%s); use --local-driver plugin_name=/path/to/binary", dir, strings.Join(candidates, ", "))
	}
}

func preferredLocalDriverBinaryNames(dir, pluginHint string) []string {
	baseName := filepath.Base(filepath.Clean(dir))
	names := []string{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		for _, existing := range names {
			if existing == v {
				return
			}
		}
		names = append(names, v)
	}
	add(pluginHint)
	add("nomad-driver-" + pluginHint)
	add(baseName)
	add("nomad-driver-" + baseName)
	return names
}

func validateLocalDriverTarget(goos string, cfg localDriverInstallConfig) error {
	if !cfg.Requested() {
		return nil
	}
	if goos != "linux" {
		return fmt.Errorf("local driver deployment is currently supported only on linux targets")
	}
	return nil
}

func applyLocalDriverNodeConfig(nodeCfg *NodeConfig, cfg localDriverInstallConfig) {
	if nodeCfg == nil || !cfg.Requested() {
		return
	}
	if nodeCfg.PluginDir == "" {
		nodeCfg.PluginDir = defaultNomadPluginsDir
	}
	seen := make(map[string]struct{}, len(nodeCfg.AdditionalDriverPlugins))
	for _, existing := range nodeCfg.AdditionalDriverPlugins {
		seen[existing] = struct{}{}
	}
	for _, driver := range cfg.Drivers {
		if _, ok := seen[driver.PluginName]; ok {
			continue
		}
		nodeCfg.AdditionalDriverPlugins = append(nodeCfg.AdditionalDriverPlugins, driver.PluginName)
		seen[driver.PluginName] = struct{}{}
	}
}

func InstallLocalDrivers(ctx context.Context, ex Executor, cfg localDriverInstallConfig, w io.Writer) error {
	if !cfg.Requested() {
		return nil
	}
	if err := validateLocalDriverTarget(ex.OS(), cfg); err != nil {
		return err
	}
	if err := ex.Run(ctx, fmt.Sprintf("sudo mkdir -p %q", defaultNomadPluginsDir), io.Discard); err != nil {
		return fmt.Errorf("create plugin directory %s: %w", defaultNomadPluginsDir, err)
	}
	for _, driver := range cfg.Drivers {
		fmt.Fprintf(w, "\n  Installing local Nomad driver %s from %s...\n", driver.PluginName, driver.BinaryPath)
		f, err := os.Open(driver.BinaryPath)
		if err != nil {
			return fmt.Errorf("open local driver binary %s: %w", driver.BinaryPath, err)
		}
		remotePath := fmt.Sprintf("%s/%s", defaultNomadPluginsDir, driver.PluginName)
		uploadErr := ex.Upload(ctx, f, remotePath, 0755)
		_ = f.Close()
		if uploadErr != nil {
			return fmt.Errorf("upload local driver %s to %s: %w", driver.PluginName, remotePath, uploadErr)
		}
		fixupCmd := fmt.Sprintf("sudo chown root:root %q && sudo chmod 0755 %q", remotePath, remotePath)
		if err := ex.Run(ctx, fixupCmd, io.Discard); err != nil {
			return fmt.Errorf("set ownership/permissions on %s: %w", remotePath, err)
		}
		fmt.Fprintf(w, "    ✓ %s installed in %s\n", driver.PluginName, defaultNomadPluginsDir)
	}
	return nil
}

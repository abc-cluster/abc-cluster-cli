package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

const (
	defaultJavaDriverJDKVersion = "21"
	defaultJDKInstallRoot       = "/usr/local/jdks"
	defaultJavaBinaryPath       = "/usr/local/bin/java"
	defaultJavacBinaryPath      = "/usr/local/bin/javac"
)

type javaDriverInstallConfig struct {
	Enabled           bool
	JDKVersions       []string
	DefaultJDKVersion string
}

type temurinJDKAsset struct {
	MajorVersion string
	ReleaseName  string
	ArchiveName  string
	DownloadURL  string
	Checksum     string
}

func (c javaDriverInstallConfig) Requested() bool {
	return c.Enabled
}

func javaDriverInstallConfigFromFlags(cmd *cobra.Command) (javaDriverInstallConfig, error) {
	enabled, _ := cmd.Flags().GetBool("java-driver")
	rawVersions, _ := cmd.Flags().GetStringArray("jdk-version")
	rawDefault, _ := cmd.Flags().GetString("jdk-default-version")
	if !enabled {
		if len(rawVersions) > 0 || strings.TrimSpace(rawDefault) != "" {
			return javaDriverInstallConfig{}, fmt.Errorf("--jdk-version and --jdk-default-version require --java-driver")
		}
		return javaDriverInstallConfig{}, nil
	}

	versions, err := normalizeJDKVersionList(rawVersions)
	if err != nil {
		return javaDriverInstallConfig{}, err
	}
	if len(versions) == 0 {
		versions = []string{defaultJavaDriverJDKVersion}
	}
	defaultVersion := strings.TrimSpace(rawDefault)
	if defaultVersion == "" {
		defaultVersion = versions[0]
	} else {
		defaultVersion, err = normalizeJDKFeatureVersion(defaultVersion)
		if err != nil {
			return javaDriverInstallConfig{}, fmt.Errorf("invalid --jdk-default-version: %w", err)
		}
		if !slices.Contains(versions, defaultVersion) {
			return javaDriverInstallConfig{}, fmt.Errorf("--jdk-default-version %s must be included in --jdk-version list", defaultVersion)
		}
	}

	return javaDriverInstallConfig{
		Enabled:           true,
		JDKVersions:       versions,
		DefaultJDKVersion: defaultVersion,
	}, nil
}

func normalizeJDKVersionList(raw []string) ([]string, error) {
	seen := make(map[string]struct{})
	versions := make([]string, 0, len(raw))
	for _, entry := range raw {
		for _, token := range strings.Split(entry, ",") {
			if strings.TrimSpace(token) == "" {
				continue
			}
			version, err := normalizeJDKFeatureVersion(token)
			if err != nil {
				return nil, err
			}
			if _, ok := seen[version]; ok {
				continue
			}
			seen[version] = struct{}{}
			versions = append(versions, version)
		}
	}
	return versions, nil
}

func normalizeJDKFeatureVersion(raw string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	trimmed = strings.TrimPrefix(trimmed, "jdk")
	trimmed = strings.TrimPrefix(trimmed, "-")
	if trimmed == "" {
		return "", fmt.Errorf("empty JDK version")
	}
	var digits strings.Builder
	for _, r := range trimmed {
		if !unicode.IsDigit(r) {
			break
		}
		digits.WriteRune(r)
	}
	if digits.Len() == 0 {
		return "", fmt.Errorf("invalid JDK version %q", raw)
	}
	n, err := strconv.Atoi(digits.String())
	if err != nil || n < 8 {
		return "", fmt.Errorf("invalid JDK major version %q", raw)
	}
	return strconv.Itoa(n), nil
}

func validateJavaDriverTarget(goos string, cfg javaDriverInstallConfig) error {
	if !cfg.Requested() {
		return nil
	}
	if goos != "linux" {
		return fmt.Errorf("java-driver setup is currently supported only on linux targets")
	}
	return nil
}

func applyJavaDriverNodeConfig(nodeCfg *NodeConfig, cfg javaDriverInstallConfig) {
	if nodeCfg == nil || !cfg.Requested() {
		return
	}
	nodeCfg.EnableJavaDriver = true
}

func InstallJavaDriver(ctx context.Context, ex Executor, cfg javaDriverInstallConfig, w io.Writer) error {
	if !cfg.Requested() {
		return nil
	}
	if err := validateJavaDriverTarget(ex.OS(), cfg); err != nil {
		return err
	}

	assets, err := resolveTemurinJDKAssets(ctx, ex.Arch(), cfg.JDKVersions)
	if err != nil {
		return err
	}
	for _, asset := range assets {
		fmt.Fprintf(w, "\n  Installing Temurin JDK %s (%s)...\n", asset.MajorVersion, asset.ReleaseName)
		if err := ex.Run(ctx, strings.Join(jdkInstallSteps(asset), " && "), LineWriter(w, "    ")); err != nil {
			return fmt.Errorf("install JDK %s: %w", asset.MajorVersion, err)
		}
		fmt.Fprintf(w, "    ✓ JDK %s installed in %s/jdk-%s\n", asset.MajorVersion, defaultJDKInstallRoot, asset.MajorVersion)
	}
	if err := ex.Run(ctx, strings.Join(javaDefaultSelectionSteps(cfg.DefaultJDKVersion), " && "), LineWriter(w, "    ")); err != nil {
		return fmt.Errorf("set default java runtime to JDK %s: %w", cfg.DefaultJDKVersion, err)
	}
	fmt.Fprintf(w, "    ✓ Default java runtime set to JDK %s\n", cfg.DefaultJDKVersion)
	return nil
}

func resolveTemurinJDKAssets(ctx context.Context, goarch string, versions []string) ([]temurinJDKAsset, error) {
	arch, err := adoptiumArchitectureFor(goarch)
	if err != nil {
		return nil, err
	}
	assets := make([]temurinJDKAsset, 0, len(versions))
	for _, version := range versions {
		apiURL := fmt.Sprintf("https://api.adoptium.net/v3/assets/latest/%s/hotspot?architecture=%s&heap_size=normal&image_type=jdk&jvm_impl=hotspot&os=linux&page_size=1&project=jdk", version, arch)
		data, err := fetchBytes(ctx, apiURL)
		if err != nil {
			return nil, fmt.Errorf("resolve Temurin JDK %s metadata: %w", version, err)
		}
		var payload []struct {
			Binary struct {
				Package struct {
					Checksum string `json:"checksum"`
					Link     string `json:"link"`
					Name     string `json:"name"`
				} `json:"package"`
			} `json:"binary"`
			ReleaseName string `json:"release_name"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, fmt.Errorf("parse Temurin JDK %s metadata: %w", version, err)
		}
		if len(payload) == 0 {
			return nil, fmt.Errorf("Temurin JDK %s is not available for architecture %s", version, arch)
		}
		pkg := payload[0].Binary.Package
		if strings.TrimSpace(pkg.Link) == "" || strings.TrimSpace(pkg.Checksum) == "" || strings.TrimSpace(pkg.Name) == "" {
			return nil, fmt.Errorf("Temurin JDK %s metadata is incomplete", version)
		}
		assets = append(assets, temurinJDKAsset{
			MajorVersion: version,
			ReleaseName:  strings.TrimSpace(payload[0].ReleaseName),
			ArchiveName:  strings.TrimSpace(pkg.Name),
			DownloadURL:  strings.TrimSpace(pkg.Link),
			Checksum:     strings.ToLower(strings.TrimSpace(pkg.Checksum)),
		})
	}
	return assets, nil
}

func adoptiumArchitectureFor(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "aarch64", nil
	default:
		return "", fmt.Errorf("unsupported arch for Temurin JDK binaries: %s", goarch)
	}
}

func jdkInstallSteps(asset temurinJDKAsset) []string {
	tmpArchive := fmt.Sprintf("/tmp/abc-jdk-%s.tar.gz", asset.MajorVersion)
	tmpExtractRoot := fmt.Sprintf("/tmp/abc-jdk-%s-extract", asset.MajorVersion)
	targetDir := fmt.Sprintf("%s/jdk-%s", defaultJDKInstallRoot, asset.MajorVersion)
	return []string{
		fmt.Sprintf("JDK_URL=%q", asset.DownloadURL),
		fmt.Sprintf("JDK_ARCHIVE_NAME=%q", asset.ArchiveName),
		fmt.Sprintf("JDK_SHA=%q", asset.Checksum),
		fmt.Sprintf("JDK_TMP_ARCHIVE=%q", tmpArchive),
		fmt.Sprintf("JDK_TMP_EXTRACT=%q", tmpExtractRoot),
		fmt.Sprintf("JDK_TARGET_DIR=%q", targetDir),
		"(command -v curl >/dev/null 2>&1 && curl -fL --retry 5 --retry-delay 1 --retry-all-errors --connect-timeout 20 --max-time 1800 -sS \"${JDK_URL}\" -o \"${JDK_TMP_ARCHIVE}\") || (command -v wget >/dev/null 2>&1 && wget -q --tries=5 --timeout=30 -O \"${JDK_TMP_ARCHIVE}\" \"${JDK_URL}\")",
		"echo \"${JDK_SHA}  ${JDK_TMP_ARCHIVE}\" | sha256sum -c -",
		"rm -rf \"${JDK_TMP_EXTRACT}\" && mkdir -p \"${JDK_TMP_EXTRACT}\"",
		"tar -xzf \"${JDK_TMP_ARCHIVE}\" -C \"${JDK_TMP_EXTRACT}\"",
		"JDK_EXTRACTED_DIR=$(find \"${JDK_TMP_EXTRACT}\" -mindepth 1 -maxdepth 1 -type d | head -n1)",
		"if [ -z \"${JDK_EXTRACTED_DIR}\" ]; then echo \"failed to locate extracted JDK directory for ${JDK_ARCHIVE_NAME}\" >&2; exit 1; fi",
		fmt.Sprintf("sudo mkdir -p %s", defaultJDKInstallRoot),
		"sudo rm -rf \"${JDK_TARGET_DIR}\"",
		"sudo mv \"${JDK_EXTRACTED_DIR}\" \"${JDK_TARGET_DIR}\"",
		"sudo chown -R root:root \"${JDK_TARGET_DIR}\"",
		"rm -rf \"${JDK_TMP_ARCHIVE}\" \"${JDK_TMP_EXTRACT}\"",
	}
}

func javaDefaultSelectionSteps(defaultVersion string) []string {
	javaTarget := fmt.Sprintf("%s/jdk-%s/bin/java", defaultJDKInstallRoot, defaultVersion)
	javacTarget := fmt.Sprintf("%s/jdk-%s/bin/javac", defaultJDKInstallRoot, defaultVersion)
	return []string{
		fmt.Sprintf("test -x %q", javaTarget),
		fmt.Sprintf("sudo ln -sfn %q %q", javaTarget, defaultJavaBinaryPath),
		fmt.Sprintf("if [ -x %q ]; then sudo ln -sfn %q %q; fi", javacTarget, javacTarget, defaultJavacBinaryPath),
		fmt.Sprintf("%s -version >/dev/null 2>&1", defaultJavaBinaryPath),
	}
}

func printJavaDriverPostSetupScriptSection(w io.Writer, goos, goarch string, cfg javaDriverInstallConfig, cfgPath, finalHCL string, rewriteNomadConfig bool) error {
	if !cfg.Requested() {
		return nil
	}
	if err := validateJavaDriverTarget(goos, cfg); err != nil {
		return err
	}
	assets, err := resolveTemurinJDKAssets(context.Background(), goarch, cfg.JDKVersions)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "# ── 8. Post-setup: Java task driver prerequisites ───────────────────────────\n")
	fmt.Fprintf(w, "# Install Temurin JDKs and configure the default java binary used by Nomad.\n")
	for _, asset := range assets {
		fmt.Fprintf(w, "# Install Temurin JDK %s (%s)\n", asset.MajorVersion, asset.ReleaseName)
		for _, step := range jdkInstallSteps(asset) {
			fmt.Fprintln(w, step)
		}
	}
	fmt.Fprintf(w, "# Select default java runtime (JDK %s)\n", cfg.DefaultJDKVersion)
	for _, step := range javaDefaultSelectionSteps(cfg.DefaultJDKVersion) {
		fmt.Fprintln(w, step)
	}
	if rewriteNomadConfig {
		printPostSetupNomadConfigRewriteAndRestart(w, cfgPath, finalHCL)
	}
	return nil
}

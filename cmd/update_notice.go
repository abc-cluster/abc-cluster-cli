package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"golang.org/x/mod/semver"
)

const (
	updateCheckDisableEnv = "ABC_CLI_DISABLE_UPDATE_CHECK"
	cliRepoOwner          = "abc-cluster"
	cliRepoName           = "abc-cluster-cli"
	updateCheckTimeout    = 1200 * time.Millisecond
	installScriptCmdFmt   = "curl -fsSL -H \"Accept: application/vnd.github.raw+json\" \"https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main\" | sh -s -- --version %s"
)

var fetchLatestCLITag = func(ctx context.Context) (string, error) {
	release, err := utils.FetchLatestReleaseWithContext(ctx, cliRepoOwner, cliRepoName)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(release.TagName), nil
}

func maybePrintCLIUpdateNotice(w io.Writer, currentVersion string, quiet bool) {
	if quiet || updateCheckDisabled() {
		return
	}

	currentNorm, ok := normalizeSemverTag(currentVersion)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()

	latestTag, err := fetchLatestCLITag(ctx)
	if err != nil {
		return
	}
	latestNorm, ok := normalizeSemverTag(latestTag)
	if !ok {
		return
	}
	if semver.Compare(latestNorm, currentNorm) <= 0 {
		return
	}

	latestDisplay := ensureVPrefix(latestNorm)
	currentDisplay := ensureVPrefix(currentNorm)
	fmt.Fprintf(w, "[abc] update available: %s (current %s)\n", latestDisplay, currentDisplay)
	fmt.Fprintf(w, "[abc] upgrade with:\n%s\n", fmt.Sprintf(installScriptCmdFmt, latestDisplay))
	fmt.Fprintf(w, "[abc] set %s=1 to silence this check\n", updateCheckDisableEnv)
}

func updateCheckDisabled() bool {
	v, ok := os.LookupEnv(updateCheckDisableEnv)
	if !ok {
		return false
	}
	v = strings.TrimSpace(strings.ToLower(v))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func normalizeSemverTag(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false
	}
	if strings.EqualFold(v, "dev") {
		return "", false
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "", false
	}
	return semver.Canonical(v), true
}

func ensureVPrefix(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

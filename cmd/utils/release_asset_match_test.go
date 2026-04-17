package utils

import "testing"

func TestFindAssetForPlatformTarget_exactAndFuzzy(t *testing.T) {
	t.Parallel()
	base := "abc-node-probe"
	release := &GitHubRelease{
		TagName: "v0.2.0",
		Assets: []GitHubReleaseAsset{
			{Name: "sha256sums.txt", DownloadURL: "https://example/sha"},
			{Name: "abc-node-probe-linux-amd64", DownloadURL: "https://example/linux-amd64"},
			{Name: "abc-node-probe-linux-arm64", DownloadURL: "https://example/linux-arm64"},
		},
	}
	if a := findAssetForPlatformTarget(release, base, "linux", "amd64"); a == nil || a.DownloadURL != "https://example/linux-amd64" {
		t.Fatalf("exact match: got %#v", a)
	}
	rel2 := &GitHubRelease{
		TagName: "v0.1.0",
		Assets: []GitHubReleaseAsset{
			{Name: "abc-node-probe-linux-amd64-v0.1.0", DownloadURL: "https://example/pref"},
		},
	}
	if a := findAssetForPlatformTarget(rel2, base, "linux", "amd64"); a == nil || a.Name != "abc-node-probe-linux-amd64-v0.1.0" {
		t.Fatalf("prefix match: got %#v", a)
	}
}

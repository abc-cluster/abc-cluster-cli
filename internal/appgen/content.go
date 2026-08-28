package appgen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MaxContentBytes caps a `content:` payload. A MultiQC report is a few MB;
	// a Quarto site with embedded data can be much larger. Rejecting here gives
	// a clear error at validate time rather than a slow failure when Nomad
	// fetches the artifact on the node.
	MaxContentBytes = 100 << 20 // 100 MiB

	// ContentBucket is the platform-reserved bucket holding app content.
	ContentBucket = "abc-reserved"

	// ContentPrefix is the key prefix within ContentBucket.
	ContentPrefix = "app-content"

	// KeepContentVersions is how many content digests are retained per app.
	// Older ones are pruned on deploy, so a rollback to the immediately
	// preceding versions stays possible without unbounded growth.
	KeepContentVersions = 3

	// StaticServerImage serves `content:` apps. Caddy takes its root as a
	// command argument, so no config file and therefore no image build is
	// needed; nginx would require a mounted nginx.conf.
	StaticServerImage = "caddy:alpine"
)

// ContentFile is one file in a content payload, with its path relative to the
// content root.
type ContentFile struct {
	Rel  string
	Abs  string
	Size int64
}

// WalkContent expands a `content:` path into the files that will be uploaded.
// A single file is published as index.html, so a bare report is served at the
// app's root without the caller having to rename it.
func WalkContent(path string) ([]ContentFile, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("`content` path %q: %w", path, err)
	}

	var files []ContentFile
	var total int64

	if !info.IsDir() {
		files = append(files, ContentFile{Rel: "index.html", Abs: path, Size: info.Size()})
		return files, info.Size(), nil
	}

	err = filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		files = append(files, ContentFile{Rel: filepath.ToSlash(rel), Abs: p, Size: fi.Size()})
		total += fi.Size()
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("`content` path %q: %w", path, err)
	}
	if len(files) == 0 {
		return nil, 0, fmt.Errorf("`content` path %q contains no files", path)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return files, total, nil
}

// ContentDigest is a stable sha256 over the payload: each file's relative path,
// then its bytes. Identical content therefore yields an identical digest, so
// redeploying unchanged content is a no-op and a rollback is a matter of
// pointing at an earlier digest.
func ContentDigest(files []ContentFile) (string, error) {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\n", f.Rel)
		fh, err := os.Open(f.Abs)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, fh); err != nil {
			fh.Close()
			return "", err
		}
		fh.Close()
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ContentKeyPrefix is where a digest's files live in ContentBucket.
func ContentKeyPrefix(project, name, digest string) string {
	return fmt.Sprintf("%s/%s/%s/%s", ContentPrefix, project, name, digest)
}

// ContentArtifactSource is the Nomad artifact source for a content digest.
// go-getter's s3 scheme is used so Nomad fetches straight from MinIO on the
// node, rather than the content being baked into an image.
func ContentArtifactSource(endpoint, project, name, digest string) string {
	ep := strings.TrimSuffix(endpoint, "/")
	return fmt.Sprintf("s3::%s/%s/%s/", ep, ContentBucket, ContentKeyPrefix(project, name, digest))
}

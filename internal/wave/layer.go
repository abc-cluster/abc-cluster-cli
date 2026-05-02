package wave

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// LayerDigests holds the SHA-256 digests required by the Wave REST API.
type LayerDigests struct {
	GzipDigest string // sha256:<hex> of the raw gzip bytes
	TarDigest  string // sha256:<hex> of the decompressed tar bytes
	GzipSize   int64  // byte length of the gzip bytes
}

// ComputeLayerDigests computes the gzip digest, tar digest, and gzip size
// from raw layer bytes (the .tar.gz produced by PackToolsLayer).
func ComputeLayerDigests(gzipData []byte) (LayerDigests, error) {
	gzipSum := sha256.Sum256(gzipData)

	gr, err := gzip.NewReader(bytes.NewReader(gzipData))
	if err != nil {
		return LayerDigests{}, fmt.Errorf("layer digests: open gzip: %w", err)
	}
	tarBytes, err := io.ReadAll(gr)
	gr.Close()
	if err != nil {
		return LayerDigests{}, fmt.Errorf("layer digests: decompress: %w", err)
	}
	tarSum := sha256.Sum256(tarBytes)

	return LayerDigests{
		GzipDigest: fmt.Sprintf("sha256:%x", gzipSum),
		TarDigest:  fmt.Sprintf("sha256:%x", tarSum),
		GzipSize:   int64(len(gzipData)),
	}, nil
}

// ToolBin is a named binary to include in a Wave layer tar.gz.
type ToolBin struct {
	Name string // filename inside usr/local/bin/
	Path string // local filesystem path
}

// PackToolsLayer packages binaries into a tar.gz archive at usr/local/bin/<name>
// with mode 0755. The archive can be extracted to a staging directory and passed
// to the wave CLI via --layer <dir> for container augmentation.
func PackToolsLayer(tools []ToolBin) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for _, t := range tools {
		f, err := os.Open(t.Path)
		if err != nil {
			return nil, fmt.Errorf("layer: open %s: %w", t.Path, err)
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("layer: stat %s: %w", t.Path, err)
		}
		hdr := &tar.Header{
			Name: "usr/local/bin/" + t.Name,
			Mode: 0755,
			Size: info.Size(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			f.Close()
			return nil, fmt.Errorf("layer: write header %s: %w", t.Name, err)
		}
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return nil, fmt.Errorf("layer: copy %s: %w", t.Name, err)
		}
		f.Close()
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("layer: close tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("layer: close gzip: %w", err)
	}
	return buf.Bytes(), nil
}

// ExtractToolsLayer extracts a tar.gz layer archive into destDir, stripping the
// leading "usr/local/bin/" prefix. Only files under that prefix are extracted;
// other entries are silently skipped. Each extracted file is marked 0755.
func ExtractToolsLayer(data []byte, destDir string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("layer: open gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	const prefix = "usr/local/bin/"
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("layer: read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if len(hdr.Name) <= len(prefix) || hdr.Name[:len(prefix)] != prefix {
			continue
		}
		name := hdr.Name[len(prefix):]
		if name == "" {
			continue
		}
		dst := destDir + "/" + name
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return fmt.Errorf("layer: create %s: %w", dst, err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return fmt.Errorf("layer: write %s: %w", dst, err)
		}
		out.Close()
	}
	return nil
}

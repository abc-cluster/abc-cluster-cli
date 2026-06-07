package data

// compress.go — `abc data compress` / `abc data decompress`
//
// Local porcelain compression, mirroring cmd/data/crypt.go (encrypt/decrypt):
// a streaming engine (read input → transform → write output) plus the two
// cobra commands and the compressForUpload helper used by `abc data upload`.
//
// Engine: zstd (github.com/klauspost/compress/zstd, pure Go, no cgo).
//
// Two safety rules, applied everywhere compression runs:
//   - BGZF-aware pass-through: already-compressed inputs (gzip / BGZF / zstd,
//     detected by magic bytes) are never silently recompressed; recompressing a
//     BGZF file would break tabix/CSI random access.
//   - Self-describing artifact: a zstd *skippable frame* is prepended to the
//     standard zstd stream carrying the original filename, size, and sha256, so
//     `decompress` can verify integrity (and `--replace` is verify-then-delete).
//     The result is still decompressible by stock `zstd -d`.
//
// Spec: specs/active/abc-data-compress.md

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/spf13/cobra"
)

const (
	zstdDefaultSuffix = ".zst"

	// abcSkipMagic is the zstd skippable-frame magic abc writes. RFC 8878
	// reserves 0x184D2A50–0x184D2A5F for skippable frames; conformant decoders
	// (including stock `zstd -d`) ignore them.
	abcSkipMagic       uint32 = 0x184D2A50
	abcSkipMagicMin    uint32 = 0x184D2A50
	abcSkipMagicMax    uint32 = 0x184D2A5F
	abcFrameVersion    byte   = 0x01
	zstdMagicFirstByte        = 0x28 // 28 b5 2f fd

	compressReadBuf = 256 * 1024
)

// ── compression-kind classification ─────────────────────────────────────────

type compressionKind int

const (
	kindRaw compressionKind = iota
	kindGzip
	kindBgzf
	kindZstd
)

func (k compressionKind) String() string {
	switch k {
	case kindGzip:
		return "gzip"
	case kindBgzf:
		return "bgzf"
	case kindZstd:
		return "zstd"
	default:
		return "raw"
	}
}

// classifyBytes inspects a file header. BGZF is gzip (1f 8b) with the FEXTRA
// flag set and a "BC" subfield (SI1=66, SI2=67) at offset 12–13 (BAM spec).
func classifyBytes(h []byte) compressionKind {
	if len(h) >= 4 && h[0] == 0x28 && h[1] == 0xb5 && h[2] == 0x2f && h[3] == 0xfd {
		return kindZstd
	}
	if len(h) >= 2 && h[0] == 0x1f && h[1] == 0x8b {
		if len(h) >= 14 && h[3]&0x04 != 0 && h[12] == 66 && h[13] == 67 {
			return kindBgzf
		}
		return kindGzip
	}
	return kindRaw
}

func classifyCompression(path string) (compressionKind, error) {
	f, err := os.Open(path)
	if err != nil {
		return kindRaw, err
	}
	defer f.Close()
	h := make([]byte, 18)
	n, err := io.ReadFull(f, h)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return kindRaw, err
	}
	return classifyBytes(h[:n]), nil
}

// isZstdDecodable reports whether path begins with the zstd frame magic or any
// skippable-frame magic (abc-produced files lead with the skippable frame).
func isZstdDecodable(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := make([]byte, 4)
	n, err := io.ReadFull(f, h)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false, err
	}
	if n < 4 {
		return false, nil
	}
	if h[0] == 0x28 && h[1] == 0xb5 && h[2] == 0x2f && h[3] == 0xfd {
		return true, nil
	}
	magic := binary.LittleEndian.Uint32(h)
	return magic >= abcSkipMagicMin && magic <= abcSkipMagicMax, nil
}

// ── skippable frame ──────────────────────────────────────────────────────────

type frameMeta struct {
	origName string
	origSize int64
	origSum  [32]byte
}

// buildSkipFrame returns magic(4 LE) + payloadLen(4 LE) + payload, where the
// payload is: version(u8) | origSize(u64 LE) | sha256(32) | nameLen(u16 LE) | name.
func buildSkipFrame(name string, size int64, sum [32]byte) []byte {
	nameBytes := []byte(filepath.Base(name))
	if len(nameBytes) > 0xffff {
		nameBytes = nameBytes[:0xffff]
	}
	payload := make([]byte, 0, 1+8+32+2+len(nameBytes))
	payload = append(payload, abcFrameVersion)
	var b8 [8]byte
	binary.LittleEndian.PutUint64(b8[:], uint64(size))
	payload = append(payload, b8[:]...)
	payload = append(payload, sum[:]...)
	var b2 [2]byte
	binary.LittleEndian.PutUint16(b2[:], uint16(len(nameBytes)))
	payload = append(payload, b2[:]...)
	payload = append(payload, nameBytes...)

	out := make([]byte, 0, 8+len(payload))
	var m [4]byte
	binary.LittleEndian.PutUint32(m[:], abcSkipMagic)
	out = append(out, m[:]...)
	var s [4]byte
	binary.LittleEndian.PutUint32(s[:], uint32(len(payload)))
	out = append(out, s[:]...)
	out = append(out, payload...)
	return out
}

// readSkipFrame consumes a leading skippable frame from br if present. It
// returns the parsed abc metadata when the frame is an abc frame; for a foreign
// skippable frame it consumes the frame and returns (nil, nil); when no
// skippable frame leads the stream it consumes nothing and returns (nil, nil)
// so the zstd decoder can read from the start.
func readSkipFrame(br *bufio.Reader) (*frameMeta, error) {
	head, err := br.Peek(8)
	if err != nil {
		return nil, nil // too short to hold a frame header; let the decoder fail
	}
	magic := binary.LittleEndian.Uint32(head[:4])
	if magic < abcSkipMagicMin || magic > abcSkipMagicMax {
		return nil, nil // standard zstd frame (or garbage) — do not consume
	}
	size := binary.LittleEndian.Uint32(head[4:8])
	if _, err := br.Discard(8); err != nil {
		return nil, err
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(br, payload); err != nil {
		return nil, fmt.Errorf("truncated skippable frame: %w", err)
	}
	// Foreign skippable frame, or one we don't understand → no integrity meta.
	if len(payload) < 1+8+32+2 || payload[0] != abcFrameVersion {
		return nil, nil
	}
	var m frameMeta
	off := 1
	m.origSize = int64(binary.LittleEndian.Uint64(payload[off : off+8]))
	off += 8
	copy(m.origSum[:], payload[off:off+32])
	off += 32
	nameLen := int(binary.LittleEndian.Uint16(payload[off : off+2]))
	off += 2
	if off+nameLen <= len(payload) {
		m.origName = string(payload[off : off+nameLen])
	}
	return &m, nil
}

// ── engine config ────────────────────────────────────────────────────────────

type compressConfig struct {
	level zstd.EncoderLevel
}

func parseCompressLevel(s string) (zstd.EncoderLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "default":
		return zstd.SpeedDefault, nil
	case "fast":
		return zstd.SpeedFastest, nil
	case "better":
		return zstd.SpeedBetterCompression, nil
	case "best":
		return zstd.SpeedBestCompression, nil
	default:
		return 0, fmt.Errorf("invalid --level %q (want: fast | default | better | best)", s)
	}
}

func newCompressConfig(level string) (*compressConfig, error) {
	lvl, err := parseCompressLevel(level)
	if err != nil {
		return nil, err
	}
	return &compressConfig{level: lvl}, nil
}

// sha256AndSize returns the sha256 and byte length of path, honouring ctx.
func sha256AndSize(ctx context.Context, path string) ([32]byte, int64, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, 0, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, compressReadBuf)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return sum, total, err
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			total += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return sum, total, rerr
		}
	}
	copy(sum[:], h.Sum(nil))
	return sum, total, nil
}

// compressFile writes the skippable frame then the zstd stream of sourcePath to
// out. It returns the original sha256 (hex) and size.
func (c *compressConfig) compressFile(ctx context.Context, sourcePath string, out io.Writer, onProgress func(int64)) (string, int64, error) {
	sum, size, err := sha256AndSize(ctx, sourcePath)
	if err != nil {
		return "", 0, err
	}
	if _, err := out.Write(buildSkipFrame(sourcePath, size, sum)); err != nil {
		return "", 0, err
	}
	in, err := os.Open(sourcePath)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	enc, err := zstd.NewWriter(out,
		zstd.WithEncoderLevel(c.level),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		return "", 0, err
	}
	buf := make([]byte, compressReadBuf)
	for {
		if cerr := ctx.Err(); cerr != nil {
			_ = enc.Close()
			return "", 0, cerr
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := enc.Write(buf[:n]); werr != nil {
				_ = enc.Close()
				return "", 0, werr
			}
			if onProgress != nil {
				onProgress(int64(n))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = enc.Close()
			return "", 0, rerr
		}
	}
	if err := enc.Close(); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(sum[:]), size, nil
}

func (c *compressConfig) compressToPathWithProgress(ctx context.Context, sourcePath, destPath string, onProgress func(int64)) error {
	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, _, err := c.compressFile(ctx, sourcePath, out, onProgress); err != nil {
		out.Close()
		_ = os.Remove(destPath)
		return err
	}
	return out.Close()
}

// compressToTempFileWithProgress compresses sourcePath to a temp file under
// uploadTempDir and returns the temp path, a cleanup func, the original sha256
// (hex), and the original size. Mirrors encryptToTempFileWithProgress.
func (c *compressConfig) compressToTempFileWithProgress(ctx context.Context, sourcePath string, onProgress func(int64)) (string, func() error, string, int64, error) {
	tmpDir, err := uploadTempDir()
	if err != nil {
		return "", nil, "", 0, err
	}
	tmp, err := os.CreateTemp(tmpDir, "abc-zstd-*")
	if err != nil {
		return "", nil, "", 0, err
	}
	tmpPath := tmp.Name()
	cleanup := func() error { return os.Remove(tmpPath) }
	sumHex, size, err := c.compressFile(ctx, sourcePath, tmp, onProgress)
	if err != nil {
		tmp.Close()
		_ = cleanup()
		return "", nil, "", 0, err
	}
	if err := tmp.Close(); err != nil {
		_ = cleanup()
		return "", nil, "", 0, err
	}
	return tmpPath, cleanup, sumHex, size, nil
}

// decompressToPath writes the decompressed contents of sourcePath to destPath,
// verifying the embedded sha256 when an abc integrity frame is present. It
// returns the frame metadata (nil when the input is a stock zstd file with no
// abc frame, in which case verification is skipped).
func (c *compressConfig) decompressToPath(ctx context.Context, sourcePath, destPath string, onProgress func(int64)) (*frameMeta, error) {
	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	meta, err := decompressToWriter(ctx, sourcePath, out, onProgress)
	if err != nil {
		out.Close()
		_ = os.Remove(destPath)
		return nil, err
	}
	return meta, out.Close()
}

func decompressToWriter(ctx context.Context, sourcePath string, out io.Writer, onProgress func(int64)) (*frameMeta, error) {
	in, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	br := bufio.NewReaderSize(in, compressReadBuf)
	meta, err := readSkipFrame(br)
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(br)
	if err != nil {
		return nil, err
	}
	defer dec.Close()

	h := sha256.New()
	mw := io.MultiWriter(out, h)
	buf := make([]byte, compressReadBuf)
	for {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		n, rerr := dec.Read(buf)
		if n > 0 {
			if _, werr := mw.Write(buf[:n]); werr != nil {
				return nil, werr
			}
			if onProgress != nil {
				onProgress(int64(n))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, fmt.Errorf("decompress: %w", rerr)
		}
	}
	if meta != nil {
		var got [32]byte
		copy(got[:], h.Sum(nil))
		if got != meta.origSum {
			return nil, fmt.Errorf("integrity check failed: decompressed sha256 does not match the value recorded at compression time")
		}
	}
	return meta, nil
}

// ── compress command ──────────────────────────────────────────────────────────

type compressOptions struct {
	inputPath  string
	outputPath string
	outputDir  string
	level      string
	progress   bool
	force      bool
	replace    bool
}

func newCompressCmd() *cobra.Command {
	opts := &compressOptions{}
	cmd := &cobra.Command{
		Use:   "compress <path>",
		Short: "Compress a file or folder with zstd (BGZF-aware)",
		Long: `Compress a local file or folder with zstd.

Raw files are compressed to <name>.zst. Already-compressed inputs (gzip, BGZF,
zstd) are passed through unchanged — recompressing them gains nothing and would
break BGZF (.bam / tabix-indexed .vcf.gz) random access.

Each .zst carries an integrity frame (original name, size, sha256) so that
'abc data decompress' can verify the result, and --replace can safely delete the
source after a verified round-trip. The output is still readable by stock zstd.

  # Compress a raw VCF (≈8–10× on real genomic text):
  abc data compress ./calls.vcf

  # Best ratio, and delete the source after a verified round-trip:
  abc data compress ./calls.vcf --level best --replace`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.inputPath = args[0]
			return runCompress(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.outputPath, "output", "", "output file path for single-file compression")
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", "", "output directory for folder compression")
	cmd.Flags().StringVar(&opts.level, "level", "default", "compression level: fast | default | better | best")
	cmd.Flags().BoolVar(&opts.progress, "progress", true, "show live progress bars for compression")
	cmd.Flags().BoolVarP(&opts.force, "force", "f", false,
		"overwrite the output if it already exists — sha256-compares old vs new; identical content is a no-op (default: refuse)")
	cmd.Flags().BoolVar(&opts.replace, "replace", false,
		"delete the source after a successful, verified compression (only the .zst is kept)")
	return cmd
}

func runCompress(cmd *cobra.Command, opts *compressOptions) error {
	info, err := os.Stat(opts.inputPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return inputError("path %q does not exist; verify the path and try again", opts.inputPath)
		}
		return localIOError("failed to access path %q: %w", opts.inputPath, err)
	}
	if opts.outputPath != "" && info.IsDir() {
		return fmt.Errorf("--output can only be used when compressing a single file")
	}
	if opts.outputDir != "" && !info.IsDir() {
		return fmt.Errorf("--output-dir can only be used when compressing a directory")
	}
	comp, err := newCompressConfig(opts.level)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return compressDirectory(cmd, comp, opts)
	}
	return compressSingleFile(cmd, comp, opts.inputPath, opts.outputPath, info.Size(), opts.progress, opts.force, opts.replace)
}

func compressSingleFile(cmd *cobra.Command, comp *compressConfig, sourcePath, outputPath string, size int64, progressEnabled, force, replace bool) error {
	kind, err := classifyCompression(sourcePath)
	if err != nil {
		return localIOError("failed to inspect %q: %w", sourcePath, err)
	}
	if kind != kindRaw {
		fmt.Fprintf(cmd.OutOrStdout(), "skipped %s: already %s (passed through, not recompressed)\n", filepath.Base(sourcePath), kind)
		return nil
	}
	if outputPath == "" {
		outputPath = sourcePath + zstdDefaultSuffix
	}
	progress := newProgressReporter(cmd.OutOrStdout(), progressEnabled, fmt.Sprintf("Compressing %s", filepath.Base(sourcePath)), size)
	err = writeWithCollisionCheck(outputPath, force, cmd.ErrOrStderr(),
		func(p string) error {
			return comp.compressToPathWithProgress(cmd.Context(), sourcePath, p, func(n int64) { progress.Add(n) })
		})
	if err != nil {
		_ = progress.Complete()
		return fmt.Errorf("failed to compress %q: %w", sourcePath, err)
	}
	if doneErr := progress.Complete(); doneErr != nil {
		return fmt.Errorf("failed to render compression progress: %w", doneErr)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "File compressed successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", outputPath)
	if replace {
		if err := verifyRoundTrip(cmd.Context(), outputPath); err != nil {
			return fmt.Errorf("compress succeeded but --replace verification failed (source kept): %w", err)
		}
		if err := os.Remove(sourcePath); err != nil {
			return fmt.Errorf("compress succeeded but --replace failed to delete source %q: %w", sourcePath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Removed source: %s\n", sourcePath)
	}
	return nil
}

func compressDirectory(cmd *cobra.Command, comp *compressConfig, opts *compressOptions) error {
	files, err := collectFiles(opts.inputPath)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no files found in directory %q", opts.inputPath)
	}
	outputDir := opts.outputDir
	if outputDir == "" {
		outputDir = opts.inputPath + "-compressed"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %q: %w", outputDir, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Compressing %d files...\n", len(files))
	for _, file := range files {
		relPath, err := filepath.Rel(opts.inputPath, file.path)
		if err != nil {
			return fmt.Errorf("failed to resolve path for %q: %w", file.path, err)
		}
		kind, err := classifyCompression(file.path)
		if err != nil {
			return localIOError("failed to inspect %q: %w", relPath, err)
		}
		if kind != kindRaw {
			fmt.Fprintf(cmd.OutOrStdout(), "skipped %s: already %s\n", relPath, kind)
			continue
		}
		destPath := filepath.Join(outputDir, relPath) + zstdDefaultSuffix
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create output directory %q: %w", filepath.Dir(destPath), err)
		}
		progress := newProgressReporter(cmd.OutOrStdout(), opts.progress, fmt.Sprintf("Compressing %s", relPath), file.size)
		writeErr := writeWithCollisionCheck(destPath, opts.force, cmd.ErrOrStderr(),
			func(p string) error {
				return comp.compressToPathWithProgress(cmd.Context(), file.path, p, func(n int64) { progress.Add(n) })
			})
		if writeErr != nil {
			_ = progress.Complete()
			return fmt.Errorf("failed to compress %q: %w", relPath, writeErr)
		}
		if doneErr := progress.Complete(); doneErr != nil {
			return fmt.Errorf("failed to render compression progress: %w", doneErr)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Compressed %s\n  Output: %s\n", relPath, destPath)
		if opts.replace {
			if err := verifyRoundTrip(cmd.Context(), destPath); err != nil {
				return fmt.Errorf("compress succeeded but --replace verification failed for %q (source kept): %w", relPath, err)
			}
			if err := os.Remove(file.path); err != nil {
				return fmt.Errorf("compress succeeded but --replace failed to delete source %q: %w", file.path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  Removed source: %s\n", file.path)
		}
	}
	return nil
}

// verifyRoundTrip decompresses path into io.Discard, verifying the embedded
// integrity frame. Used by --replace before deleting a source.
func verifyRoundTrip(ctx context.Context, path string) error {
	meta, err := decompressToWriter(ctx, path, io.Discard, nil)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("no integrity frame to verify against")
	}
	return nil
}

// ── decompress command ──────────────────────────────────────────────────────

type decompressOptions struct {
	inputPath  string
	outputPath string
	outputDir  string
	progress   bool
	force      bool
	replace    bool
}

func newDecompressCmd() *cobra.Command {
	opts := &decompressOptions{}
	cmd := &cobra.Command{
		Use:   "decompress <path>",
		Short: "Decompress a zstd file or folder produced by abc data compress",
		Long: `Decompress a local zstd file or folder.

When the file carries an abc integrity frame, the decompressed output is verified
against the original sha256 before being trusted. Files produced by stock zstd
(no integrity frame) decompress too, with a note that verification was skipped.

  abc data decompress ./calls.vcf.zst
  abc data decompress ./calls.vcf.zst --output ./restored.vcf --replace`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.inputPath = args[0]
			return runDecompress(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.outputPath, "output", "", "output file path for single-file decompression")
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", "", "output directory for folder decompression")
	cmd.Flags().BoolVar(&opts.progress, "progress", true, "show live progress bars for decompression")
	cmd.Flags().BoolVarP(&opts.force, "force", "f", false,
		"overwrite the output if it already exists — sha256-compares old vs new; identical content is a no-op (default: refuse)")
	cmd.Flags().BoolVar(&opts.replace, "replace", false,
		"remove the source .zst after successful, verified decompression")
	return cmd
}

func runDecompress(cmd *cobra.Command, opts *decompressOptions) error {
	info, err := os.Stat(opts.inputPath)
	if err != nil {
		return fmt.Errorf("failed to access path %q: %w", opts.inputPath, err)
	}
	if opts.outputPath != "" && info.IsDir() {
		return fmt.Errorf("--output can only be used when decompressing a single file")
	}
	if opts.outputDir != "" && !info.IsDir() {
		return fmt.Errorf("--output-dir can only be used when decompressing a directory")
	}
	comp := &compressConfig{}
	if info.IsDir() {
		return decompressDirectory(cmd, comp, opts)
	}
	return decompressSingleFile(cmd, comp, opts.inputPath, opts.outputPath, info.Size(), opts.progress, opts.force, opts.replace)
}

// resolveDecompressOutput chooses the destination: explicit --output, else the
// source with its .zst suffix stripped, else the frame's recorded original name
// (in the source's directory), else error.
func resolveDecompressOutput(sourcePath, outputPath string, meta *frameMeta) (string, error) {
	if outputPath != "" {
		return outputPath, nil
	}
	if strings.HasSuffix(sourcePath, zstdDefaultSuffix) {
		trimmed := strings.TrimSuffix(sourcePath, zstdDefaultSuffix)
		if trimmed != "" {
			return trimmed, nil
		}
	}
	if meta != nil && meta.origName != "" {
		return filepath.Join(filepath.Dir(sourcePath), meta.origName), nil
	}
	return "", fmt.Errorf(
		"cannot determine output path: %q has no %q suffix and no recorded original name\n"+
			"  pass --output <path> to specify where to write the decompressed file",
		sourcePath, zstdDefaultSuffix)
}

func decompressSingleFile(cmd *cobra.Command, comp *compressConfig, sourcePath, outputPath string, size int64, progressEnabled, force, replace bool) error {
	ok, err := isZstdDecodable(sourcePath)
	if err != nil {
		return localIOError("failed to inspect %q: %w", sourcePath, err)
	}
	if !ok {
		return inputError("not a zstd file: %q", sourcePath)
	}
	// Peek the frame first so output naming can fall back to the recorded name.
	peekMeta, _ := peekFrame(sourcePath)
	outputPath, err = resolveDecompressOutput(sourcePath, outputPath, peekMeta)
	if err != nil {
		return err
	}
	progress := newProgressReporter(cmd.OutOrStdout(), progressEnabled, fmt.Sprintf("Decompressing %s", filepath.Base(sourcePath)), frameOrFileSize(peekMeta, size))
	var meta *frameMeta
	err = writeWithCollisionCheck(outputPath, force, cmd.ErrOrStderr(),
		func(p string) error {
			m, derr := comp.decompressToPath(cmd.Context(), sourcePath, p, func(n int64) { progress.Add(n) })
			meta = m
			return derr
		})
	if err != nil {
		_ = progress.Complete()
		return fmt.Errorf("failed to decompress %q: %w", sourcePath, err)
	}
	if doneErr := progress.Complete(); doneErr != nil {
		return fmt.Errorf("failed to render decompression progress: %w", doneErr)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "File decompressed successfully.")
	fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", outputPath)
	if meta == nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "  note: no integrity frame, skipping verify")
	}
	if replace {
		if err := os.Remove(sourcePath); err != nil {
			return fmt.Errorf("decompress succeeded but --replace failed to delete source %q: %w", sourcePath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Removed source: %s\n", sourcePath)
	}
	return nil
}

func decompressDirectory(cmd *cobra.Command, comp *compressConfig, opts *decompressOptions) error {
	files, err := collectFiles(opts.inputPath)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no files found in directory %q", opts.inputPath)
	}
	outputDir := opts.outputDir
	if outputDir == "" {
		outputDir = opts.inputPath + "-decompressed"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %q: %w", outputDir, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Decompressing files in %s...\n", opts.inputPath)
	for _, file := range files {
		relPath, err := filepath.Rel(opts.inputPath, file.path)
		if err != nil {
			return fmt.Errorf("failed to resolve path for %q: %w", file.path, err)
		}
		ok, err := isZstdDecodable(file.path)
		if err != nil {
			return localIOError("failed to inspect %q: %w", relPath, err)
		}
		if !ok {
			fmt.Fprintf(cmd.OutOrStdout(), "skipped %s: not a zstd file\n", relPath)
			continue
		}
		base := relPath
		if strings.HasSuffix(base, zstdDefaultSuffix) {
			base = strings.TrimSuffix(base, zstdDefaultSuffix)
		}
		destPath := filepath.Join(outputDir, base)
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create output directory %q: %w", filepath.Dir(destPath), err)
		}
		var meta *frameMeta
		if err := writeWithCollisionCheck(destPath, opts.force, cmd.ErrOrStderr(),
			func(p string) error {
				m, derr := comp.decompressToPath(cmd.Context(), file.path, p, nil)
				meta = m
				return derr
			}); err != nil {
			return fmt.Errorf("failed to decompress %q: %w", relPath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Decompressed %s\n  Output: %s\n", relPath, destPath)
		if meta == nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  note: no integrity frame for %s, skipping verify\n", relPath)
		}
		if opts.replace {
			if err := os.Remove(file.path); err != nil {
				return fmt.Errorf("decompress succeeded but --replace failed to delete source %q: %w", file.path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  Removed source: %s\n", file.path)
		}
	}
	return nil
}

// peekFrame reads (without decompressing) any leading abc integrity frame.
func peekFrame(path string) (*frameMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, compressReadBuf)
	return readSkipFrame(br)
}

func frameOrFileSize(meta *frameMeta, fileSize int64) int64 {
	if meta != nil && meta.origSize > 0 {
		return meta.origSize
	}
	return fileSize
}

// ── upload integration ──────────────────────────────────────────────────────

// compressForUpload compresses sourcePath to a temp file when comp is non-nil
// AND the input is raw (uncompressed). Already-compressed inputs are passed
// through unchanged. Mirrors encryptForUpload's (path, cleanup, err) shape, with
// an extra `compressed` flag and the original sha256 for upload metadata.
func compressForUpload(ctx context.Context, sourcePath string, comp *compressConfig, onProgress func(int64)) (path string, cleanup func() error, compressed bool, origSum string, err error) {
	if comp == nil {
		return sourcePath, nil, false, "", nil
	}
	kind, err := classifyCompression(sourcePath)
	if err != nil {
		return "", nil, false, "", err
	}
	if kind != kindRaw {
		return sourcePath, nil, false, "", nil // pass-through
	}
	tmpPath, clean, sumHex, _, err := comp.compressToTempFileWithProgress(ctx, sourcePath, onProgress)
	if err != nil {
		return "", nil, false, "", err
	}
	return tmpPath, clean, true, sumHex, nil
}

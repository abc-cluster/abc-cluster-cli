package data

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestClassifyBytes(t *testing.T) {
	bgzf := []byte{0x1f, 0x8b, 0x08, 0x04, 0, 0, 0, 0, 0, 0xff, 0x06, 0x00, 66, 67, 0x02, 0x00, 0, 0}
	cases := []struct {
		name string
		in   []byte
		want compressionKind
	}{
		{"zstd", []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00}, kindZstd},
		{"bgzf", bgzf, kindBgzf},
		{"plain gzip", []byte{0x1f, 0x8b, 0x08, 0x00, 0, 0, 0, 0, 0, 0xff}, kindGzip},
		{"raw text", []byte("##fileformat=VCFv4.2\n#CHROM\tPOS\n"), kindRaw},
		{"empty", []byte{}, kindRaw},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBytes(tc.in); got != tc.want {
				t.Fatalf("classifyBytes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseCompressLevel(t *testing.T) {
	for _, in := range []string{"", "default", "fast", "better", "best", "BEST", " fast "} {
		if _, err := parseCompressLevel(in); err != nil {
			t.Errorf("parseCompressLevel(%q) unexpected error: %v", in, err)
		}
	}
	if _, err := parseCompressLevel("turbo"); err == nil {
		t.Error("parseCompressLevel(turbo) should error")
	}
}

func TestSkipFrameRoundTrip(t *testing.T) {
	var sum [32]byte
	for i := range sum {
		sum[i] = byte(i)
	}
	frame := buildSkipFrame("/some/dir/calls.vcf", 12345, sum)
	br := bufio.NewReader(bytes.NewReader(frame))
	meta, err := readSkipFrame(br)
	if err != nil {
		t.Fatalf("readSkipFrame: %v", err)
	}
	if meta == nil {
		t.Fatal("expected frame metadata, got nil")
	}
	if meta.origName != "calls.vcf" {
		t.Errorf("origName = %q, want calls.vcf", meta.origName)
	}
	if meta.origSize != 12345 {
		t.Errorf("origSize = %d, want 12345", meta.origSize)
	}
	if meta.origSum != sum {
		t.Errorf("origSum mismatch")
	}
	// The reader must be fully consumed (frame is the whole input here).
	if _, err := br.ReadByte(); err == nil {
		t.Error("expected reader to be drained after the frame")
	}
}

func TestReadSkipFrame_NoFrameDoesNotConsume(t *testing.T) {
	// A standard zstd frame magic must NOT be treated as skippable, and must be
	// left in the buffer for the decoder.
	zMagic := []byte{0x28, 0xb5, 0x2f, 0xfd, 0xAA, 0xBB, 0xCC, 0xDD}
	br := bufio.NewReader(bytes.NewReader(zMagic))
	meta, err := readSkipFrame(br)
	if err != nil {
		t.Fatalf("readSkipFrame: %v", err)
	}
	if meta != nil {
		t.Fatal("expected nil meta for a standard zstd frame")
	}
	peek, _ := br.Peek(4)
	if !bytes.Equal(peek, zMagic[:4]) {
		t.Errorf("zstd magic was consumed; peek=%x", peek)
	}
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestCompressDecompressRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// Redundant text compresses well and round-trips exactly.
	orig := []byte(strings.Repeat("##contig=<ID=chr1,length=248956422>\n", 5000))
	src := writeFile(t, dir, "calls.vcf", orig)

	comp, err := newCompressConfig("default")
	if err != nil {
		t.Fatal(err)
	}
	dst := src + zstdDefaultSuffix
	ctx := context.Background()
	if err := comp.compressToPathWithProgress(ctx, src, dst, nil); err != nil {
		t.Fatalf("compress: %v", err)
	}

	// Output begins with the abc skippable frame and is recognised as zstd.
	ok, err := isZstdDecodable(dst)
	if err != nil || !ok {
		t.Fatalf("isZstdDecodable = %v, %v", ok, err)
	}

	out := filepath.Join(dir, "restored.vcf")
	meta, err := comp.decompressToPath(ctx, dst, out, nil)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if meta == nil {
		t.Fatal("expected integrity frame on abc-produced file")
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, orig) {
		t.Fatal("round-trip mismatch")
	}
	if meta.origName != "calls.vcf" {
		t.Errorf("frame origName = %q", meta.origName)
	}
}

func TestDecompressStockZstdNoFrame(t *testing.T) {
	dir := t.TempDir()
	orig := []byte("plain payload with no abc frame\n")
	// Produce a stock zstd stream (no skippable frame).
	var buf bytes.Buffer
	enc, _ := zstd.NewWriter(&buf)
	_, _ = enc.Write(orig)
	_ = enc.Close()
	stock := writeFile(t, dir, "stock.zst", buf.Bytes())

	out := filepath.Join(dir, "stock.out")
	meta, err := (&compressConfig{}).decompressToPath(context.Background(), stock, out, nil)
	if err != nil {
		t.Fatalf("decompress stock zstd: %v", err)
	}
	if meta != nil {
		t.Fatal("stock zstd should have no abc integrity frame")
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, orig) {
		t.Fatal("stock zstd round-trip mismatch")
	}
}

func TestDecompressDetectsCorruption(t *testing.T) {
	dir := t.TempDir()
	orig := []byte(strings.Repeat("ACGTACGTACGT\n", 2000))
	src := writeFile(t, dir, "seq.txt", orig)
	comp, _ := newCompressConfig("fast")
	dst := src + zstdDefaultSuffix
	ctx := context.Background()
	if err := comp.compressToPathWithProgress(ctx, src, dst, nil); err != nil {
		t.Fatal(err)
	}
	// Flip a byte deep in the zstd payload (past the skippable frame).
	raw, _ := os.ReadFile(dst)
	if len(raw) < 80 {
		t.Fatalf("compressed file unexpectedly small: %d", len(raw))
	}
	raw[len(raw)-10] ^= 0xff
	corrupt := writeFile(t, dir, "seq.txt.corrupt.zst", raw)
	if _, err := comp.decompressToPath(ctx, corrupt, filepath.Join(dir, "out"), nil); err == nil {
		t.Fatal("expected decompress of corrupted data to fail")
	}
}

func TestCompressForUpload_PassThrough(t *testing.T) {
	dir := t.TempDir()
	// A stock zstd file: already compressed → pass-through, no temp.
	var buf bytes.Buffer
	enc, _ := zstd.NewWriter(&buf)
	_, _ = enc.Write([]byte("already compressed"))
	_ = enc.Close()
	zpath := writeFile(t, dir, "data.zst", buf.Bytes())

	comp, _ := newCompressConfig("default")
	path, cleanup, compressed, _, err := compressForUpload(context.Background(), zpath, comp, nil)
	if err != nil {
		t.Fatalf("compressForUpload: %v", err)
	}
	if compressed {
		t.Error("already-compressed input should pass through, not compress")
	}
	if path != zpath {
		t.Errorf("pass-through should return the source path; got %q", path)
	}
	if cleanup != nil {
		t.Error("pass-through should not create a temp file (cleanup should be nil)")
	}
}

func TestCompressForUpload_NilConfig(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "raw.txt", []byte("hello"))
	path, cleanup, compressed, _, err := compressForUpload(context.Background(), src, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if compressed || cleanup != nil || path != src {
		t.Error("nil compressor must be a no-op pass-through")
	}
}

func TestCompressForUpload_RawSumMatches(t *testing.T) {
	dir := t.TempDir()
	data := []byte(strings.Repeat("raw genomic text\n", 1000))
	src := writeFile(t, dir, "raw.vcf", data)
	comp, _ := newCompressConfig("default")
	path, cleanup, compressed, sumHex, err := compressForUpload(context.Background(), src, comp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer func() { _ = cleanup() }()
	}
	if !compressed {
		t.Fatal("raw input should be compressed")
	}
	if path == src {
		t.Fatal("compressed upload should use a temp path, not the source")
	}
	want := sha256.Sum256(data)
	if sumHex != hexString(want[:]) {
		t.Errorf("reported original sha256 %q does not match the source", sumHex)
	}
}

func hexString(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}

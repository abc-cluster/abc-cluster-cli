package data

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bdragon300/tusgo"
)

func TestTusUploader_CreateUploadIncludesChecksumMetadata(t *testing.T) {
	payload := "checksum-payload"
	tmpFile := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(tmpFile, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}

	var gotMetadata map[string]string
	var gotPatchBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			metadata, err := tusgo.DecodeMetadata(r.Header.Get("Upload-Metadata"))
			if err != nil {
				t.Fatalf("decode metadata: %v", err)
			}
			gotMetadata = metadata
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Location", "/files/upload-1")
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", "0")
			w.Header().Set("Upload-Length", "16")
			w.WriteHeader(http.StatusOK)
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read patch body: %v", err)
			}
			gotPatchBody = string(body)
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", "16")
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	uploader, err := newTusUploader(server.URL+"/files/", "", UploaderOptions{})
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}

	location, err := uploader.Upload(context.Background(), tmpFile, map[string]string{"filename": "payload.txt"})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if location == "" {
		t.Fatal("expected non-empty location")
	}
	if gotMetadata["filename"] != "payload.txt" {
		t.Fatalf("expected filename metadata, got %q", gotMetadata["filename"])
	}
	checksum := gotMetadata["checksum"]
	if !strings.HasPrefix(checksum, "sha256:") {
		t.Fatalf("expected sha256 checksum metadata, got %q", checksum)
	}
	if len(checksum) != len("sha256:")+64 {
		t.Fatalf("expected checksum length %d, got %d", len("sha256:")+64, len(checksum))
	}
	if gotPatchBody != payload {
		t.Fatalf("expected patch body %q, got %q", payload, gotPatchBody)
	}
}

func TestTusUploader_ChecksumCanBeDisabledViaMetadataMarker(t *testing.T) {
	payload := "checksum-disabled-payload"
	tmpFile := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(tmpFile, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}

	var gotMetadata map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			metadata, err := tusgo.DecodeMetadata(r.Header.Get("Upload-Metadata"))
			if err != nil {
				t.Fatalf("decode metadata: %v", err)
			}
			gotMetadata = metadata
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Location", "/files/upload-2")
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", "0")
			w.Header().Set("Upload-Length", "25")
			w.WriteHeader(http.StatusOK)
		case http.MethodPatch:
			_, err := io.Copy(io.Discard, r.Body)
			if err != nil {
				t.Fatalf("read patch body: %v", err)
			}
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", "25")
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	uploader, err := newTusUploader(server.URL+"/files/", "", UploaderOptions{})
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}

	_, err = uploader.Upload(context.Background(), tmpFile, map[string]string{
		"filename": "payload.txt",
		"checksum": "",
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if gotMetadata["filename"] != "payload.txt" {
		t.Fatalf("expected filename metadata, got %q", gotMetadata["filename"])
	}
	if _, ok := gotMetadata["checksum"]; ok {
		t.Fatalf("expected checksum metadata to be omitted, got %q", gotMetadata["checksum"])
	}
}

func TestTusUploader_ResumesAfterTransientPatchFailure(t *testing.T) {
	payload := strings.Repeat("abc123", 512)
	tmpFile := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(tmpFile, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var uploaded strings.Builder
	uploaded.Grow(len(payload))
	uploadLength := len(payload)
	uploadOffset := 0
	patchCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Location", "/files/upload-resume")
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			mu.Lock()
			offset := uploadOffset
			mu.Unlock()
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.Itoa(offset))
			w.Header().Set("Upload-Length", strconv.Itoa(uploadLength))
			w.WriteHeader(http.StatusOK)
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read patch body: %v", err)
			}

			mu.Lock()
			expectedOffset := strconv.Itoa(uploadOffset)
			patchCalls++
			call := patchCalls
			mu.Unlock()

			if got := r.Header.Get("Upload-Offset"); got != expectedOffset {
				t.Fatalf("unexpected patch offset: got %s want %s", got, expectedOffset)
			}

			if call == 1 {
				half := len(body) / 2
				if half == 0 {
					half = len(body)
				}

				mu.Lock()
				uploaded.WriteString(string(body[:half]))
				uploadOffset += half
				mu.Unlock()

				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Fatal("response writer does not support hijacking")
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					t.Fatalf("hijack: %v", err)
				}
				_ = conn.Close()
				return
			}

			mu.Lock()
			uploaded.WriteString(string(body))
			uploadOffset += len(body)
			offset := uploadOffset
			mu.Unlock()

			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.Itoa(offset))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	uploader, err := newTusUploader(server.URL+"/files/", "", UploaderOptions{})
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}

	location, err := uploader.Upload(context.Background(), tmpFile, map[string]string{"filename": "payload.txt"})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if location == "" {
		t.Fatal("expected non-empty location")
	}

	mu.Lock()
	gotPayload := uploaded.String()
	gotPatchCalls := patchCalls
	gotOffset := uploadOffset
	mu.Unlock()

	if gotPatchCalls < 2 {
		t.Fatalf("expected at least 2 patch calls after transient failure, got %d", gotPatchCalls)
	}
	if gotOffset != len(payload) {
		t.Fatalf("expected final offset %d, got %d", len(payload), gotOffset)
	}
	if gotPayload != payload {
		t.Fatalf("uploaded payload mismatch: got %d bytes want %d bytes", len(gotPayload), len(payload))
	}
}

func TestTusUploader_TooLargeErrorIncludesLimit(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	payload := strings.Repeat("x", 32)
	if err := os.WriteFile(tmpFile, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.Header().Set("Tus-Max-Size", "16")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	uploader, err := newTusUploader(server.URL+"/files/", "", UploaderOptions{})
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}

	_, err = uploader.Upload(context.Background(), tmpFile, map[string]string{"filename": "payload.bin"})
	if err == nil {
		t.Fatal("expected too-large error")
	}
	errText := err.Error()
	if !strings.Contains(errText, "file is too large") {
		t.Fatalf("expected helpful too-large message, got %q", errText)
	}
	if !strings.Contains(errText, "Tus-Max-Size is 16 bytes") {
		t.Fatalf("expected Tus-Max-Size detail in error, got %q", errText)
	}
}

func TestTusUploader_ResumesUsingStoredLocationForFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	tmpFile := filepath.Join(t.TempDir(), "payload.txt")
	payload := "hello world"
	if err := os.WriteFile(tmpFile, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	const existingLocation = "/files/upload-existing"
	resumeOffset := int64(6)
	var createCalled bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			if r.URL.Path != existingLocation {
				t.Fatalf("unexpected head path: %s", r.URL.Path)
			}
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.FormatInt(resumeOffset, 10))
			w.Header().Set("Upload-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
		case http.MethodPatch:
			if got := r.Header.Get("Upload-Offset"); got != strconv.FormatInt(resumeOffset, 10) {
				t.Fatalf("unexpected patch offset: got %s want %d", got, resumeOffset)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read patch body: %v", err)
			}
			if string(body) != payload[resumeOffset:] {
				t.Fatalf("expected resumed payload %q, got %q", payload[resumeOffset:], string(body))
			}
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			createCalled = true
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Location", "/files/unexpected-create")
			w.WriteHeader(http.StatusCreated)
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	resumePath, err := uploadResumeStatePath(server.URL+"/files/", tmpFile, info.Size(), info.ModTime().UnixNano())
	if err != nil {
		t.Fatalf("resume path: %v", err)
	}
	if err := storeUploadResumeLocation(resumePath, existingLocation); err != nil {
		t.Fatalf("store resume location: %v", err)
	}

	uploader, err := newTusUploader(server.URL+"/files/", "", UploaderOptions{})
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}

	location, err := uploader.Upload(context.Background(), tmpFile, map[string]string{"filename": "payload.txt"})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if createCalled {
		t.Fatal("expected resume from stored location without creating a new upload")
	}
	if location != existingLocation {
		t.Fatalf("unexpected upload location: got %q want %q", location, existingLocation)
	}
	if _, err := os.Stat(resumePath); !os.IsNotExist(err) {
		t.Fatalf("expected resume state to be cleared, stat err=%v", err)
	}
}

func TestTusUploader_ResumesAfterInterruptedProcess(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	payload := bytes.Repeat([]byte("x"), 8*1024*1024)
	if err := os.WriteFile(tmpFile, payload, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	uploadOffset := int64(0)
	uploadLength := int64(len(payload))
	patchCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Location", "/files/upload-interrupted")
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			mu.Lock()
			offset := uploadOffset
			mu.Unlock()
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
			w.Header().Set("Upload-Length", strconv.FormatInt(uploadLength, 10))
			w.WriteHeader(http.StatusOK)
		case http.MethodPatch:
			mu.Lock()
			expectedOffset := uploadOffset
			mu.Unlock()

			if got := r.Header.Get("Upload-Offset"); got != strconv.FormatInt(expectedOffset, 10) {
				t.Fatalf("unexpected patch offset: got %s want %d", got, expectedOffset)
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read patch body: %v", err)
			}

			mu.Lock()
			patchCalls++
			call := patchCalls
			uploadOffset += int64(len(body))
			offset := uploadOffset
			mu.Unlock()

			if call == 1 {
				// Delay first response long enough for client context timeout,
				// leaving remote offset advanced and resumable from next invocation.
				time.Sleep(300 * time.Millisecond)
			}

			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	resumePath, err := uploadResumeStatePath(server.URL+"/files/", tmpFile, info.Size(), info.ModTime().UnixNano())
	if err != nil {
		t.Fatalf("resume path: %v", err)
	}

	// Use a 1 MiB chunk so the 8 MiB file requires multiple PATCH calls and
	// the test can observe an interrupted upload followed by a resumed one.
	// The default 64 MiB chunk would send the whole file in one PATCH, making
	// it impossible to interrupt mid-way with the 100 ms timeout used below.
	smallChunk := UploaderOptions{ChunkSize: 1 * 1024 * 1024}

	uploader, err := newTusUploader(server.URL+"/files/", "", smallChunk)
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = uploader.Upload(ctx, tmpFile, map[string]string{"filename": "payload.bin"})
	if err == nil {
		t.Fatal("expected interrupted upload error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected context timeout error, got %v", err)
	}
	if _, statErr := os.Stat(resumePath); statErr != nil {
		t.Fatalf("expected resume state file to exist after interruption: %v", statErr)
	}

	secondUploader, err := newTusUploader(server.URL+"/files/", "", smallChunk)
	if err != nil {
		t.Fatalf("new second uploader: %v", err)
	}
	location, err := secondUploader.Upload(context.Background(), tmpFile, map[string]string{"filename": "payload.bin"})
	if err != nil {
		t.Fatalf("resume upload: %v", err)
	}
	if location != "/files/upload-interrupted" {
		t.Fatalf("unexpected upload location: %q", location)
	}
	if _, statErr := os.Stat(resumePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected resume state file to be removed after success, stat err=%v", statErr)
	}

	mu.Lock()
	finalOffset := uploadOffset
	finalPatchCalls := patchCalls
	mu.Unlock()
	if finalOffset != uploadLength {
		t.Fatalf("expected final offset %d, got %d", uploadLength, finalOffset)
	}
	if finalPatchCalls < 2 {
		t.Fatalf("expected at least 2 patch calls across interrupted + resumed upload, got %d", finalPatchCalls)
	}
}

func TestTusUploader_ResumesAfterFileModTimeChange(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	tmpFile := filepath.Join(t.TempDir(), "payload.bin")
	payload := bytes.Repeat([]byte("z"), 4*1024*1024)
	if err := os.WriteFile(tmpFile, payload, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	var createCalls int
	const existingLocation = "/files/upload-modtime"
	resumeOffset := int64(len(payload) / 2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			createCalls++
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Location", "/files/unexpected-create")
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.FormatInt(resumeOffset, 10))
			w.Header().Set("Upload-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read patch body: %v", err)
			}
			if int64(len(body)) != int64(len(payload))-resumeOffset {
				t.Fatalf("unexpected resumed payload length: got %d want %d", len(body), int64(len(payload))-resumeOffset)
			}
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	resumePath, err := uploadResumeStatePath(server.URL+"/files/", tmpFile, info.Size(), info.ModTime().UnixNano())
	if err != nil {
		t.Fatalf("resume path: %v", err)
	}
	if err := storeUploadResumeLocation(resumePath, existingLocation); err != nil {
		t.Fatalf("store resume location: %v", err)
	}

	// Touch file to change mtime without changing content.
	now := time.Now().Add(1 * time.Second)
	if err := os.Chtimes(tmpFile, now, now); err != nil {
		t.Fatalf("touch file: %v", err)
	}

	uploader, err := newTusUploader(server.URL+"/files/", "", UploaderOptions{})
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}
	location, err := uploader.Upload(context.Background(), tmpFile, map[string]string{"filename": "payload.bin"})
	if err != nil {
		t.Fatalf("resume upload: %v", err)
	}
	if location != existingLocation {
		t.Fatalf("unexpected upload location: got %q want %q", location, existingLocation)
	}
	if createCalls != 0 {
		t.Fatalf("expected no new upload creation, got %d", createCalls)
	}
}

func TestTusUploader_ChunkSizeIsRespected(t *testing.T) {
	const chunkSize = 512
	payload := strings.Repeat("y", chunkSize*4) // 4 chunks worth of data
	tmpFile := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(tmpFile, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var patchSizes []int
	uploadOffset := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Location", "/files/upload-chunked")
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			mu.Lock()
			offset := uploadOffset
			mu.Unlock()
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.Itoa(offset))
			w.Header().Set("Upload-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read patch body: %v", err)
			}
			mu.Lock()
			patchSizes = append(patchSizes, len(body))
			uploadOffset += len(body)
			offset := uploadOffset
			mu.Unlock()
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.Itoa(offset))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	uploader, err := newTusUploader(server.URL+"/files/", "", UploaderOptions{ChunkSize: chunkSize})
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}

	_, err = uploader.Upload(context.Background(), tmpFile, map[string]string{"filename": "payload.txt"})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	mu.Lock()
	sizes := make([]int, len(patchSizes))
	copy(sizes, patchSizes)
	mu.Unlock()

	for i, sz := range sizes {
		if sz > chunkSize {
			t.Fatalf("patch %d: size %d exceeds chunk size %d", i, sz, chunkSize)
		}
	}
	if len(sizes) < 4 {
		t.Fatalf("expected at least 4 patch calls for 4-chunk payload, got %d", len(sizes))
	}
}

func TestTusUploader_DefaultChunkSizeIs64MB(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	payload := strings.Repeat("z", 32) // small payload; we just care about the chunk-size header
	tmpFile := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(tmpFile, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}

	var gotChunkSize int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Location", "/files/upload-default-chunk")
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", "0")
			w.Header().Set("Upload-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
		case http.MethodPatch:
			// Upload-Length on PATCH body may be capped by file size, but the
			// Upload-Offset header lets us derive the chunk boundary.
			body, _ := io.ReadAll(r.Body)
			gotChunkSize = int64(len(body))
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	// Construct uploader with no explicit ChunkSize — default should apply.
	uploader, err := newTusUploader(server.URL+"/files/", "", UploaderOptions{})
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}
	tus := uploader.(*tusUploader)

	stream := tusgo.NewUploadStream(tus.client.WithContext(context.Background()), &tusgo.Upload{})
	// makeStream is a closure so we can't call it directly, but we can verify
	// that the defaultUploadChunkSize constant matches our expectation.
	if defaultUploadChunkSize != 64*1024*1024 {
		t.Fatalf("defaultUploadChunkSize = %d, want 64 MiB (%d)", defaultUploadChunkSize, 64*1024*1024)
	}
	_ = stream // used only to confirm tusgo import is present

	_, err = uploader.Upload(context.Background(), tmpFile, map[string]string{"filename": "payload.txt"})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	// The file is tiny (32 bytes) so it fits in one chunk; verify the server
	// saw all bytes (chunk was not artificially limited below file size).
	if gotChunkSize != int64(len(payload)) {
		t.Fatalf("patch body size = %d, want %d", gotChunkSize, len(payload))
	}
}

func TestTusUploader_NoResumeIgnoresStoredState(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	tmpFile := filepath.Join(t.TempDir(), "payload.txt")
	payload := "hello world"
	if err := os.WriteFile(tmpFile, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}

	const staleLocation = "/files/upload-stale"
	var createCalled bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("Tus-Version", "1.0.0")
			w.Header().Set("Tus-Extension", "creation")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			createCalled = true
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Location", "/files/upload-fresh")
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", "0")
			w.Header().Set("Upload-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read patch body: %v", err)
			}
			w.Header().Set("Tus-Resumable", "1.0.0")
			w.Header().Set("Upload-Offset", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	// Pre-store a stale resume URL.
	statePath, err := uploadResumeStatePrimaryPath(server.URL+"/files/", tmpFile)
	if err != nil {
		t.Fatalf("state path: %v", err)
	}
	if err := storeUploadResumeLocation(statePath, staleLocation); err != nil {
		t.Fatalf("store stale location: %v", err)
	}

	uploader, err := newTusUploader(server.URL+"/files/", "", UploaderOptions{NoResume: true})
	if err != nil {
		t.Fatalf("new uploader: %v", err)
	}

	location, err := uploader.Upload(context.Background(), tmpFile, map[string]string{"filename": "payload.txt"})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !createCalled {
		t.Fatal("expected a fresh upload to be created when NoResume=true")
	}
	if location == staleLocation {
		t.Fatal("expected location to differ from stale location")
	}
}

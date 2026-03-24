package data

import (
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

	uploader, err := newTusUploader(server.URL+"/files/", "")
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

	uploader, err := newTusUploader(server.URL+"/files/", "")
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

	uploader, err := newTusUploader(server.URL+"/files/", "")
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

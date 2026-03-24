package data

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

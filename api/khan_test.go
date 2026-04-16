package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetKhanRcloneConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/khan/v1/rclone.conf" {
			t.Fatalf("path %s", r.URL.Path)
		}
		if r.URL.Query().Get("workspaceId") != "ws1" {
			t.Fatalf("query %v", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte("[a]\ntype=s3\n"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "ws1")
	c.httpClient = srv.Client()

	body, err := c.GetKhanRcloneConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "[a]") {
		t.Fatalf("body %q", body)
	}
}

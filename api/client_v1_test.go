package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestClient_GetV1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/emissions" {
			t.Fatalf("path %s", r.URL.Path)
		}
		if r.URL.Query().Get("workspaceId") != "ws9" {
			t.Fatalf("query %v", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"kgco2e":1.2}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", "ws9")
	c.httpClient = srv.Client()

	q := url.Values{"workspaceId": []string{"ws9"}}
	body, err := c.GetV1("/v1/emissions", q)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"kgco2e":1.2}` {
		t.Fatalf("body %s", body)
	}
}

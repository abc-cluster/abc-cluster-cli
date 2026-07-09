package cluster

import (
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

// B5: MinIO registers both an S3 API port and a console port; the endpoint
// resolver must pick the S3/API port, never the console port.
func TestPreferredServicePort(t *testing.T) {
	// console listed first, but s3 must win.
	if p := preferredServicePort([]utils.NomadDynamicPort{
		{Label: "console", Value: 9001},
		{Label: "s3", Value: 9000},
	}); p == nil || p.Value != 9000 {
		t.Fatalf("want s3 port 9000, got %+v", p)
	}
	// only a console port → still returns it (best available).
	if p := preferredServicePort([]utils.NomadDynamicPort{{Label: "console", Value: 9001}}); p == nil || p.Value != 9001 {
		t.Fatalf("want 9001 fallback, got %+v", p)
	}
	// unlabelled ports → first non-zero.
	if p := preferredServicePort([]utils.NomadDynamicPort{{Value: 8080}, {Value: 9090}}); p == nil || p.Value != 8080 {
		t.Fatalf("want 8080, got %+v", p)
	}
	// no usable port.
	if p := preferredServicePort([]utils.NomadDynamicPort{{Value: 0}}); p != nil {
		t.Fatalf("want nil, got %+v", p)
	}
}

func TestSameHostPort(t *testing.T) {
	if !sameHostPort("http://10.0.0.1:9000", "10.0.0.1:9000/") {
		t.Error("scheme/trailing-slash differences should still match")
	}
	if sameHostPort("http://10.0.0.1:9000", "http://10.0.0.1:9001") {
		t.Error("different ports must not match")
	}
}

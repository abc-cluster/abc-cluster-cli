package data

import "testing"

func TestParseBucketPath(t *testing.T) {
	cases := []struct {
		name, arg, bucket, rest string
	}{
		{"bare bucket", "su-demo", "su-demo", ""},
		{"bucket with trailing slash", "su-demo/", "su-demo", ""},
		{"bucket and single-segment prefix", "su-demo/user", "su-demo", "user"},
		{"bucket and multi-segment prefix", "su-demo/user/slate-sunbird/", "su-demo", "user/slate-sunbird/"},
		// s3:// scheme — pasted from `abc pipeline run` output. The naive
		// strings.Cut would have split on `s3:` / `/bucket/...`. parse
		// must strip the scheme up front.
		{"s3 scheme bucket only", "s3://su-demo", "su-demo", ""},
		{"s3 scheme bucket trailing slash", "s3://su-demo/", "su-demo", ""},
		{"s3 scheme bucket and prefix", "s3://su-demo/user/slate-sunbird/", "su-demo", "user/slate-sunbird/"},
		{"empty input", "", "", ""},
		{"slash only", "/", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, r := parseBucketPath(tc.arg)
			if b != tc.bucket || r != tc.rest {
				t.Errorf("parseBucketPath(%q) = (%q, %q); want (%q, %q)",
					tc.arg, b, r, tc.bucket, tc.rest)
			}
		})
	}
}

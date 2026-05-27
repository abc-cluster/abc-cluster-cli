package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveClaimEndpoint(t *testing.T) {
	cases := []struct {
		name, endpoint, tier, want string
		wantErr                    bool
	}{
		{"tier seedling default", "", "seedling",
			"https://signup.seedling.abc-cluster.cloud/claim", false},
		{"tier grove", "", "grove",
			"https://signup.grove.abc-cluster.cloud/claim", false},
		{"endpoint with /claim already", "https://x.example.com/claim", "seedling",
			"https://x.example.com/claim", false},
		{"endpoint without /claim → appended", "https://x.example.com", "seedling",
			"https://x.example.com/claim", false},
		{"endpoint with trailing slash", "https://x.example.com/", "seedling",
			"https://x.example.com/claim", false},
		{"endpoint bare host → error (no scheme)", "x.example.com", "seedling", "", true},
		{"both empty → error", "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveClaimEndpoint(tc.endpoint, tc.tier)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestMapServerError(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode int
		wantSub  string
	}{
		{"code invalid", 404, `{"error":"code_invalid_or_used"}`, exitCodeCodeInvalid, "invalid or has already been used"},
		{"pool exhausted", 404, `{"error":"pool_exhausted"}`, exitCodePoolExhausted, "pool"},
		{"consent required", 400, `{"error":"consent_required"}`, exitCodeEligibility, "POPIA"},
		{"email not eligible", 400, `{"error":"email_not_eligible"}`, exitCodeEligibility, "allowlist"},
		{"group invalid", 400, `{"error":"group_invalid"}`, exitCodeEligibility, "allowlisted group"},
		{"unknown 5xx", 502, `{"error":"upstream"}`, exitCodeNetwork, "server error 502"},
		{"unknown 4xx", 418, `{"error":"i'm a teapot"}`, exitCodeGenericErr, "claim failed (418)"},
		{"empty body", 500, ``, exitCodeNetwork, "server error 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mapServerError(tc.status, []byte(tc.body))
			var ce *claimErrorExit
			if !errors.As(err, &ce) {
				t.Fatalf("expected *claimErrorExit, got %T (%v)", err, err)
			}
			if ce.code != tc.wantCode {
				t.Errorf("code=%d want=%d", ce.code, tc.wantCode)
			}
			if !strings.Contains(ce.msg, tc.wantSub) {
				t.Errorf("msg %q missing substring %q", ce.msg, tc.wantSub)
			}
		})
	}
}

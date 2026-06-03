package debuglog

import (
	"net/http"
	"time"
)

// LoggingTransport wraps an http.RoundTripper and logs every request and its
// response (or error) to the debug logger found in the REQUEST's context. This
// is how non-Nomad HTTP paths (portal/auth-svc, claim, workbench token, the
// GitHub release client, …) get the same http.request / http.response trace
// that NomadClient.do produces — with zero per-call-site logging code, as long
// as the request carries the command context (req = req.WithContext(ctx)).
//
// Redaction: AttrsHTTPRequest/Response strip the URL query string (tokens live
// there), and the RedactingHandler scrubs any token-shaped values downstream.
type LoggingTransport struct {
	Base http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *LoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	ctx := req.Context()
	log := FromContext(ctx)

	var bodyBytes int64
	if req.ContentLength > 0 {
		bodyBytes = req.ContentLength
	}
	log.LogAttrs(ctx, L2, "http.request",
		AttrsHTTPRequest(req.Method, req.URL.String(), bodyBytes)...)

	start := time.Now()
	resp, err := base.RoundTrip(req)
	if err != nil {
		log.LogAttrs(ctx, L1, "http.error",
			AttrsError(req.Method+" "+req.URL.String(), err)...)
		return resp, err
	}
	log.LogAttrs(ctx, L2, "http.response",
		AttrsHTTPResponse(req.Method, req.URL.String(), resp.StatusCode, time.Since(start).Milliseconds())...)
	return resp, nil
}

// NewLoggingClient returns a copy of base with a LoggingTransport installed
// (preserving base's existing Transport as the wrapped layer). Never mutates
// the passed client. If base is nil, a fresh client is used.
//
// Usage at a call site:
//
//	client := debuglog.NewLoggingClient(nil)        // or wrap an existing one
//	req, _ := http.NewRequestWithContext(ctx, …)    // ctx must carry the logger
//	resp, err := client.Do(req)                     // logged automatically
func NewLoggingClient(base *http.Client) *http.Client {
	if base == nil {
		return &http.Client{Transport: &LoggingTransport{Base: http.DefaultTransport}}
	}
	c := *base // shallow copy — don't mutate the caller's client
	c.Transport = &LoggingTransport{Base: base.Transport}
	return &c
}

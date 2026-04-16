package config

import (
	"fmt"
	"net/url"
	"strings"
)

// DeriveUploadEndpointFromAPI returns a tus upload root URL by appending a
// single "files" path segment after the API URL's path. Trailing slashes on
// the input are normalized so the result never contains "//" in the path
// (beyond the scheme's "://"). If the path already ends with "files", it is
// returned with a trailing slash only (no duplicate "files" segment).
func DeriveUploadEndpointFromAPI(apiEndpoint string) (string, error) {
	raw := strings.TrimSpace(apiEndpoint)
	if raw == "" {
		return "", fmt.Errorf("empty API endpoint")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse API URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid API URL %q", apiEndpoint)
	}

	trimmed := strings.Trim(u.Path, "/")
	segments := make([]string, 0, 8)
	if trimmed != "" {
		for _, s := range strings.Split(trimmed, "/") {
			if s != "" {
				segments = append(segments, s)
			}
		}
	}
	if len(segments) > 0 && strings.EqualFold(segments[len(segments)-1], "files") {
		u.Path = "/" + strings.Join(segments, "/")
	} else {
		segments = append(segments, "files")
		u.Path = "/" + strings.Join(segments, "/")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u.String(), nil
}

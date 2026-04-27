package floor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3Client is a minimal S3-compatible client (MinIO/RustFS) using AWS Signature V4.
// It implements list-buckets, list-objects-v2, and head-object for the data ls/stat commands.
type S3Client struct {
	endpoint  string // e.g. "http://100.70.185.46:9000"
	accessKey string
	secretKey string
	region    string
	http      *http.Client
}

// NewS3Client creates an S3-compatible client.
func NewS3Client(endpoint, accessKey, secretKey, region string) *S3Client {
	if region == "" {
		region = "us-east-1"
	}
	return &S3Client{
		endpoint:  strings.TrimRight(endpoint, "/"),
		accessKey: accessKey,
		secretKey: secretKey,
		region:    region,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

// S3Bucket describes an S3 bucket.
type S3Bucket struct {
	Name         string
	CreationDate time.Time
}

// S3Object describes an object in a bucket.
type S3Object struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	StorageClass string
}

// ListBuckets returns all buckets for the authenticated user.
func (c *S3Client) ListBuckets(ctx context.Context) ([]S3Bucket, error) {
	resp, err := c.signedGet(ctx, "/", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list buckets %d: %s", resp.StatusCode, trimXMLError(body))
	}

	var out struct {
		Buckets struct {
			Bucket []struct {
				Name         string `xml:"Name"`
				CreationDate string `xml:"CreationDate"`
			} `xml:"Bucket"`
		} `xml:"Buckets"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse list-buckets: %w", err)
	}

	buckets := make([]S3Bucket, 0, len(out.Buckets.Bucket))
	for _, b := range out.Buckets.Bucket {
		t, _ := time.Parse(time.RFC3339, b.CreationDate)
		buckets = append(buckets, S3Bucket{Name: b.Name, CreationDate: t})
	}
	return buckets, nil
}

// ListObjects lists objects in bucket under prefix (empty = all).
// Returns at most maxKeys results (0 = server default, typically 1000).
func (c *S3Client) ListObjects(ctx context.Context, bucket, prefix string, maxKeys int) ([]S3Object, []string, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	q.Set("delimiter", "/")
	if maxKeys > 0 {
		q.Set("max-keys", fmt.Sprintf("%d", maxKeys))
	}

	resp, err := c.signedGet(ctx, "/"+bucket+"/", q)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("list objects %d: %s", resp.StatusCode, trimXMLError(body))
	}

	var out struct {
		Contents []struct {
			Key          string `xml:"Key"`
			Size         int64  `xml:"Size"`
			LastModified string `xml:"LastModified"`
			ETag         string `xml:"ETag"`
			StorageClass string `xml:"StorageClass"`
		} `xml:"Contents"`
		CommonPrefixes []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return nil, nil, fmt.Errorf("parse list-objects: %w", err)
	}

	objects := make([]S3Object, 0, len(out.Contents))
	for _, o := range out.Contents {
		t, _ := time.Parse(time.RFC3339, o.LastModified)
		objects = append(objects, S3Object{
			Key:          o.Key,
			Size:         o.Size,
			LastModified: t,
			ETag:         strings.Trim(o.ETag, `"`),
			StorageClass: o.StorageClass,
		})
	}

	prefixes := make([]string, 0, len(out.CommonPrefixes))
	for _, cp := range out.CommonPrefixes {
		prefixes = append(prefixes, cp.Prefix)
	}
	return objects, prefixes, nil
}

// HeadObject retrieves metadata for a single object without downloading its body.
func (c *S3Client) HeadObject(ctx context.Context, bucket, key string) (*S3Object, map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead,
		c.endpoint+"/"+bucket+"/"+key, nil)
	if err != nil {
		return nil, nil, err
	}
	c.signRequest(req, nil)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("head object: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, fmt.Errorf("object %s/%s not found", bucket, key)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("head object %d", resp.StatusCode)
	}

	t, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	sizeStr := resp.Header.Get("Content-Length")
	var size int64
	fmt.Sscanf(sizeStr, "%d", &size)

	obj := &S3Object{
		Key:          key,
		Size:         size,
		LastModified: t,
		ETag:         strings.Trim(resp.Header.Get("ETag"), `"`),
		StorageClass: resp.Header.Get("X-Amz-Storage-Class"),
	}

	// Collect x-amz-meta-* headers.
	meta := make(map[string]string)
	for k, vs := range resp.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-meta-") {
			meta[strings.TrimPrefix(lk, "x-amz-meta-")] = strings.Join(vs, ",")
		}
	}
	return obj, meta, nil
}

// ── AWS Signature V4 helpers ─────────────────────────────────────────────────

// DeleteObject removes a single object from a bucket.
func (c *S3Client) DeleteObject(ctx context.Context, bucket, key string) error {
	path := "/" + bucket + "/" + key
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoint+path, nil)
	if err != nil {
		return err
	}
	c.signRequest(req, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("delete %s/%s: HTTP %d: %s", bucket, key, resp.StatusCode, trimXMLError(body))
	}
	return nil
}

// DeleteObjectsWithPrefix lists all objects (recursively, no delimiter) under
// prefix in bucket and deletes them one by one.
// Returns the number of objects deleted and any error.
func (c *S3Client) DeleteObjectsWithPrefix(ctx context.Context, bucket, prefix string) (int, error) {
	var (
		deleted           int
		continuationToken string
	)
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		q.Set("max-keys", "1000")
		if continuationToken != "" {
			q.Set("continuation-token", continuationToken)
		}
		resp, err := c.signedGet(ctx, "/"+bucket+"/", q)
		if err != nil {
			return deleted, fmt.Errorf("list objects: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return deleted, fmt.Errorf("list objects: HTTP %d: %s", resp.StatusCode, trimXMLError(body))
		}
		var page struct {
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
			Contents              []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
		}
		if err := xml.Unmarshal(body, &page); err != nil {
			return deleted, fmt.Errorf("parse list-objects: %w", err)
		}
		for _, obj := range page.Contents {
			if err := c.DeleteObject(ctx, bucket, obj.Key); err != nil {
				return deleted, err
			}
			deleted++
		}
		if !page.IsTruncated {
			break
		}
		continuationToken = page.NextContinuationToken
	}
	return deleted, nil
}

func (c *S3Client) signedGet(ctx context.Context, path string, query url.Values) (*http.Response, error) {
	rawURL := c.endpoint + path
	if query != nil {
		rawURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	c.signRequest(req, query)
	return c.http.Do(req)
}

func (c *S3Client) signRequest(req *http.Request, _ url.Values) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", emptyBodyHash)
	if req.Host == "" {
		req.Host = req.URL.Host
	}

	// Build canonical request.
	canonicalHeaders, signedHeaders := buildHeaders(req, amzDate)
	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQueryString := buildCanonicalQuery(req.URL.RawQuery)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		emptyBodyHash,
	}, "\n")

	// Build string to sign.
	credScope := dateStamp + "/" + c.region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + credScope + "\n" + hexHash([]byte(canonicalRequest))

	// Derive signing key.
	signingKey := deriveSigningKey(c.secretKey, dateStamp, c.region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		c.accessKey, credScope, signedHeaders, signature,
	))
}

const emptyBodyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func buildHeaders(req *http.Request, amzDate string) (canonical, signed string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers := map[string]string{
		"host":                 host,
		"x-amz-content-sha256": emptyBodyHash,
		"x-amz-date":          amzDate,
	}

	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var cb, sb strings.Builder
	for i, k := range keys {
		cb.WriteString(k)
		cb.WriteByte(':')
		cb.WriteString(strings.TrimSpace(headers[k]))
		cb.WriteByte('\n')
		if i > 0 {
			sb.WriteByte(';')
		}
		sb.WriteString(k)
	}
	return cb.String(), sb.String()
}

func buildCanonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	vals, _ := url.ParseQuery(raw)
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		sort.Strings(vals[k])
		for _, v := range vals[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func deriveSigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hexHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func trimXMLError(body []byte) string {
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if err := xml.Unmarshal(body, &e); err == nil && e.Message != "" {
		return e.Code + ": " + e.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

package media

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// S3 speaks the S3 HTTP API, which is what AWS S3, Cloudflare R2, MinIO,
// Backblaze B2, Wasabi and Hetzner Object Storage all speak.
//
// Written by hand rather than through a vendor SDK, for the reason the whole
// daemon is written that way: it has to stay a single static binary that runs
// on a 1 vCPU box, and the AWS SDK alone is larger than everything here. What it
// costs is this file. SigV4 is fiddly but it is not deep, and one signature
// covers six vendors.
type S3 struct {
	Endpoint  string // https://s3.eu-central-1.amazonaws.com, https://<id>.r2.cloudflarestorage.com
	Region    string
	Bucket    string
	Prefix    string
	AccessKey string
	SecretKey string
	// PathStyle puts the bucket in the path rather than the hostname. MinIO and
	// most self-hosted servers need it; AWS and R2 do not.
	PathStyle bool
	HTTP      *http.Client
}

func (s *S3) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	// No global default: an upload of a large object is slow by nature, and a
	// short timeout on it is a failure that looks like corruption.
	return &http.Client{Timeout: 30 * time.Minute}
}

func (s *S3) full(key string) string {
	p := strings.Trim(s.Prefix, "/")
	if p == "" {
		return strings.TrimPrefix(key, "/")
	}
	return p + "/" + strings.TrimPrefix(key, "/")
}

// urlFor builds the request URL and the host to sign against.
func (s *S3) urlFor(key string) (string, error) {
	base, err := url.Parse(strings.TrimSuffix(s.Endpoint, "/"))
	if err != nil {
		return "", err
	}
	if s.PathStyle {
		base.Path = "/" + s.Bucket + "/" + escapePath(s.full(key))
	} else {
		base.Host = s.Bucket + "." + base.Host
		base.Path = "/" + escapePath(s.full(key))
	}
	return base.String(), nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	u, err := s.urlFor(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, r)
	if err != nil {
		return err
	}
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// UNSIGNED-PAYLOAD rather than a hash of the body: the body is a stream
	// this process is forwarding, and hashing it first would mean holding a
	// whole video in memory or on disk to sign it. Every S3 implementation
	// accepts it over HTTPS, which is the only way this ever talks.
	if err := s.sign(req, "UNSIGNED-PAYLOAD"); err != nil {
		return err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return s3err(res, "put")
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	u, err := s.urlFor(key)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if err := s.sign(req, emptyHash); err != nil {
		return nil, err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		res.Body.Close()
		return nil, ErrNoObject
	}
	if err := s3err(res, "get"); err != nil {
		res.Body.Close()
		return nil, err
	}
	return res.Body, nil
}

func (s *S3) Stat(ctx context.Context, key string) (int64, string, error) {
	u, err := s.urlFor(key)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return 0, "", err
	}
	if err := s.sign(req, emptyHash); err != nil {
		return 0, "", err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return 0, "", ErrNoObject
	}
	if err := s3err(res, "head"); err != nil {
		return 0, "", err
	}
	n, _ := strconv.ParseInt(res.Header.Get("Content-Length"), 10, 64)
	return n, res.Header.Get("Content-Type"), nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	u, err := s.urlFor(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	if err := s.sign(req, emptyHash); err != nil {
		return err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	return s3err(res, "delete")
}

// PresignPut is the point of using object storage at all: a URL the browser
// uploads to directly, so a 300 MB video never passes through this server.
func (s *S3) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	return s.presign(http.MethodPut, key, ttl)
}

// PresignGet is the same trick for reading a private object.
func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return s.presign(http.MethodGet, key, ttl)
}

// ---- signature ------------------------------------------------------------

const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func hmacSHA(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

func (s *S3) signingKey(date string) []byte {
	k := hmacSHA([]byte("AWS4"+s.SecretKey), date)
	k = hmacSHA(k, s.Region)
	k = hmacSHA(k, "s3")
	return hmacSHA(k, "aws4_request")
}

// sign adds an Authorization header, the ordinary way a request is signed.
func (s *S3) sign(req *http.Request, payloadHash string) error {
	now := time.Now().UTC()
	stamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("Host", req.URL.Host)

	signed, canonHeaders := canonicalHeaders(req)
	canonReq := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonHeaders,
		signed,
		payloadHash,
	}, "\n")
	scope := date + "/" + s.Region + "/s3/aws4_request"
	toSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		stamp,
		scope,
		sha256hex(canonReq),
	}, "\n")
	sig := hex.EncodeToString(hmacSHA(s.signingKey(date), toSign))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.AccessKey, scope, signed, sig))
	return nil
}

// presign puts the signature in the query string instead, which is what makes
// the URL usable by something that cannot set headers — a browser doing a PUT,
// an <img> tag, a curl somebody pastes.
func (s *S3) presign(method, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = 15 * time.Minute
	}
	raw, err := s.urlFor(key)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	stamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	scope := date + "/" + s.Region + "/s3/aws4_request"

	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", s.AccessKey+"/"+scope)
	q.Set("X-Amz-Date", stamp)
	q.Set("X-Amz-Expires", strconv.Itoa(int(ttl.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	u.RawQuery = q.Encode()

	canonReq := strings.Join([]string{
		method,
		u.EscapedPath(),
		u.RawQuery,
		"host:" + u.Host + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	toSign := strings.Join([]string{"AWS4-HMAC-SHA256", stamp, scope, sha256hex(canonReq)}, "\n")
	q.Set("X-Amz-Signature", hex.EncodeToString(hmacSHA(s.signingKey(date), toSign)))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func canonicalHeaders(req *http.Request) (signed, canonical string) {
	names := []string{"host"}
	values := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if lk == "x-amz-date" || lk == "x-amz-content-sha256" || lk == "content-type" {
			names = append(names, lk)
			values[lk] = strings.TrimSpace(strings.Join(v, ","))
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteString(":")
		b.WriteString(values[n])
		b.WriteString("\n")
	}
	return strings.Join(names, ";"), b.String()
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func s3err(res *http.Response, what string) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2<<10))
	msg := strings.TrimSpace(string(body))
	// The XML is noise in a panel; the message inside it is not.
	if i := strings.Index(msg, "<Message>"); i >= 0 {
		if j := strings.Index(msg[i:], "</Message>"); j > 0 {
			msg = msg[i+len("<Message>") : i+j]
		}
	}
	if msg == "" {
		msg = res.Status
	}
	return fmt.Errorf("storage %s failed: %s", what, msg)
}

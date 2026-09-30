package media

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GCS speaks Google Cloud Storage's JSON API with a service account.
//
// Google's buckets also answer the S3 API if you make an HMAC key for them, and
// the S3 driver next door reaches them that way with the endpoint
// storage.googleapis.com — which is worth knowing, because it needs none of
// this file. What this file is for is the credential people actually have: a
// service-account key, downloaded as JSON, which is what a Google project hands
// out and what its documentation assumes.
//
// The work is entirely in getting an access token: a JWT the daemon signs with
// the account's private key, traded at Google's token endpoint for something
// that lasts an hour. After that it is ordinary HTTP. No SDK, for the reason
// everything else here has none — this daemon stays one static binary.
type GCS struct {
	Bucket      string
	Prefix      string
	ClientEmail string
	// PrivateKey is the PEM from the service-account JSON, `private_key`.
	PrivateKey string
	HTTP       *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
	key     *rsa.PrivateKey
}

const (
	gcsAPI    = "https://storage.googleapis.com/storage/v1"
	gcsUpload = "https://storage.googleapis.com/upload/storage/v1"
	gcsToken  = "https://oauth2.googleapis.com/token"
	gcsScope  = "https://www.googleapis.com/auth/devstorage.read_write"
)

func (g *GCS) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 30 * time.Minute}
}

func (g *GCS) full(key string) string {
	p := strings.Trim(g.Prefix, "/")
	if p == "" {
		return strings.TrimPrefix(key, "/")
	}
	return p + "/" + strings.TrimPrefix(key, "/")
}

// object is the JSON API's path form: the whole key is one escaped segment,
// slashes included. Escaping it as a path instead is the mistake that makes
// everything work until the first object in a folder.
func (g *GCS) object(key string) string {
	return gcsAPI + "/b/" + escapeSegment(g.Bucket) + "/o/" + escapeSegment(g.full(key))
}

// signer parses the PEM once. A service-account key is PKCS#8 as Google writes
// it; PKCS#1 is accepted too, because somebody will have converted theirs.
func (g *GCS) signer() (*rsa.PrivateKey, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.key != nil {
		return g.key, nil
	}
	body := strings.ReplaceAll(strings.TrimSpace(g.PrivateKey), "\\n", "\n")
	block, _ := pem.Decode([]byte(body))
	if block == nil {
		return nil, fmt.Errorf("the service account's private_key is not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("the service account's key is not RSA")
		}
		g.key = rk
		return rk, nil
	}
	rk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the service account's private_key could not be read")
	}
	g.key = rk
	return rk, nil
}

func rsaSign(key *rsa.PrivateKey, payload string) (string, error) {
	sum := sha256.Sum256([]byte(payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sig), nil
}

// accessToken returns a bearer token, minting one when the last has nearly run
// out. A minute of margin, because a token that expires between the check and
// the request is a failure nobody can reproduce.
func (g *GCS) accessToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	if g.token != "" && time.Now().Before(g.expires.Add(-time.Minute)) {
		t := g.token
		g.mu.Unlock()
		return t, nil
	}
	g.mu.Unlock()

	key, err := g.signer()
	if err != nil {
		return "", err
	}
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iss":   g.ClientEmail,
		"scope": gcsScope,
		"aud":   gcsToken,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sig, err := rsaSign(key, body)
	if err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", body+"."+sig)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gcsToken, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := g.client().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<10))
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("google refused the service account: %s: %s", res.Status, strings.TrimSpace(string(raw)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("google's token response could not be read")
	}
	g.mu.Lock()
	g.token = out.AccessToken
	g.expires = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	g.mu.Unlock()
	return out.AccessToken, nil
}

func (g *GCS) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	tok, err := g.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return g.client().Do(req)
}

func (g *GCS) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	u := gcsUpload + "/b/" + escapeSegment(g.Bucket) + "/o?uploadType=media&name=" + url.QueryEscape(g.full(key))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, r)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if size >= 0 {
		req.ContentLength = size
	}
	res, err := g.do(ctx, req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return gcsErr("upload", res)
	}
	return nil
}

func (g *GCS) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.object(key)+"?alt=media", nil)
	if err != nil {
		return nil, err
	}
	res, err := g.do(ctx, req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		res.Body.Close()
		return nil, ErrNoObject
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		return nil, gcsErr("read", res)
	}
	return res.Body, nil
}

func (g *GCS) Stat(ctx context.Context, key string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.object(key), nil)
	if err != nil {
		return 0, "", err
	}
	res, err := g.do(ctx, req)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return 0, "", ErrNoObject
	}
	if res.StatusCode >= 300 {
		return 0, "", gcsErr("stat", res)
	}
	var meta struct {
		Size        string `json:"size"`
		ContentType string `json:"contentType"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&meta); err != nil {
		return 0, "", err
	}
	size, _ := strconv.ParseInt(meta.Size, 10, 64)
	return size, meta.ContentType, nil
}

func (g *GCS) Delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, g.object(key), nil)
	if err != nil {
		return err
	}
	res, err := g.do(ctx, req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound || res.StatusCode < 300 {
		return nil
	}
	return gcsErr("delete", res)
}

// PresignGet and PresignPut hand out a V4 signed URL, so a browser can read or
// write one object without this daemon carrying the bytes.
func (g *GCS) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return g.presign(http.MethodGet, key, ttl)
}

func (g *GCS) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	return g.presign(http.MethodPut, key, ttl)
}

// presign is Google's V4 signing: the same shape as S3's, with an RSA signature
// from the service-account key in place of a derived HMAC.
func (g *GCS) presign(method, key string, ttl time.Duration) (string, error) {
	signer, err := g.signer()
	if err != nil {
		return "", err
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = 15 * time.Minute
	}
	now := time.Now().UTC()
	stamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	scope := date + "/auto/storage/goog4_request"

	// Path style, and every segment of the key escaped to V4's rule rather than
	// Go's: `PathEscape` leaves `&`, `=`, `+`, `:`, `@` and `$` bare, and a
	// canonical request that disagrees with the URL by one character is a 403
	// with nothing in it.
	path := "/" + escapeSegment(g.Bucket) + "/" + escapeKeyPath(g.full(key))

	q := url.Values{}
	q.Set("X-Goog-Algorithm", "GOOG4-RSA-SHA256")
	q.Set("X-Goog-Credential", g.ClientEmail+"/"+scope)
	q.Set("X-Goog-Date", stamp)
	q.Set("X-Goog-Expires", strconv.Itoa(int(ttl.Seconds())))
	q.Set("X-Goog-SignedHeaders", "host")

	canonReq := strings.Join([]string{
		method,
		path,
		q.Encode(),
		"host:storage.googleapis.com\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	sum := sha256.Sum256([]byte(canonReq))
	toSign := strings.Join([]string{"GOOG4-RSA-SHA256", stamp, scope, hex.EncodeToString(sum[:])}, "\n")

	raw, err := rsa.SignPKCS1v15(rand.Reader, signer, crypto.SHA256, hashOf(toSign))
	if err != nil {
		return "", err
	}
	q.Set("X-Goog-Signature", hex.EncodeToString(raw))
	return "https://storage.googleapis.com" + path + "?" + q.Encode(), nil
}

func hashOf(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func gcsErr(what string, res *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return fmt.Errorf("google cloud storage %s failed: %s", what, e.Error.Message)
	}
	return fmt.Errorf("google cloud storage %s failed: %s: %s", what, res.Status, strings.TrimSpace(string(body)))
}

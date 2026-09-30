package media

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Azure speaks the Azure Blob Storage REST API.
//
// Written by hand for the same reason as S3 next door: the vendor SDK is larger
// than this daemon, and what it would save is this file. Shared Key is older
// and simpler than SigV4 — an HMAC over a fixed list of headers and a
// canonicalised resource — and the whole of it is `sign` below.
//
// Azure is the one major provider that speaks nothing S3-shaped, which is why
// it needs a driver at all: Google's buckets can be reached with the S3 driver
// and an HMAC key, and every other provider here already is.
type Azure struct {
	// Account is the storage account name, which is also the hostname.
	Account string
	// Key is the account key, base64 as the portal gives it.
	Key       string
	Container string
	Prefix    string
	// Endpoint overrides the host, for Azurite or a sovereign cloud. Empty
	// means the public one.
	Endpoint string
	HTTP     *http.Client
}

// apiVersion is the REST version these requests are written against. Azure
// keeps old versions working, so this is pinned rather than tracking latest:
// the day a new one changes a header's meaning should not be the day uploads
// start failing on somebody's server.
const azureAPIVersion = "2021-08-06"

func (a *Azure) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 30 * time.Minute}
}

func (a *Azure) host() string {
	if a.Endpoint != "" {
		return strings.TrimSuffix(a.Endpoint, "/")
	}
	return "https://" + a.Account + ".blob.core.windows.net"
}

func (a *Azure) full(key string) string {
	p := strings.Trim(a.Prefix, "/")
	if p == "" {
		return strings.TrimPrefix(key, "/")
	}
	return p + "/" + strings.TrimPrefix(key, "/")
}

// resource is the path both the URL and the signature are built from.
//
// Both use the *escaped* path. Shared Key is signed over "the resource's encoded
// URI path", which is also what the Azure SDKs sign, so anything else is a 403
// with an empty body for every key containing a space, a `#`, a `?` or a
// non-ASCII byte — which is to say for most real filenames. This file had it the
// other way round, with a comment confidently explaining the wrong rule, until a
// release check read it against the specification.
//
// A service SAS is the exception and signs the *decoded* resource; that is forty
// lines below, and is not a mistake.
func (a *Azure) resource(key string) (urlPath, canonical string) {
	escaped := "/" + escapeSegment(a.Container) + "/" + escapeKeyPath(a.full(key))
	return escaped, "/" + a.Account + escaped
}

func (a *Azure) request(ctx context.Context, method, key string, body io.Reader, size int64, contentType string, extra map[string]string) (*http.Request, string, error) {
	p, canonical := a.resource(key)
	req, err := http.NewRequestWithContext(ctx, method, a.host()+p, body)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("x-ms-date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("x-ms-version", azureAPIVersion)
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if size >= 0 {
		req.ContentLength = size
	}
	return req, canonical, nil
}

// sign adds the Shared Key authorization header.
//
// The string to sign is a fixed list of thirteen header values in a fixed
// order, most of which are empty on every request here, then the x-ms-* headers
// sorted, then the canonicalised resource. Empty means empty: a missing header
// contributes a blank line, and getting the count of blank lines wrong is the
// classic way to spend an afternoon on a 403 that says nothing.
func (a *Azure) sign(req *http.Request, canonical string) error {
	key, err := base64.StdEncoding.DecodeString(a.Key)
	if err != nil {
		return fmt.Errorf("the account key is not valid base64")
	}
	length := ""
	if req.ContentLength > 0 {
		length = strconv.FormatInt(req.ContentLength, 10)
	}
	var headers []string
	for k := range req.Header {
		if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-ms-") {
			headers = append(headers, lk+":"+strings.TrimSpace(req.Header.Get(k)))
		}
	}
	sort.Strings(headers)

	// Query parameters join the resource, one per line, sorted, lowercased.
	res := canonical
	if q := req.URL.Query(); len(q) > 0 {
		var keys []string
		for k := range q {
			keys = append(keys, strings.ToLower(k))
		}
		sort.Strings(keys)
		for _, k := range keys {
			res += "\n" + k + ":" + strings.Join(q[k], ",")
		}
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(azureStringToSign(req, res, length, strings.Join(headers, "\n"))))
	req.Header.Set("Authorization", "SharedKey "+a.Account+":"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return nil
}

// azureStringToSign is the thirteen-line block Shared Key hashes, in its own
// function because every mistake anybody makes with Shared Key is in here: a
// field in the wrong order, or a blank line that should not be blank — and the
// only feedback Azure gives is 403 with no hint which.
func azureStringToSign(req *http.Request, resource, length, canonicalHeaders string) string {
	parts := []string{
		req.Method,
		req.Header.Get("Content-Encoding"),
		req.Header.Get("Content-Language"),
		length,
		req.Header.Get("Content-MD5"),
		req.Header.Get("Content-Type"),
		"", // Date: x-ms-date is used instead, and then this must be blank
		req.Header.Get("If-Modified-Since"),
		req.Header.Get("If-Match"),
		req.Header.Get("If-None-Match"),
		req.Header.Get("If-Unmodified-Since"),
		req.Header.Get("Range"),
		canonicalHeaders,
		resource,
	}
	return strings.Join(parts, "\n")
}

func (a *Azure) do(req *http.Request, canonical string) (*http.Response, error) {
	if err := a.sign(req, canonical); err != nil {
		return nil, err
	}
	return a.client().Do(req)
}

func (a *Azure) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	req, canonical, err := a.request(ctx, http.MethodPut, key, r, size, contentType, map[string]string{
		"x-ms-blob-type": "BlockBlob",
	})
	if err != nil {
		return err
	}
	res, err := a.do(req, canonical)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return azureErr("upload", res)
	}
	return nil
}

func (a *Azure) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	req, canonical, err := a.request(ctx, http.MethodGet, key, nil, -1, "", nil)
	if err != nil {
		return nil, err
	}
	res, err := a.do(req, canonical)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		res.Body.Close()
		return nil, ErrNoObject
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		return nil, azureErr("read", res)
	}
	return res.Body, nil
}

func (a *Azure) Stat(ctx context.Context, key string) (int64, string, error) {
	req, canonical, err := a.request(ctx, http.MethodHead, key, nil, -1, "", nil)
	if err != nil {
		return 0, "", err
	}
	res, err := a.do(req, canonical)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return 0, "", ErrNoObject
	}
	if res.StatusCode >= 300 {
		return 0, "", azureErr("stat", res)
	}
	size, _ := strconv.ParseInt(res.Header.Get("Content-Length"), 10, 64)
	return size, res.Header.Get("Content-Type"), nil
}

func (a *Azure) Delete(ctx context.Context, key string) error {
	req, canonical, err := a.request(ctx, http.MethodDelete, key, nil, -1, "", nil)
	if err != nil {
		return err
	}
	res, err := a.do(req, canonical)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	// Gone is the state that was asked for.
	if res.StatusCode == http.StatusNotFound || res.StatusCode < 300 {
		return nil
	}
	return azureErr("delete", res)
}

// PresignGet and PresignPut mint a service SAS: a URL carrying its own
// permission and expiry, so a browser can read or write one blob without the
// account key and without this daemon in the middle of the bytes.
func (a *Azure) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return a.sas(key, "r", ttl, "")
}

func (a *Azure) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	return a.sas(key, "cw", ttl, contentType)
}

// sas builds a service shared access signature.
//
// As with Shared Key, the string to sign is a fixed list in a fixed order and
// every field that does not apply is an empty line. The version pinned here
// decides how many lines there are, which is why it is a constant and not a
// parameter.
func (a *Azure) sas(key, permissions string, ttl time.Duration, contentType string) (string, error) {
	secret, err := base64.StdEncoding.DecodeString(a.Key)
	if err != nil {
		return "", fmt.Errorf("the account key is not valid base64")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	const layout = "2006-01-02T15:04:05Z"
	// Started five minutes ago, because the signing clock and the reader's
	// clock are not the same clock.
	start := time.Now().UTC().Add(-5 * time.Minute).Format(layout)
	expiry := time.Now().UTC().Add(ttl).Format(layout)
	resource := "/blob/" + a.Account + "/" + a.Container + "/" + a.full(key)

	parts := []string{
		permissions,
		start,
		expiry,
		resource,
		"",              // signed identifier
		"",              // signed IP
		"https",         // protocol
		azureAPIVersion, // signed version
		"b",             // resource: a blob
		"",              // snapshot time
		"",              // encryption scope
		"",              // rscc: cache-control
		"",              // rscd: content-disposition
		"",              // rsce: content-encoding
		"",              // rscl: content-language
		contentType,     // rsct: content-type
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strings.Join(parts, "\n")))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	q := url.Values{}
	q.Set("sv", azureAPIVersion)
	q.Set("sr", "b")
	q.Set("sp", permissions)
	q.Set("st", start)
	q.Set("se", expiry)
	q.Set("spr", "https")
	if contentType != "" {
		q.Set("rsct", contentType)
	}
	q.Set("sig", sig)
	p, _ := a.resource(key)
	return a.host() + p + "?" + q.Encode(), nil
}

func azureErr(what string, res *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	// Azure puts the useful part in a header; the body is XML nobody wants to
	// read in a panel.
	if code := res.Header.Get("x-ms-error-code"); code != "" {
		return fmt.Errorf("azure %s failed: %s (%s)", what, code, res.Status)
	}
	return fmt.Errorf("azure %s failed: %s: %s", what, res.Status, strings.TrimSpace(string(body)))
}

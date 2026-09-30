package media

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Every driver the bucket form accepts has to be one the service can actually
// build. The two halves are in different files and a driver added to one and
// not the other is a bucket that saves and then fails on its first upload.
func TestEveryDriverThatSavesCanBeBuilt(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	for _, driver := range []string{"local", "s3", "gcs", "azure"} {
		b := &Bucket{Name: "b-" + driver, Driver: driver, AccessKey: "who", Config: map[string]string{
			"endpoint": "https://example.invalid", "bucket": "b", "container": "c",
		}}
		saved, err := s.SaveBucket(ctx, "tester", b, "a-secret")
		if err != nil {
			t.Errorf("the form refuses %q: %v", driver, err)
			continue
		}
		if _, err := s.storage(ctx, saved); err != nil {
			t.Errorf("%q saves and then cannot be built: %v", driver, err)
		}
	}
	// And one that is not a driver is refused rather than saved and broken.
	if _, err := s.SaveBucket(ctx, "tester", &Bucket{Name: "nope", Driver: "dropbox"}, ""); err == nil {
		t.Error("a driver nobody wrote was accepted")
	}
}

// ---- azure ----------------------------------------------------------------

// Shared Key is thirteen lines in a fixed order, and Azure's only answer to
// getting it wrong is 403 with nothing in it. So the lines are pinned.
func TestAzuresStringToSignHasItsFieldsInOrder(t *testing.T) {
	a := &Azure{Account: "acct", Key: base64.StdEncoding.EncodeToString([]byte("secret-key-bytes")), Container: "media"}
	req, canonical, err := a.request(context.Background(), http.MethodPut, "photos/cat.jpg", strings.NewReader("x"), 1, "image/jpeg",
		map[string]string{"x-ms-blob-type": "BlockBlob"})
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-ms-date", "Mon, 01 Jan 2026 00:00:00 GMT")
	sts := azureStringToSign(req, canonical, "1", "x-ms-blob-type:BlockBlob\nx-ms-date:Mon, 01 Jan 2026 00:00:00 GMT\nx-ms-version:"+azureAPIVersion)
	lines := strings.Split(sts, "\n")

	if lines[0] != "PUT" {
		t.Errorf("line 1 is the verb, got %q", lines[0])
	}
	if lines[3] != "1" {
		t.Errorf("line 4 is Content-Length, got %q", lines[3])
	}
	if lines[5] != "image/jpeg" {
		t.Errorf("line 6 is Content-Type, got %q", lines[5])
	}
	// Line 7 is Date, and must be empty because x-ms-date is used instead.
	// Filling both is the single most common Shared Key mistake.
	if lines[6] != "" {
		t.Errorf("line 7 must be blank when x-ms-date is present, got %q", lines[6])
	}
	if !strings.HasSuffix(sts, "/acct/media/photos/cat.jpg") {
		t.Errorf("the canonical resource is not last: %q", sts)
	}
	if !strings.Contains(sts, "x-ms-blob-type:BlockBlob") {
		t.Error("the x-ms headers are not in the signature")
	}
}

// The URL escapes each segment; the signature does not. A key with a space in
// it works only if those two disagree in exactly this way.
func TestAzureEscapesThePathButNotTheSignedResource(t *testing.T) {
	a := &Azure{Account: "acct", Container: "media"}
	path, canonical := a.resource("holiday photos/a b.jpg")
	if !strings.Contains(path, "holiday%20photos") || !strings.Contains(path, "a%20b.jpg") {
		t.Errorf("the request path is not escaped: %s", path)
	}
	if canonical != "/acct/media/holiday photos/a b.jpg" {
		t.Errorf("the signed resource must be unescaped, got %s", canonical)
	}
}

func TestAzureSASCarriesItsOwnPermissionAndExpiry(t *testing.T) {
	a := &Azure{Account: "acct", Key: base64.StdEncoding.EncodeToString([]byte("k")), Container: "media"}
	raw, err := a.PresignGet(context.Background(), "clip.mp4", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for _, k := range []string{"sv", "sr", "sp", "st", "se", "sig", "spr"} {
		if q.Get(k) == "" {
			t.Errorf("a SAS without %s is refused by Azure", k)
		}
	}
	if q.Get("sp") != "r" {
		t.Errorf("a read link carries %q", q.Get("sp"))
	}
	if q.Get("spr") != "https" {
		t.Error("the link does not insist on https")
	}
	// An expiry in the past is a link that never worked; one far in the future
	// is a link that outlives its reason.
	exp, err := time.Parse("2006-01-02T15:04:05Z", q.Get("se"))
	if err != nil {
		t.Fatalf("expiry is not a timestamp: %q", q.Get("se"))
	}
	if d := time.Until(exp); d <= 0 || d > 2*time.Minute {
		t.Errorf("expiry is %v away", d)
	}
	if up, err := a.PresignPut(context.Background(), "clip.mp4", "video/mp4", time.Minute); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(up, "sp=cw") {
		t.Error("an upload link cannot create or write")
	}
}

// ---- gcs ------------------------------------------------------------------

func testServiceAccountKey(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// A service-account key arrives inside JSON, where the newlines are written as
// \n. Whether it has been unescaped on the way in depends on who pasted it, so
// both forms have to parse or the failure is "invalid PEM" over a key that
// looks perfectly fine on screen.
func TestAGoogleKeyParsesEscapedOrNot(t *testing.T) {
	pemKey := testServiceAccountKey(t)
	for name, body := range map[string]string{
		"as pasted":     pemKey,
		"json-escaped":  strings.ReplaceAll(pemKey, "\n", "\\n"),
		"with padding ": "  " + pemKey + "\n",
	} {
		g := &GCS{Bucket: "b", ClientEmail: "svc@example.iam.gserviceaccount.com", PrivateKey: body}
		if _, err := g.signer(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := &GCS{PrivateKey: "not a key"}
	if _, err := bad.signer(); err == nil {
		t.Error("nonsense was accepted as a private key")
	}
}

// The JSON API takes the whole key as one escaped segment. Escaping it as a
// path instead works for every object until the first one in a folder.
func TestGCSEscapesTheWholeKeyAsOneSegment(t *testing.T) {
	g := &GCS{Bucket: "my-bucket", Prefix: "shop"}
	got := g.object("2026/09/holiday photo.jpg")
	if !strings.Contains(got, "shop%2F2026%2F09%2Fholiday+photo.jpg") && !strings.Contains(got, "shop%2F2026%2F09%2Fholiday%20photo.jpg") {
		t.Errorf("the key is not escaped as one segment: %s", got)
	}
	if strings.Contains(got, "/o/shop/2026") {
		t.Errorf("the key was escaped as a path, which the JSON API reads as other objects: %s", got)
	}
}

func TestAGoogleSignedURLCarriesEverythingGoogleChecks(t *testing.T) {
	g := &GCS{Bucket: "my-bucket", Prefix: "shop", ClientEmail: "svc@example.iam.gserviceaccount.com", PrivateKey: testServiceAccountKey(t)}
	raw, err := g.PresignGet(context.Background(), "clip.mp4", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "storage.googleapis.com" {
		t.Errorf("host is %q", u.Host)
	}
	if u.Path != "/my-bucket/shop/clip.mp4" {
		t.Errorf("path is %q", u.Path)
	}
	q := u.Query()
	for _, k := range []string{"X-Goog-Algorithm", "X-Goog-Credential", "X-Goog-Date", "X-Goog-Expires", "X-Goog-SignedHeaders", "X-Goog-Signature"} {
		if q.Get(k) == "" {
			t.Errorf("a signed URL without %s is refused", k)
		}
	}
	if q.Get("X-Goog-Algorithm") != "GOOG4-RSA-SHA256" {
		t.Errorf("algorithm is %q", q.Get("X-Goog-Algorithm"))
	}
	if !strings.HasPrefix(q.Get("X-Goog-Credential"), g.ClientEmail+"/") || !strings.HasSuffix(q.Get("X-Goog-Credential"), "/auto/storage/goog4_request") {
		t.Errorf("credential scope is %q", q.Get("X-Goog-Credential"))
	}
	if q.Get("X-Goog-Expires") != "300" {
		t.Errorf("expiry is %q seconds", q.Get("X-Goog-Expires"))
	}
	// An RSA-2048 signature is 256 bytes, hex.
	if len(q.Get("X-Goog-Signature")) != 512 {
		t.Errorf("signature is %d hex characters", len(q.Get("X-Goog-Signature")))
	}
	// Two links for the same object must differ, or one is cached somewhere
	// and outlives its expiry.
	again, _ := g.PresignPut(context.Background(), "clip.mp4", "video/mp4", 5*time.Minute)
	if again == raw {
		t.Error("a write link and a read link are the same URL")
	}
}

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
	secrets := map[string]string{
		"local": "",
		"s3":    "a-secret",
		"gcs":   testServiceAccountKey(t),
		"azure": base64.StdEncoding.EncodeToString([]byte("an account key")),
	}
	for _, driver := range []string{"local", "s3", "gcs", "azure"} {
		b := &Bucket{Name: "b-" + driver, Driver: driver, AccessKey: "who", Config: map[string]string{
			"endpoint": "https://example.invalid", "bucket": "b", "container": "c",
		}}
		saved, err := s.SaveBucket(ctx, "tester", b, secrets[driver])
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

// A credential the driver could never use is refused when it is typed, not at
// the first upload — which is what SaveBucket's own comment promises and what
// it was not doing: an empty Azure key was accepted and then used to sign with
// a zero-length HMAC, which fails somewhere else entirely and says nothing.
func TestACredentialOfTheWrongShapeIsRefusedAtSave(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	cfg := map[string]string{"bucket": "b", "container": "c"}
	for _, c := range []struct {
		driver, secret, says string
	}{
		{"azure", "not base64 !!!", "base64"},
		{"gcs", "totally-not-a-pem", "PEM"},
	} {
		_, err := s.SaveBucket(ctx, "tester", &Bucket{Name: "bad-" + c.driver, Driver: c.driver, AccessKey: "who", Config: cfg}, c.secret)
		if err == nil {
			t.Errorf("%s accepted a secret it can never use", c.driver)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: the refusal does not say why: %v", c.driver, err)
		}
	}
	// No secret at all, for a bucket that has none yet.
	if _, err := s.SaveBucket(ctx, "tester", &Bucket{Name: "empty", Driver: "azure", AccessKey: "acct", Config: cfg}, ""); err == nil {
		t.Error("an azure bucket saved with no account key")
	}
	// But editing one that already has a secret must not require typing it in
	// again, or every unrelated change asks for the credential.
	saved, err := s.SaveBucket(ctx, "tester", &Bucket{Name: "good", Driver: "azure", AccessKey: "acct", Config: cfg},
		base64.StdEncoding.EncodeToString([]byte("k")))
	if err != nil {
		t.Fatal(err)
	}
	saved.Name = "renamed"
	if _, err := s.SaveBucket(ctx, "tester", saved, ""); err != nil {
		t.Errorf("renaming a bucket demanded its secret again: %v", err)
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

// Shared Key is signed over the encoded path, and the request sends the encoded
// path, and those are the same string. This test was written the other way
// round on the strength of a wrong comment, which is how the bug survived
// review: an assertion can be as confidently wrong as the code it guards.
func TestAzureSignsExactlyThePathItSends(t *testing.T) {
	a := &Azure{Account: "acct", Container: "media"}
	path, canonical := a.resource("holiday photos/a b.jpg")
	if !strings.Contains(path, "holiday%20photos") || !strings.Contains(path, "a%20b.jpg") {
		t.Errorf("the request path is not escaped: %s", path)
	}
	if canonical != "/acct"+path {
		t.Errorf("the signature is over %q but the request asks for %q", canonical, path)
	}
	// A plus in a name is a plus, not a space: whichever escaper writes `+` for
	// a space turns one object into two.
	p2, c2 := a.resource("a+b.jpg")
	if strings.Contains(p2, "a+b") || !strings.Contains(p2, "%2B") {
		t.Errorf("a literal plus was not escaped: %s", p2)
	}
	if c2 != "/acct"+p2 {
		t.Errorf("signature and request disagree: %q vs %q", c2, p2)
	}
}

// Neither of Go's escapers is right for an object key, and both were used here.
func TestAKeyIsEscapedForAPathAndNotForAQuery(t *testing.T) {
	// A space is %20: as `+` it is a literal plus in a path, so the object is
	// written under one name and read back under another.
	if got := escapeSegment("My Holiday.mp4"); got != "My%20Holiday.mp4" {
		t.Errorf("space: %s", got)
	}
	// A slash inside one segment is escaped; escapeKeyPath keeps the ones
	// between segments.
	if got := escapeSegment("a/b"); got != "a%2Fb" {
		t.Errorf("slash: %s", got)
	}
	if got := escapeKeyPath("shop/a b/c.jpg"); got != "shop/a%20b/c.jpg" {
		t.Errorf("key path: %s", got)
	}
	// Everything a signed canonical request insists on, which PathEscape leaves
	// bare and a 403 never explains.
	for _, c := range []string{"&", "=", "+", ":", "@", "$", "?", "#", "[", "]", "!", "'", "(", ")", "*", ",", ";", `"`} {
		if got := escapeSegment(c); got == c {
			t.Errorf("%q is left bare", c)
		}
	}
	// And the unreserved set is left alone, or every URL grows by a third.
	if got := escapeSegment("aZ0-._~"); got != "aZ0-._~" {
		t.Errorf("unreserved characters were escaped: %s", got)
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

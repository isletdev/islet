package media

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
)

// A local bucket is a directory, and an object in it is a file where the key
// says it is. Everything above this depends on that being boringly true.
func TestLocalStorageRoundTrip(t *testing.T) {
	l := &Local{Root: t.TempDir()}
	ctx := context.Background()
	if err := l.Put(ctx, "a/b/logo.png", strings.NewReader("bytes"), 5, "image/png"); err != nil {
		t.Fatal(err)
	}
	rc, err := l.Get(ctx, "a/b/logo.png")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "bytes" {
		t.Errorf("read back %q", got)
	}
	size, _, err := l.Stat(ctx, "a/b/logo.png")
	if err != nil || size != 5 {
		t.Errorf("stat = %d %v", size, err)
	}
	if err := l.Delete(ctx, "a/b/logo.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Get(ctx, "a/b/logo.png"); err != ErrNoObject {
		t.Errorf("after delete: %v", err)
	}
	// Deleting what is not there is the state that was asked for.
	if err := l.Delete(ctx, "a/b/logo.png"); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
}

// An object key reaches this from a URL, so it must not be able to name a file
// outside the bucket.
func TestALocalKeyCannotClimbOut(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &Local{Root: filepath.Join(root, "bucket")}
	ctx := context.Background()
	for _, key := range []string{"../outside.txt", "a/../../outside.txt", "/../outside.txt"} {
		if rc, err := l.Get(ctx, key); err == nil {
			rc.Close()
			t.Errorf("%q read a file outside the bucket", key)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "secret" {
		t.Error("a key reached outside and changed something")
	}
}

// The name of a variant carries everything that decides its bytes. That is what
// lets there be no table of derivatives: change a preset and the old file is
// simply no longer addressed, rather than served as if it were the new one.
func TestAVariantIsNamedAfterEverythingThatDecidesIt(t *testing.T) {
	base := "ns/ab/abcdef/logo.png"
	p := Preset{Name: "thumb", Width: 320, Height: 320, Fit: "cover", Quality: 78, Format: "auto"}
	first := derivativeKey(base, p)
	if !strings.HasPrefix(first, "ns/ab/abcdef/") {
		t.Errorf("a variant left its object's directory: %s", first)
	}
	if !strings.HasSuffix(first, ".webp") {
		t.Errorf("auto should be webp: %s", first)
	}
	p.Quality = 90
	if derivativeKey(base, p) == first {
		t.Error("changing the quality addressed the same file")
	}
	p.Quality = 78
	p.Fit = "contain"
	if derivativeKey(base, p) == first {
		t.Error("changing the fit addressed the same file")
	}
	p.Fit = "cover"
	p.Format = "jpeg"
	if k := derivativeKey(base, p); k == first || !strings.HasSuffix(k, ".jpg") {
		t.Errorf("changing the format: %s", k)
	}
}

// Two uploads of logo.png are two objects, and the name survives so a download
// arrives called what it was.
func TestTwoUploadsOfOneNameAreTwoObjects(t *testing.T) {
	a := objectKeyFor("shop", "aabbccdd11223344", "logo.png")
	b := objectKeyFor("shop", "99887766554433aa", "logo.png")
	if a == b {
		t.Fatal("the same key twice")
	}
	for _, k := range []string{a, b} {
		if !strings.HasPrefix(k, "shop/") {
			t.Errorf("%s is outside its namespace", k)
		}
		if !strings.HasSuffix(k, "/logo.png") {
			t.Errorf("%s lost the file name", k)
		}
	}
	// No namespace is not a namespace called "".
	if k := objectKeyFor("", "aabbccdd11223344", "logo.png"); strings.HasPrefix(k, "/") {
		t.Errorf("an empty namespace produced %q", k)
	}
}

// A key may do what it says and nothing else, and a key with no origins is a
// server-side key that no browser may use.
func TestWhatAKeyMayDo(t *testing.T) {
	k := &Key{Scopes: "upload,read"}
	for _, yes := range []string{"upload", "read"} {
		if !k.Can(yes) {
			t.Errorf("should be able to %s", yes)
		}
	}
	for _, no := range []string{"delete", "sign", "", "admin"} {
		if k.Can(no) {
			t.Errorf("should not be able to %q", no)
		}
	}
	if !k.AllowsOrigin("") {
		t.Error("a request with no Origin is not a browser and should pass")
	}
	if k.AllowsOrigin("https://evil.example") {
		t.Error("a key with no origins let a browser in")
	}
	k.Origins = "https://shop.example, https://www.shop.example"
	if !k.AllowsOrigin("https://shop.example") || !k.AllowsOrigin("https://www.shop.example") {
		t.Error("a listed origin was refused")
	}
	if k.AllowsOrigin("https://shop.example.evil") {
		t.Error("a prefix match let the wrong origin in")
	}
}

// A bucket's allowlist is what stops somebody's photograph endpoint becoming a
// file host.
func TestTheTypeAllowlist(t *testing.T) {
	if !allowed("", "application/x-dosexec") {
		t.Error("an empty list is the service's own default, not a refusal")
	}
	if !allowed("image/*", "image/png") || !allowed("image/*,application/pdf", "application/pdf") {
		t.Error("an allowed type was refused")
	}
	if allowed("image/*", "text/html") || allowed("image/png", "image/jpeg") {
		t.Error("a type outside the list was allowed")
	}
}

// A signed link works until it does not, and a forged one never does.
func TestSignedLinks(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	q, err := s.Sign(ctx, "obj1", "thumb", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	exp, sig := splitQuery(q)
	if !s.Verify(ctx, "obj1", "thumb", exp, sig) {
		t.Fatal("a link this server just signed did not verify")
	}
	// Each of these is somebody trying it on.
	if s.Verify(ctx, "obj2", "thumb", exp, sig) {
		t.Error("a signature for one object worked for another")
	}
	if s.Verify(ctx, "obj1", "hero", exp, sig) {
		t.Error("a signature for one preset worked for another")
	}
	if s.Verify(ctx, "obj1", "thumb", exp, sig+"a") {
		t.Error("a tampered signature verified")
	}
	// An expiry is part of what is signed, so pushing it out invalidates it
	// rather than extending it.
	if s.Verify(ctx, "obj1", "thumb", "9999999999", sig) {
		t.Error("moving the expiry extended the link")
	}
	// A link with a very short life, waited out. The expiry is inside the
	// signature, so this is the only honest way to produce one.
	brief, _ := s.Sign(ctx, "obj1", "", time.Millisecond)
	bexp, bsig := splitQuery(brief)
	time.Sleep(1100 * time.Millisecond)
	if s.Verify(ctx, "obj1", "", bexp, bsig) {
		t.Error("an expired link still worked")
	}
}

// An upload ticket is signed rather than stored, so it cannot be edited into
// another namespace and an upload nobody finishes leaves nothing behind.
func TestAnUploadTicketCannotBeEdited(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	handle, err := s.sealTicket(ctx, "id1", "shop/aa/id1/logo.png", "logo.png", "public")
	if err != nil {
		t.Fatal(err)
	}
	id, key, name, vis, err := s.openTicket(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	if id != "id1" || key != "shop/aa/id1/logo.png" || name != "logo.png" || vis != "public" {
		t.Errorf("came back as %q %q %q %q", id, key, name, vis)
	}
	raw, mac, _ := strings.Cut(handle, ".")
	forged := strings.Replace(raw, hexEncode("shop"), hexEncode("bank"), 1) + "." + mac
	if _, _, _, _, err := s.openTicket(ctx, forged); err == nil {
		t.Error("a ticket edited to point at another namespace was accepted")
	}
	if _, _, _, _, err := s.openTicket(ctx, "not-a-ticket"); err == nil {
		t.Error("nonsense was accepted as a ticket")
	}
}

// The limiter's job is to stop a loop. A burst is fine; a loop is not.
func TestTheRateLimiterStopsALoop(t *testing.T) {
	l := newLimiter()
	allowed := 0
	for i := 0; i < 1000; i++ {
		if l.allow("k", 60) {
			allowed++
		}
	}
	if allowed == 1000 {
		t.Fatal("a thousand requests in a moment were all allowed")
	}
	if allowed < 5 {
		t.Errorf("only %d allowed; a page loading a dozen images would fail", allowed)
	}
	// One key's loop does not spend another key's allowance.
	if !l.allow("other", 60) {
		t.Error("a second key was refused because the first was busy")
	}
}

// ---- helpers --------------------------------------------------------------

func splitQuery(q string) (exp, sig string) {
	for _, part := range strings.Split(q, "&") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "exp":
			exp = v
		case "sig":
			sig = v
		}
	}
	return
}

// testService is the service against a real database in a temporary directory:
// the signing seed, the settings and the tables are all things it reads.
func testService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	keys, err := auth.LoadOrCreateKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, keys, nil, dir, nil)
}

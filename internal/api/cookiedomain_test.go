package api

import "testing"

// Scoping the session cookie to a name nobody owns fails in the worst way it
// could: the setting saves, the browser silently declines to store the cookie,
// and every protected site goes on asking for a login that plainly worked.
func TestAPublicSuffixIsNotADomainAnybodyCanScopeACookieTo(t *testing.T) {
	for _, d := range []string{"co.uk", "com.au", "co.jp", "org.uk", "ac.uk", "ne.jp", "com.br", "gov.uk"} {
		if !publicSuffix(d) {
			t.Errorf("%q was accepted, and a browser will quietly refuse the cookie", d)
		}
	}
	for _, d := range []string{
		"example.com", "islet.dev", "poolse.dev", "example.co.uk", "shop.example.com",
		"example.io", // two labels, but .io is not a country-code registry shape here
		"a.example",
	} {
		if publicSuffix(d) {
			t.Errorf("%q is somebody's domain and was refused", d)
		}
	}
	// The empty setting means "this host only", which is always allowed.
	if publicSuffix("") {
		t.Error("clearing the setting was treated as a public suffix")
	}
}

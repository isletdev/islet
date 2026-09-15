package api

import (
	"testing"
	"time"
)

// Checking for newer images asks a registry per image over the internet. Done
// one after another it took 4.8 seconds for two apps on the development server,
// and it runs whenever somebody opens the page — so the answer is remembered.
func TestTheUpdateCheckIsRemembered(t *testing.T) {
	var u updateCheck
	if _, ok := u.get(); ok {
		t.Fatal("an empty cache answered")
	}
	u.put(map[string][]string{"shop": {"nginx:1.27"}})
	got, ok := u.get()
	if !ok || len(got["shop"]) != 1 {
		t.Fatalf("cache returned %v, %v", got, ok)
	}

	// What comes back is a copy: the handler writes it out as JSON while
	// another request may be replacing it, and handing out the live map would
	// be a data race with a stack trace nobody can reproduce.
	got["shop"] = append(got["shop"], "added by the caller")
	again, _ := u.get()
	if len(again["shop"]) != 1 {
		t.Errorf("the caller's append reached the cache: %v", again["shop"])
	}

	// And it goes stale rather than being believed forever.
	u.at = time.Now().Add(-updateCacheFor - time.Minute)
	if _, ok := u.get(); ok {
		t.Error("a stale answer was served")
	}
}

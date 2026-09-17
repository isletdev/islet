package ai_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/isletdev/islet/internal/ai"
	"github.com/isletdev/islet/internal/store"
)

func open(t *testing.T) *ai.Service {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return ai.New(st)
}

// With one model configured nothing should have to be chosen, anywhere — so
// the first one saved is the default whether or not anybody said so.
func TestFirstProviderIsTheDefault(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p := &ai.Provider{Name: "Anthropic", Kind: ai.KindAnthropic, Model: "claude-opus-5", SealedKey: "sealed"}
	if err := s.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Resolve(ctx, "")
	if err != nil || got == nil || got.ID != p.ID {
		t.Fatalf("resolve = %+v %v", got, err)
	}
	if !got.Default || !got.KeySet {
		t.Errorf("default=%v keySet=%v, want both true", got.Default, got.KeySet)
	}

	// A second one does not steal it.
	second := &ai.Provider{Name: "Work key", Kind: ai.KindAnthropic, SealedKey: "x"}
	if err := s.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Resolve(ctx, ""); got.ID != p.ID {
		t.Error("saving a second provider changed the default")
	}
	// Until it is asked for, and then there is exactly one default.
	if err := s.SetDefault(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(ctx)
	n := 0
	for _, p := range list {
		if p.Default {
			n++
		}
	}
	if n != 1 || list[0].ID != second.ID {
		t.Fatalf("defaults=%d, first=%s want %s", n, list[0].ID, second.ID)
	}
}

// A conversation started against a provider somebody later deleted must keep
// opening. Falling back to the default is the difference between "this chat
// now uses another model" and "this chat cannot be read".
func TestResolveFallsBackRatherThanFailing(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if got, err := s.Resolve(ctx, "anything"); got != nil || err != nil {
		t.Fatalf("an empty server should resolve to nothing: %+v %v", got, err)
	}
	keep := &ai.Provider{Name: "Keep", Kind: ai.KindSubscription}
	gone := &ai.Provider{Name: "Gone", Kind: ai.KindAnthropic, SealedKey: "k"}
	for _, p := range []*ai.Provider{keep, gone} {
		if err := s.Save(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Resolve(ctx, gone.ID)
	if err != nil || got == nil || got.ID != keep.ID {
		t.Fatalf("resolve after deletion = %+v %v", got, err)
	}
	// Deleting the default hands the title on rather than leaving none.
	if !got.Default {
		t.Error("no provider is the default after the default was deleted")
	}
}

// Saving a model must not wipe a credential the panel never displayed.
func TestSaveKeepsTheKeyWhenNoneIsGiven(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p := &ai.Provider{Name: "Anthropic", Kind: ai.KindAnthropic, SealedKey: "sealed-original"}
	if err := s.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Model, p.SealedKey = "claude-opus-5", ""
	if err := s.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.SealedKey != "sealed-original" {
		t.Fatalf("key became %q", got.SealedKey)
	}
	if got.Model != "claude-opus-5" {
		t.Fatalf("model did not save: %q", got.Model)
	}
}

func TestValidate(t *testing.T) {
	bad := []ai.Provider{
		{Name: "", Kind: ai.KindAnthropic},
		{Name: "x", Kind: "gemini"},
		{Name: "x", Kind: ai.KindOpenAI, BaseURL: "api.example.com"},
	}
	for _, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("%+v was accepted", p)
		}
	}
	ok := ai.Provider{Name: " Work ", Kind: ai.KindOpenAI, BaseURL: "https://api.example.com"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.Name != "Work" {
		t.Errorf("name not trimmed: %q", ok.Name)
	}
}

// Two providers may not share a name: the name is how somebody tells them
// apart in a picker, and the error has to say that rather than leak the index.
func TestNamesAreUnique(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.Save(ctx, &ai.Provider{Name: "Claude", Kind: ai.KindSubscription}); err != nil {
		t.Fatal(err)
	}
	err := s.Save(ctx, &ai.Provider{Name: "Claude", Kind: ai.KindAnthropic})
	if err == nil || !contains(err.Error(), "already exists") {
		t.Fatalf("second save said %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

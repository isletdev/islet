package vault

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/store"
)

// fakeKeys is reversible and obvious, so a test failure says whether the value
// was encrypted rather than what it was encrypted to.
type fakeKeys struct{}

func (fakeKeys) Encrypt(b []byte) ([]byte, error) { return append([]byte("ENC:"), b...), nil }
func (fakeKeys) Decrypt(b []byte) ([]byte, error) {
	return []byte(strings.TrimPrefix(string(b), "ENC:")), nil
}

func newService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, fakeKeys{})
}

func TestSetListAndRead(t *testing.T) {
	ctx := context.Background()
	s := newService(t)

	if err := s.Set(ctx, "DATABASE_PASSWORD", "hunter2", "the shop database"); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "DATABASE_PASSWORD" || list[0].Description != "the shop database" {
		t.Fatalf("list %+v", list)
	}
	// The listing is the thing most likely to end up in a log or a screenshot,
	// so it must not be able to carry a value at all.
	if strings.Contains(strings.ToLower(reflectString(list[0])), "hunter2") {
		t.Fatal("a value reached the listing")
	}
	v, err := s.Value(ctx, "DATABASE_PASSWORD")
	if err != nil || v != "hunter2" {
		t.Fatalf("value %q %v", v, err)
	}
}

// Setting the same name again replaces the value rather than making a second
// row, or a rotation would leave the old one readable.
func TestSetReplaces(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	_ = s.Set(ctx, "TOKEN", "old", "")
	if err := s.Set(ctx, "TOKEN", "new", "rotated"); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(ctx)
	if len(list) != 1 {
		t.Fatalf("expected one row after a rotation, got %d", len(list))
	}
	if v, _ := s.Value(ctx, "TOKEN"); v != "new" {
		t.Errorf("value %q", v)
	}
	if list[0].Description != "rotated" {
		t.Errorf("description should update too, got %q", list[0].Description)
	}
}

func TestNamesAreChecked(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	for _, bad := range []string{"", "lower", "1LEADING", "has space", "has-dash", strings.Repeat("A", 65), "WITH$SIGN"} {
		if err := s.Set(ctx, bad, "v", ""); err == nil {
			t.Errorf("%q should have been refused", bad)
		}
	}
	for _, good := range []string{"A", "DATABASE_PASSWORD", "S3_KEY_2"} {
		if err := s.Set(ctx, good, "v", ""); err != nil {
			t.Errorf("%q should be allowed: %v", good, err)
		}
	}
}

func TestExpand(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	_ = s.Set(ctx, "DB_PASS", "hunter2", "")
	_ = s.Set(ctx, "DB_USER", "shop", "")

	got, missing, err := s.Expand(ctx, "postgres://@vault:DB_USER:@vault:DB_PASS@localhost/shop")
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgres://shop:hunter2@localhost/shop" {
		t.Errorf("expanded to %q", got)
	}
	if len(missing) != 0 {
		t.Errorf("nothing should be missing, got %v", missing)
	}
}

// A name that does not exist stays as written. Replacing it with nothing turns
// a wrong password into a working connection to the wrong place; leaving the
// reference in place fails immediately and says what is wrong.
func TestExpandLeavesAnUnknownReferenceAlone(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	_ = s.Set(ctx, "KNOWN", "yes", "")

	got, missing, err := s.Expand(ctx, "a=@vault:KNOWN b=@vault:TYPO")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a=yes b=@vault:TYPO" {
		t.Errorf("got %q", got)
	}
	if !reflect.DeepEqual(missing, []string{"TYPO"}) {
		t.Errorf("missing %v", missing)
	}
}

func TestRefs(t *testing.T) {
	in := "@vault:ONE and @vault:TWO and @vault:ONE again, not @vault:lower"
	if got := Refs(in); !reflect.DeepEqual(got, []string{"ONE", "TWO"}) {
		t.Errorf("Refs = %v", got)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	_ = s.Set(ctx, "GONE", "v", "")
	if err := s.Delete(ctx, "GONE"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Value(ctx, "GONE"); err == nil {
		t.Error("a deleted secret should not be readable")
	}
	if err := s.Delete(ctx, "NEVER_EXISTED"); err == nil {
		t.Error("deleting something that is not there should say so")
	}
}

// Reading a value marks it used, which is what makes an unused secret
// identifiable later — the ones nobody can account for are the ones to rotate.
func TestReadingMarksUse(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	_ = s.Set(ctx, "USED", "v", "")
	if list, _ := s.List(ctx); list[0].LastUsedAt != "" {
		t.Fatal("a new secret has not been used")
	}
	_, _ = s.Value(ctx, "USED")
	list, _ := s.List(ctx)
	if list[0].LastUsedAt == "" {
		t.Error("reading should record that it was used")
	}
}

func reflectString(v any) string { return strings.ToLower(strings.TrimSpace(sprint(v))) }
func sprint(v any) string        { return strings.TrimSpace(strings.Join(fields(v), " ")) }
func fields(v any) []string {
	rv := reflect.ValueOf(v)
	out := make([]string, 0, rv.NumField())
	for i := 0; i < rv.NumField(); i++ {
		out = append(out, rv.Field(i).String())
	}
	return out
}

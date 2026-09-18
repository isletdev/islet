package uploads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTest builds a service with a scanner that does what the test says, so the
// infected and broken-scanner paths can be exercised on a machine with no
// ClamAV — which is every machine this is developed and tested on.
func newTest(t *testing.T, scan func(ctx context.Context, actor, path string) (string, error)) *Service {
	t.Helper()
	s := New(nil, t.TempDir(), nil)
	if scan != nil {
		s.scanFile = scan
	} else {
		s.scanFile = func(context.Context, string, string) (string, error) { return "clamdscan", nil }
	}
	return s
}

func TestAnUploadBecomesAPathOnThisServer(t *testing.T) {
	s := newTest(t, nil)
	f, err := s.Save(context.Background(), "someone", "logo.svg", strings.NewReader("<svg/>"))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(f.Path) {
		t.Errorf("the path handed to the model is not absolute: %s", f.Path)
	}
	// The name survives intact. An agent told to use "logo.svg" can say so.
	if filepath.Base(f.Path) != "logo.svg" || f.Name != "logo.svg" {
		t.Errorf("the name was not kept: %s", f.Path)
	}
	if f.Size != 6 {
		t.Errorf("size is %d", f.Size)
	}
	if f.Type != "image/svg+xml" {
		t.Errorf("type is %q", f.Type)
	}
	if f.Scanner != "clamdscan" {
		t.Errorf("the scan was not reported: %q", f.Scanner)
	}
	b, err := os.ReadFile(f.Path)
	if err != nil || string(b) != "<svg/>" {
		t.Fatalf("the file is not where it says it is: %v %q", err, b)
	}
}

// Two uploads of the same name are two files, because a person naming both
// "logo.png" means two logos and not a replacement.
func TestTheSameNameTwiceIsTwoFiles(t *testing.T) {
	s := newTest(t, nil)
	a, err := s.Save(context.Background(), "x", "logo.png", strings.NewReader("one"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Save(context.Background(), "x", "logo.png", strings.NewReader("two"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Path == b.Path {
		t.Fatalf("the second upload overwrote the first: %s", a.Path)
	}
	list, err := s.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("list is %d files: %v", len(list), err)
	}
}

// A file the scanner objects to is not saved at all: not listed, not served,
// and with no path to give a model.
func TestAnInfectedFileIsNotKept(t *testing.T) {
	s := newTest(t, func(context.Context, string, string) (string, error) {
		return "", &Infected{Signature: "Eicar-Test-Signature"}
	})
	_, err := s.Save(context.Background(), "x", "payload.zip", strings.NewReader("nasty"))
	var inf *Infected
	if !errors.As(err, &inf) {
		t.Fatalf("an infected upload was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "Eicar-Test-Signature") {
		t.Errorf("the refusal does not name what was found: %v", err)
	}
	list, _ := s.List()
	if len(list) != 0 {
		t.Fatalf("it is still listed: %+v", list)
	}
	if entries, _ := os.ReadDir(s.Dir()); len(entries) != 0 {
		t.Errorf("it left a directory behind: %+v", entries)
	}
}

// A scanner that is installed and cannot answer is a refusal too. Calling the
// file clean because the check broke is how a scanner becomes decoration.
func TestAScannerThatCannotAnswerRefuses(t *testing.T) {
	s := newTest(t, func(context.Context, string, string) (string, error) {
		return "", errors.New("clamdscan could not check this file: Can't connect to clamd")
	})
	if _, err := s.Save(context.Background(), "x", "a.txt", strings.NewReader("hello")); err == nil {
		t.Fatal("a file nothing could check was accepted as clean")
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Errorf("it was kept: %+v", list)
	}
}

// And a machine with no scanner at all still takes the file, saying so. The
// alternative is a feature that does not work on the servers Islet is for.
func TestNoScannerIsSaidRatherThanPretended(t *testing.T) {
	s := newTest(t, func(context.Context, string, string) (string, error) { return "", nil })
	f, err := s.Save(context.Background(), "x", "a.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Scanner != "" {
		t.Errorf("it claims to have been scanned by %q", f.Scanner)
	}
}

func TestRemovingAnUploadTakesTheFileWithIt(t *testing.T) {
	s := newTest(t, nil)
	f, err := s.Save(context.Background(), "x", "a.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Path); !os.IsNotExist(err) {
		t.Errorf("the file is still there: %v", err)
	}
	if err := s.Remove(f.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing it twice should say it is gone: %v", err)
	}
}

// An id from the URL is used to build a path, so it has to be exactly what this
// package hands out and nothing else.
func TestAnIdFromTheUrlCannotWalkOut(t *testing.T) {
	s := newTest(t, nil)
	for _, id := range []string{"..", "../..", "/etc", "abc", "", strings.Repeat("a", 12) + "/x", "zzzzzzzzzzzz"} {
		if _, err := s.Get(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want not found", id, err)
		}
		if err := s.Remove(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Remove(%q) = %v, want not found", id, err)
		}
	}
}

// The name is displayed, written to disk, and handed to a model that will put
// it in a shell command. It is reduced to a plain file name before any of that.
func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"logo.svg":                  "logo.svg",
		"../../etc/passwd":          "passwd",
		`C:\Users\me\brand kit.zip`: "brand kit.zip",
		"  spaced.png  ":            "spaced.png",
		"a\x00b.txt":                "ab.txt",
		"line\nbreak.txt":           "linebreak.txt", // control characters go, they are not turned into spaces
		"odd\u00a0space.txt":        "odd space.txt", // but an exotic space is one
		"..":                        "",
		"/":                         "",
		".":                         "",
		"":                          "",
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	long := SafeName(strings.Repeat("x", 300) + ".png")
	if len(long) > 128 || !strings.HasSuffix(long, ".png") {
		t.Errorf("a very long name came back as %d characters: %q", len(long), long)
	}
}

// The signature is pulled out of clamscan's own output, because "rejected" on
// its own is not something anyone can act on.
func TestTheSignatureIsReadFromTheScannersOutput(t *testing.T) {
	out := "/var/lib/islet/uploads/aa/x.zip: Eicar-Test-Signature FOUND\n"
	if got := signature(out); got != "Eicar-Test-Signature" {
		t.Errorf("signature = %q", got)
	}
	if got := signature("/x: OK\n"); got != "" {
		t.Errorf("a clean line produced a signature: %q", got)
	}
}

package scan

import (
	"context"
	"testing"
)

// The signature is read out of the scanner's own output, because "rejected" on
// its own is not something anyone can act on.
func TestTheSignatureIsReadFromTheScannersOutput(t *testing.T) {
	out := "/var/lib/islet/uploads/aa/x.zip: Eicar-Test-Signature FOUND\n"
	if got := signature(out); got != "Eicar-Test-Signature" {
		t.Errorf("signature = %q", got)
	}
	if got := signature("/x: OK\n"); got != "" {
		t.Errorf("a clean line produced a signature: %q", got)
	}
	if got := (&Infected{Signature: "X"}).Error(); got == "" || len(got) < 10 {
		t.Errorf("the refusal says too little: %q", got)
	}
	if got := (&Infected{}).Error(); got == "" {
		t.Error("an unnamed refusal says nothing at all")
	}
}

// A machine with no scanner is not an error: the file is taken, and the caller
// is told that nothing looked at it rather than that it is clean.
func TestNoScannerIsNotAFailure(t *testing.T) {
	s := New(nil, nil)
	s.look = func(context.Context) string { return "" }
	name, err := s.File(context.Background(), "someone", "/tmp/whatever")
	if err != nil {
		t.Fatalf("a machine with no scanner refused a file: %v", err)
	}
	if name != "" {
		t.Errorf("it claims %q looked", name)
	}
	if got := s.Name(context.Background()); got != "" {
		t.Errorf("Name = %q on a machine with nothing installed", got)
	}
}

package media

import (
	"strings"
	"testing"
)

// The words live beside the original under the same prefix as every other
// derivative, so a bucket has one place where derived bytes are.
func TestExtractedTextIsKeptBesideItsDocument(t *testing.T) {
	got := textKey("shop/2026/contract.pdf")
	if !strings.HasPrefix(got, "shop/2026/"+derivativeInfix+"/") {
		t.Errorf("not under the derivatives prefix: %s", got)
	}
	if !strings.HasSuffix(got, ".txt") {
		t.Errorf("not a text file: %s", got)
	}
}

func TestOnlyADocumentHasTextToRead(t *testing.T) {
	if !Textable("application/pdf") || !Textable("Application/PDF ") {
		t.Error("a PDF has text in it")
	}
	for _, no := range []string{"image/jpeg", "video/mp4", "text/plain", ""} {
		if Textable(no) {
			t.Errorf("%q was offered text extraction", no)
		}
	}
}

// Stripping re-saves the file, so it is offered only where vips reads and
// writes the format — and two of the three lose nothing by it.
func TestOnlyImagesVipsCanRewriteAreStripped(t *testing.T) {
	for _, yes := range []string{"image/jpeg", "image/png", "image/webp", "IMAGE/JPEG"} {
		if !strippable(yes) {
			t.Errorf("%q should be strippable", yes)
		}
	}
	for _, no := range []string{"image/svg+xml", "image/gif", "application/pdf", "video/mp4", "image/avif", ""} {
		if strippable(no) {
			t.Errorf("%q would be re-encoded by a path that cannot read it", no)
		}
	}
}

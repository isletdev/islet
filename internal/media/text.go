package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// The words in a PDF, for an application that wants to search them.
//
// A PDF is the one thing here that carries content rather than pixels, and the
// service could already make a picture of its first page while having nothing to
// say about what it was about. This is the other half: pdftotext, which is in
// the worker already for the thumbnails, and the result kept beside the original
// like every other derivative — so the second request is a read.
//
// Deliberately not a search index. An index is a feature with a query language,
// a schema and a rebuild; what an application needs from here is the text, which
// it can put in whatever it already searches with.

// textKey is where the extracted words live: beside the original, under the same
// derivatives prefix as the thumbnails.
func textKey(objectKey string) string {
	return path.Dir(objectKey) + "/" + derivativeInfix + "/text.txt"
}

// ErrNoText is a file there is nothing to read out of.
var ErrNoText = errors.New("there is no text to extract from this kind of file")

// Text returns the words in a document, extracting them the first time it is
// asked and reading them back afterwards.
func (s *Service) Text(ctx context.Context, actor string, o *Object) (io.ReadCloser, int64, error) {
	if o.ContentType != "application/pdf" {
		return nil, 0, ErrNoText
	}
	b, err := s.Bucket(ctx, o.BucketID)
	if err != nil {
		return nil, 0, err
	}
	st, err := s.storage(ctx, b)
	if err != nil {
		return nil, 0, err
	}
	key := textKey(o.Key)
	if size, _, err := st.Stat(ctx, key); err == nil {
		rc, err := st.Get(ctx, key)
		return rc, size, err
	}

	// The same ceiling as the image variants, for the same reason: this is a
	// path an anonymous request can make expensive, and a hundred of them at
	// once is a server that stops.
	gate := s.gateNow()
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-time.After(20 * time.Second):
		return nil, 0, errors.New("the server is busy converting; try again shortly")
	}
	if size, _, err := st.Stat(ctx, key); err == nil {
		rc, err := st.Get(ctx, key)
		return rc, size, err
	}
	if t := s.WorkerStatus(ctx); !t.Running {
		if t.Foreign {
			return nil, 0, errors.New("the converter container on this host belongs to another Islet daemon, so nothing here can reach it")
		}
		return nil, 0, errors.New("the converters are not installed on this server")
	}

	srcFile, srcRel, err := s.tmpFile("src")
	if err != nil {
		return nil, 0, err
	}
	srcPath := filepath.Join(s.workDir(), srcRel)
	defer func() { _ = os.Remove(srcPath) }()
	rc, err := st.Get(ctx, o.Key)
	if err != nil {
		_ = srcFile.Close()
		return nil, 0, err
	}
	_, err = io.Copy(srcFile, rc)
	_ = rc.Close()
	_ = srcFile.Close()
	if err != nil {
		return nil, 0, err
	}

	dstRel := srcRel + ".txt"
	dstPath := filepath.Join(s.workDir(), dstRel)
	defer func() { _ = os.Remove(dstPath) }()
	// -layout keeps columns and tables readable rather than interleaving them,
	// which is the difference between text somebody can search and text nobody
	// can read.
	if err := s.run(ctx, actor, "pdftotext", "-layout", "-enc", "UTF-8", srcRel, dstRel); err != nil {
		return nil, 0, fmt.Errorf("reading the text out of %s failed: %w", o.Filename, err)
	}

	out, err := os.Open(dstPath)
	if err != nil {
		return nil, 0, err
	}
	info, err := out.Stat()
	if err != nil {
		_ = out.Close()
		return nil, 0, err
	}
	// Stored even when it is empty. A scanned PDF with no text layer produces
	// nothing, and an empty file is the honest answer to "what does it say" —
	// and stops every later request running pdftotext again to find that out.
	if err := st.Put(ctx, key, out, info.Size(), "text/plain; charset=utf-8"); err != nil {
		_ = out.Close()
		return nil, 0, err
	}
	_ = out.Close()

	rc2, err := st.Get(ctx, key)
	return rc2, info.Size(), err
}

// Textable reports whether there is anything to extract, so a panel can offer
// the button only where it does something.
func Textable(contentType string) bool {
	return strings.EqualFold(strings.TrimSpace(contentType), "application/pdf")
}

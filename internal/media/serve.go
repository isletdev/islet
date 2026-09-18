package media

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Rendition is an object ready to be written to a response.
type Rendition struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
	// Redirect is set instead of Body when the bytes are better fetched from
	// somewhere else — a public bucket behind a CDN, or a presigned URL for a
	// private object. Serving those through this process would put a 1 vCPU
	// server in front of every image on somebody's site for no reason.
	Redirect string
	// Immutable says the bytes at this URL can never change, which is true of
	// every object here: the id is in the path and a new upload is a new id.
	Immutable bool
}

// Open produces an object, at a preset if one is named.
//
// The variant is made on the first request and kept in the bucket beside the
// original, so the second request is a read. There is no table of derivatives:
// the name of a variant carries everything that decides its bytes, so a missing
// one is made again and a changed preset addresses a different name.
func (s *Service) Open(ctx context.Context, actor string, o *Object, presetName string) (*Rendition, error) {
	b, err := s.Bucket(ctx, o.BucketID)
	if err != nil {
		return nil, err
	}
	st, err := s.storage(ctx, b)
	if err != nil {
		return nil, err
	}

	key, contentType := o.Key, o.ContentType
	if presetName != "" {
		p, err := s.Preset(ctx, presetName)
		if err != nil {
			return nil, err
		}
		key = derivativeKey(o.Key, *p)
		contentType = p.contentType()
		if _, _, err := st.Stat(ctx, key); err != nil {
			if !errors.Is(err, ErrNoObject) {
				return nil, err
			}
			if err := s.makeVariant(ctx, actor, st, o, *p, key); err != nil {
				return nil, err
			}
		}
	}

	// Public bytes with somewhere better to get them: send the caller there.
	if o.Visibility == "public" && b.PublicBase != "" {
		return &Rendition{Redirect: strings.TrimSuffix(b.PublicBase, "/") + "/" + key, Immutable: true}, nil
	}
	if pre, ok := st.(Presigner); ok && b.Driver == "s3" {
		url, err := pre.PresignGet(ctx, key, 10*time.Minute)
		if err == nil {
			return &Rendition{Redirect: url, Immutable: o.Visibility == "public"}, nil
		}
		// A presign that failed is not a reason to fail the request; fall
		// through and stream it ourselves.
	}
	size, _, err := st.Stat(ctx, key)
	if err != nil && !errors.Is(err, ErrNoObject) {
		return nil, err
	}
	rc, err := st.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return &Rendition{Body: rc, ContentType: contentType, Size: size, Immutable: true}, nil
}

// makeVariant converts one object to one preset.
//
// Bounded by a ceiling of concurrent conversions, because this is the one path
// an anonymous request can make expensive: a page with twelve images is twelve
// of these, and a server that runs them all at once is a server that stops.
func (s *Service) makeVariant(ctx context.Context, actor string, st Storage, o *Object, p Preset, dstKey string) error {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(20 * time.Second):
		// Shedding rather than queueing further: a caller that has waited
		// twenty seconds for a thumbnail is better told no than told nothing.
		return errors.New("the server is busy converting; try again shortly")
	}

	// It may have been made while this waited for its turn.
	if _, _, err := st.Stat(ctx, dstKey); err == nil {
		return nil
	}

	if t := s.WorkerStatus(ctx); !t.Running {
		return errors.New("the converters are not installed on this server")
	}

	srcFile, srcRel, err := s.tmpFile("src")
	if err != nil {
		return err
	}
	srcPath := filepath.Join(s.workDir(), srcRel)
	defer os.Remove(srcPath)
	rc, err := st.Get(ctx, o.Key)
	if err != nil {
		srcFile.Close()
		return err
	}
	_, err = io.Copy(srcFile, rc)
	rc.Close()
	srcFile.Close()
	if err != nil {
		return err
	}

	// The extension is part of the name because it is what decides the encoder;
	// without it vips answers "not a known file format", which is a sentence
	// about the output rather than the input and takes a moment to place.
	dstRel := srcRel + "-out" + p.ext()
	dstPath := filepath.Join(s.workDir(), dstRel)
	defer os.Remove(dstPath)
	if err := s.derive(ctx, actor, srcRel, dstRel, o.ContentType, p); err != nil {
		return err
	}

	out, err := os.Open(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()
	info, err := out.Stat()
	if err != nil {
		return err
	}
	return st.Put(ctx, dstKey, out, info.Size(), p.contentType())
}

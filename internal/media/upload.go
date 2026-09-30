package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/scan"
	"github.com/isletdev/islet/internal/uploads"
)

// Upload takes a file from an application and turns it into an object.
//
// The order matters and is the whole of the security of this path: the bytes go
// to a staging file inside the directory the converter can see, they are checked
// for size, type and malware there, and only then are they written to the bucket
// and given a row. A file that fails any of those was never an object — not a
// row to clean up, not a URL that briefly worked.
func (s *Service) Upload(ctx context.Context, actor string, k *Key, b *Bucket, filename, claimedType string, r io.Reader, visibility string) (*Object, error) {
	if !k.Can("upload") {
		return nil, ErrForbidden
	}
	filename = uploads.SafeName(filename)
	if filename == "" {
		filename = "file"
	}
	max := b.MaxBytes
	if max <= 0 {
		max = s.Settings(ctx).MaxBytes
	}
	if k.QuotaBytes > 0 {
		used, err := s.StoredBytes(ctx, k.ID)
		if err != nil {
			return nil, err
		}
		if used >= k.QuotaBytes {
			return nil, ErrQuota
		}
	}

	f, rel, err := s.tmpFile("in")
	if err != nil {
		return nil, err
	}
	staged := filepath.Join(s.workDir(), rel)
	defer os.Remove(staged)

	// Hashed while it is written rather than read back afterwards: the checksum
	// is what lets an application know it uploaded what it meant to, and a
	// second pass over a large file is a second pass nobody needs.
	sum := sha256.New()
	head := make([]byte, 0, 512)
	n, err := io.Copy(io.MultiWriter(f, sum, headWriter{&head}), io.LimitReader(r, max+1))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if n > max {
		return nil, ErrTooBig
	}

	contentType := guessType(filename, claimedType)
	if contentType == "application/octet-stream" {
		contentType = sniffType(head)
	}
	if !allowed(b.AllowTypes, contentType) {
		return nil, fmt.Errorf("%w: %s", ErrType, contentType)
	}

	scanner := ""
	if b.ScanUploads {
		name, err := s.scanner.File(ctx, actor, staged)
		if err != nil {
			return nil, err
		}
		scanner = name
	}

	probe := s.probe(ctx, rel, contentType)

	// Metadata on the way in, for a bucket that asked for it gone. What a site
	// actually serves is a derivative and those have never carried any; this is
	// for the original, which an application may well link to directly and
	// which arrives from a phone with the coordinates it was taken at.
	if b.Config["stripMetadata"] == "1" && strippable(contentType) {
		// An animation is one frame to `vips copy`, so stripping it would store
		// a still and throw the rest away — irreversibly, on the original the
		// operator chose to keep. Found by a release check that uploaded a
		// ten-frame APNG and got back 893 bytes of the first frame.
		if pages := s.pages(ctx, rel); pages > 1 {
			s.log.Info("media: keeping the metadata on an animation, which stripping would flatten", "file", filename, "pages", pages)
		} else if newRel, newSize, newSum, err := s.stripped(ctx, actor, rel, contentType); err != nil {
			s.log.Warn("media: could not strip metadata; storing the file as it arrived", "file", filename, "err", err)
		} else {
			defer func() { _ = os.Remove(filepath.Join(s.workDir(), newRel)) }()
			rel, staged, n = newRel, filepath.Join(s.workDir(), newRel), newSize
			sum = newSum
		}
	}

	id := newID()
	objectKey := objectKeyFor(k.Namespace, id, filename)
	st, err := s.storage(ctx, b)
	if err != nil {
		return nil, err
	}
	src, err := os.Open(staged)
	if err != nil {
		return nil, err
	}
	err = st.Put(ctx, objectKey, src, n, contentType)
	src.Close()
	if err != nil {
		return nil, err
	}

	if visibility != "private" {
		visibility = "public"
	}
	meta, _ := json.Marshal(map[string]string{})
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO media_objects (id, server_id, bucket_id, namespace, object_key, filename, content_type, size_bytes, checksum, width, height, visibility, scanner, metadata, key_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, s.st.ServerID, b.ID, k.Namespace, objectKey, filename, contentType, n,
		hex.EncodeToString(sum.Sum(nil)), probe.Width, probe.Height, visibility, scanner, string(meta), k.ID); err != nil {
		// The bytes are in the bucket and the row is not, which is the one
		// ordering that leaves rubbish. Take them back out.
		_ = st.Delete(context.WithoutCancel(ctx), objectKey)
		return nil, err
	}
	return s.Object(ctx, id)
}

// Adopt records an object that was uploaded straight to the bucket with a
// presigned URL.
//
// The bytes never passed through this server, so what is known about them is
// what the bucket says: it is asked for the size and type rather than told. An
// application claiming a 4 KB upload of a 4 GB file would otherwise have a row
// that disagreed with the object.
func (s *Service) Adopt(ctx context.Context, actor string, k *Key, b *Bucket, ticket string) (*Object, error) {
	if !k.Can("upload") {
		return nil, ErrForbidden
	}
	id, objectKey, filename, visibility, err := s.openTicket(ctx, ticket)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(objectKey, nsPrefix(k.Namespace)) {
		return nil, ErrForbidden
	}
	st, err := s.storage(ctx, b)
	if err != nil {
		return nil, err
	}
	size, ctype, err := st.Stat(ctx, objectKey)
	if errors.Is(err, ErrNoObject) {
		return nil, errors.New("nothing was uploaded to that ticket")
	}
	if err != nil {
		return nil, err
	}
	contentType := guessType(filename, ctype)

	scanner := ""
	if b.ScanUploads {
		// Reading the object back to scan it. It is the only way — the bytes
		// never came through here — and it is why this is off by default on a
		// bucket that takes presigned uploads, said out loud rather than
		// implied.
		name, err := s.scanObject(ctx, actor, st, objectKey)
		if err != nil {
			_ = st.Delete(context.WithoutCancel(ctx), objectKey)
			return nil, err
		}
		scanner = name
	}
	meta, _ := json.Marshal(map[string]string{})
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO media_objects (id, server_id, bucket_id, namespace, object_key, filename, content_type, size_bytes, checksum, width, height, visibility, scanner, metadata, key_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, s.st.ServerID, b.ID, k.Namespace, objectKey, filename, contentType, size, "", 0, 0, visibility, scanner, string(meta), k.ID); err != nil {
		return nil, err
	}
	return s.Object(ctx, id)
}

// scanObject pulls an object down to a staging file and checks it.
func (s *Service) scanObject(ctx context.Context, actor string, st Storage, key string) (string, error) {
	rc, err := st.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	f, rel, err := s.tmpFile("scan")
	if err != nil {
		return "", err
	}
	staged := filepath.Join(s.workDir(), rel)
	defer os.Remove(staged)
	_, err = io.Copy(f, rc)
	f.Close()
	if err != nil {
		return "", err
	}
	return s.scanner.File(ctx, actor, staged)
}

// Ticket is a presigned upload: a URL the browser PUTs to directly, and the
// handle it uses afterwards to tell Islet the upload happened.
type Ticket struct {
	Handle    string `json:"handle"`
	URL       string `json:"url"`
	Method    string `json:"method"`
	ExpiresIn int    `json:"expiresIn"`
	ObjectID  string `json:"objectId"`
}

// Ticket mints one. Only for buckets that can presign — a local disk has no
// second address, so the caller gets told to upload through the service
// instead, which is a different path rather than a failure.
func (s *Service) Ticket(ctx context.Context, k *Key, b *Bucket, filename, contentType, visibility string) (*Ticket, error) {
	if !k.Can("upload") {
		return nil, ErrForbidden
	}
	st, err := s.storage(ctx, b)
	if err != nil {
		return nil, err
	}
	pre, ok := st.(Presigner)
	if !ok {
		return nil, errors.New("this bucket stores on the server itself, so upload to it directly rather than asking for a ticket")
	}
	filename = uploads.SafeName(filename)
	if filename == "" {
		filename = "file"
	}
	id := newID()
	objectKey := objectKeyFor(k.Namespace, id, filename)
	url, err := pre.PresignPut(ctx, objectKey, guessType(filename, contentType), DefaultPresignTTL)
	if err != nil {
		return nil, err
	}
	handle, err := s.sealTicket(ctx, id, objectKey, filename, visibility)
	if err != nil {
		return nil, err
	}
	return &Ticket{Handle: handle, URL: url, Method: "PUT", ExpiresIn: int(DefaultPresignTTL.Seconds()), ObjectID: id}, nil
}

// A ticket is signed rather than stored.
//
// The alternative is a row per upload that was started, which means a table of
// things that mostly never happen and a job to sweep it. Signed, the ticket is
// the record: it cannot be edited to point at another namespace, it expires by
// itself, and an upload nobody finishes leaves nothing anywhere.
func (s *Service) sealTicket(ctx context.Context, id, key, filename, visibility string) (string, error) {
	if visibility != "private" {
		visibility = "public"
	}
	payload := strings.Join([]string{id, key, filename, visibility, fmt.Sprint(time.Now().Add(DefaultPresignTTL).Unix())}, "\x1f")
	seed, err := s.signingSeed(ctx)
	if err != nil {
		return "", err
	}
	mac := hmacOf(seed, payload)
	return hexEncode(payload) + "." + mac, nil
}

func (s *Service) openTicket(ctx context.Context, handle string) (id, key, filename, visibility string, err error) {
	raw, mac, ok := strings.Cut(handle, ".")
	if !ok {
		return "", "", "", "", errors.New("that upload ticket is not one of ours")
	}
	payload, err := hexDecode(raw)
	if err != nil {
		return "", "", "", "", errors.New("that upload ticket is not one of ours")
	}
	seed, err := s.signingSeed(ctx)
	if err != nil {
		return "", "", "", "", err
	}
	if hmacOf(seed, payload) != mac {
		return "", "", "", "", errors.New("that upload ticket is not one of ours")
	}
	parts := strings.Split(payload, "\x1f")
	if len(parts) != 5 {
		return "", "", "", "", errors.New("that upload ticket is not one of ours")
	}
	var exp int64
	fmt.Sscan(parts[4], &exp)
	if time.Now().Unix() > exp {
		return "", "", "", "", errors.New("that upload ticket has expired; ask for another")
	}
	return parts[0], parts[1], parts[2], parts[3], nil
}

// Delete removes an object, its derivatives and its row.
func (s *Service) Delete(ctx context.Context, actor string, o *Object) error {
	b, err := s.Bucket(ctx, o.BucketID)
	if err != nil {
		return err
	}
	st, err := s.storage(ctx, b)
	if err != nil {
		return err
	}
	if err := st.Delete(ctx, o.Key); err != nil {
		return err
	}
	// Derivatives are named from the original, so they can be found without a
	// table — which is the reason there is no table.
	presets, _ := s.Presets(ctx)
	for _, p := range presets {
		_ = st.Delete(ctx, derivativeKey(o.Key, p))
	}
	// Every kind of derivative, not only the resized images. A video's
	// renditions are the largest things this service ever writes — three of them
	// for a 200 MB upload is most of a gigabyte — and leaving them behind when
	// the row goes leaves bytes that no API can reach, that `usage` cannot see,
	// and that nothing will ever delete. Found by a release check that deleted
	// its own test videos and then looked at the disk.
	for _, f := range VideoFormats {
		_ = st.Delete(ctx, f.key(o.Key))
	}
	_ = st.Delete(ctx, textKey(o.Key))
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM media_objects WHERE server_id = ? AND id = ?`, s.st.ServerID, o.ID); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "media.object.delete", o.Filename, o.ID)
	return nil
}

// ---- keys and paths -------------------------------------------------------

func nsPrefix(ns string) string {
	if ns == "" {
		return ""
	}
	return strings.Trim(ns, "/") + "/"
}

// objectKeyFor puts the id in the path so two uploads of logo.png are two
// objects, and keeps the original name on the end so a URL is readable and a
// download arrives called what it was.
func objectKeyFor(ns, id, filename string) string {
	return nsPrefix(ns) + id[:2] + "/" + id + "/" + filename
}

// derivativeKey is where a variant lives: deterministic, beside the original,
// and carrying everything that would change the bytes. Change a preset's
// quality and the old file is simply no longer addressed.
func derivativeKey(objectKey string, p Preset) string {
	dir := path.Dir(objectKey)
	return fmt.Sprintf("%s/%s/%s-%dx%d-%s-%d%s", dir, derivativeInfix, p.Name, p.Width, p.Height, p.Fit, p.Quality, p.ext())
}

type headWriter struct{ into *[]byte }

func (h headWriter) Write(p []byte) (int, error) {
	if len(*h.into) < 512 {
		room := 512 - len(*h.into)
		if room > len(p) {
			room = len(p)
		}
		*h.into = append(*h.into, p[:room]...)
	}
	return len(p), nil
}

func hexEncode(s string) string { return hex.EncodeToString([]byte(s)) }

func hexDecode(s string) (string, error) {
	b, err := hex.DecodeString(s)
	return string(b), err
}

func hmacOf(seed []byte, payload string) string {
	m := sha256.Sum256(append(append([]byte{}, seed...), payload...))
	return hex.EncodeToString(m[:16])
}

// Infected is re-exported so a caller can tell a rejected file from a broken
// one without importing the scanner.
type Infected = scan.Infected

// strippable is the formats where re-saving actually removes the metadata.
//
// WebP is not one of them, which is the whole reason this list is not simply
// "images vips can write": libvips 8.14 — what the worker image has — accepts
// `strip` on webpsave and ignores it, so the EXIF survives and nothing says so.
// A privacy switch that reports success and does nothing is worse than one that
// is absent, so WebP is refused here rather than pretended at, and the panel
// says which formats are covered.
func strippable(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/png":
		return true
	}
	return false
}

// pages is how many frames an image has, for telling an animation from a
// picture. Anything it cannot read counts as one, because the question this
// answers is "is it safe to re-save" and an unreadable header is not a reason
// to refuse the ordinary case.
func (s *Service) pages(ctx context.Context, rel string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s.exec(ctx, "vipsheader", "-f", "n-pages", rel)))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// stripped re-saves an image without its metadata and returns the new staging
// file, its size and its checksum.
//
// A PNG comes back with the same pixels, byte for byte, verified by subtracting
// one from the other. A JPEG is re-encoded, which is why this is a switch
// somebody turns on rather than the default: taking a photograph somebody
// uploaded and quietly re-compressing it is not a thing to do to every server on
// the strength of a privacy argument about a field most of them do not serve.
func (s *Service) stripped(ctx context.Context, actor, srcRel, contentType string) (string, int64, hash.Hash, error) {
	ext := ".jpg"
	opts := "[strip,Q=92,optimize_coding]"
	if strings.EqualFold(contentType, "image/png") {
		ext, opts = ".png", "[strip,compression=6]"
	}
	dstRel := srcRel + "-clean" + ext
	if err := s.run(ctx, actor, "vips", "copy", srcRel, dstRel+opts); err != nil {
		return "", 0, nil, err
	}
	f, err := os.Open(filepath.Join(s.workDir(), dstRel))
	if err != nil {
		return "", 0, nil, err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	n, err := io.Copy(sum, f)
	if err != nil {
		return "", 0, nil, err
	}
	return dstRel, n, sum, nil
}

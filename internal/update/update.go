// Package update checks GitHub releases for a newer isletd, verifies the
// release's signed checksums with the embedded public key, and replaces the
// running binary atomically. The daemon and the CLI both use it.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are published to.
const Repo = "isletdev/islet"

// Channel selects which releases count.
type Channel string

const (
	Stable Channel = "stable"
	Beta   Channel = "beta" // includes pre-releases
)

// Release is one published version.
type Release struct {
	Tag         string            `json:"tag"`
	Version     string            `json:"version"`
	Prerelease  bool              `json:"prerelease"`
	PublishedAt time.Time         `json:"publishedAt"`
	Notes       string            `json:"notes"`
	Assets      map[string]string `json:"-"` // name -> download URL
}

var client = &http.Client{Timeout: 5 * time.Minute}

func get(ctx context.Context, url string, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "isletd-updater")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound && strings.Contains(url, "/releases") {
		resp.Body.Close()
		return nil, errors.New("no releases have been published yet")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

// Latest returns the newest release on the channel.
func Latest(ctx context.Context, ch Channel) (*Release, error) {
	url := "https://api.github.com/repos/" + Repo + "/releases"
	if ch != Beta {
		url += "/latest"
	}
	resp, err := get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	type ghAsset struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}
	type ghRelease struct {
		TagName     string    `json:"tag_name"`
		Prerelease  bool      `json:"prerelease"`
		Draft       bool      `json:"draft"`
		PublishedAt time.Time `json:"published_at"`
		Body        string    `json:"body"`
		Assets      []ghAsset `json:"assets"`
	}
	var picked *ghRelease
	if ch == Beta {
		var list []ghRelease
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			return nil, err
		}
		for i := range list {
			if !list[i].Draft {
				picked = &list[i]
				break
			}
		}
		if picked == nil {
			return nil, errors.New("no releases yet")
		}
	} else {
		var one ghRelease
		if err := json.NewDecoder(resp.Body).Decode(&one); err != nil {
			return nil, err
		}
		picked = &one
	}
	r := &Release{Tag: picked.TagName, Version: strings.TrimPrefix(picked.TagName, "v"), Prerelease: picked.Prerelease,
		PublishedAt: picked.PublishedAt, Notes: picked.Body, Assets: map[string]string{}}
	for _, a := range picked.Assets {
		r.Assets[a.Name] = a.URL
	}
	return r, nil
}

// IsNewer reports whether candidate is a higher semantic version than current.
// A "dev" build is always considered older.
func IsNewer(candidate, current string) bool {
	if current == "" || current == "dev" || current == "unknown" {
		return true
	}
	a, b := parse(candidate), parse(current)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	// Equal core: a release beats a pre-release of the same version.
	return !strings.Contains(candidate, "-") && strings.Contains(current, "-")
}

func parse(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

// Result describes an applied update.
type Result struct {
	From string `json:"from"`
	To   string `json:"to"`
	Path string `json:"path"`
}

// Apply downloads, verifies and installs the release over the running
// executable. It never downgrades and never installs an unsigned build.
func Apply(ctx context.Context, r *Release, current string, log func(string, ...any)) (*Result, error) {
	if !IsNewer(r.Version, current) {
		return nil, fmt.Errorf("%s is not newer than %s", r.Version, current)
	}
	sumsURL, ok := r.Assets["checksums.txt"]
	if !ok {
		return nil, errors.New("release has no checksums.txt")
	}
	sigURL, ok := r.Assets["checksums.txt.sig"]
	if !ok {
		return nil, errors.New("release has no checksums.txt.sig; refusing an unsigned release")
	}
	sums, err := fetchBytes(ctx, sumsURL, 1<<20)
	if err != nil {
		return nil, err
	}
	sig, err := fetchBytes(ctx, sigURL, 4096)
	if err != nil {
		return nil, err
	}
	if err := VerifySignature(sums, sig); err != nil {
		return nil, err
	}
	log("release signature verified", "version", r.Version)

	asset := fmt.Sprintf("isletd_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	want, err := checksumFor(sums, asset)
	if err != nil {
		return nil, err
	}
	url, ok := r.Assets[asset]
	if !ok {
		return nil, fmt.Errorf("release has no asset %s", asset)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".isletd-update-*")
	if err != nil {
		return nil, fmt.Errorf("cannot write next to %s: %w", exe, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	resp, err := get(ctx, url, "")
	if err != nil {
		tmp.Close()
		return nil, err
	}
	defer resp.Body.Close()
	h := sha256.New()
	archive, err := os.CreateTemp("", "isletd-*.tar.gz")
	if err != nil {
		tmp.Close()
		return nil, err
	}
	defer os.Remove(archive.Name())
	if _, err := io.Copy(io.MultiWriter(archive, h), io.LimitReader(resp.Body, 512<<20)); err != nil {
		tmp.Close()
		archive.Close()
		return nil, err
	}
	archive.Close()
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		tmp.Close()
		return nil, fmt.Errorf("checksum mismatch for %s: got %s want %s", asset, got, want)
	}
	log("archive checksum verified", "asset", asset)

	if err := extractBinary(archive.Name(), "isletd", tmp); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return nil, err
	}
	// Prove the new binary runs before it becomes the only one. A build that
	// cannot start would otherwise crash-loop under systemd with the panel gone,
	// leaving SSH as the only way back.
	check := exec.CommandContext(ctx, tmpPath, "-version")
	if out, err := check.CombinedOutput(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("the downloaded binary does not run, keeping the current one: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Keep the previous binary beside the new one. "islet update --rollback"
	// and the recovery notes both rely on it being there.
	prev := exe + ".prev"
	if err := copyFile(exe, prev); err != nil {
		log("could not keep a copy of the current binary", "err", err)
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		return nil, fmt.Errorf("replace %s: %w", exe, err)
	}
	return &Result{From: current, To: r.Version, Path: exe}, nil
}

func fetchBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := get(ctx, url, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// VerifySignature checks an Ed25519 signature (base64) over data using the
// embedded release public key.
func VerifySignature(data, sigB64 []byte) error {
	pub, err := base64.StdEncoding.DecodeString(PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("embedded public key is malformed")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("signature is malformed")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
		return errors.New("release signature does not verify: refusing to install")
	}
	return nil
}

func checksumFor(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

func extractBinary(archivePath, name string, dst io.Writer) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == name {
			_, err := io.Copy(dst, io.LimitReader(tr, 512<<20))
			return err
		}
	}
}

// copyFile duplicates a file, preserving its mode.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".tmp", dst)
}

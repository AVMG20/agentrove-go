// Package agycli keeps a server-managed copy of the newest Antigravity CLI
// (agy) release. The API downloads each release once (checked against the
// release manifest's SHA-512) into STORAGE_PATH/agy-cli, which every box
// mounts read-only; the box's login shells put it ahead of the image's agy,
// so boxes run the newest agy without an image rebuild.
//
// Layout: versions/<version>/agy, plus current -> versions/<version>. agy
// runs one process per turn, so the next turn after an update runs the new
// release; a running turn keeps its binary open.
package agycli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"time"

	"agentrove/internal/config"
	"agentrove/internal/models"
)

// ManifestURL serves, per platform, the newest release's version, tarball
// URL and SHA-512 (what https://antigravity.google/cli/install.sh reads).
const ManifestURL = "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests"

// binaryName is the executable inside the release tarball.
const binaryName = "antigravity"

// maxBinary caps the extracted binary's size.
const maxBinary = 1 << 30

// keep is how many versions stay on disk: the current one and the one
// before it, which turns started before the update may still run.
const keep = 2

var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type Updater struct {
	dir      string // STORAGE_PATH/agy-cli
	enabled  bool
	interval time.Duration
	baseURL  string
	client   *http.Client
	platform string
}

func New(cfg *config.Config) *Updater {
	return &Updater{
		dir:      cfg.AgyCLIDir(),
		enabled:  cfg.AgyAutoUpdate,
		interval: cfg.AgyUpdateInterval,
		baseURL:  ManifestURL,
		client:   &http.Client{Timeout: 15 * time.Minute},
		platform: "linux_" + runtime.GOARCH,
	}
}

// Run checks for a new release every interval while auto-update is on.
// Off, it removes the managed copy so boxes run the image's agy.
func (u *Updater) Run(ctx context.Context) {
	if !u.enabled {
		_ = os.Remove(filepath.Join(u.dir, "current"))
		return
	}
	for {
		if err := u.Check(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("agy cli update", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(u.interval):
		}
	}
}

// installed is the version current points to, if its binary is there.
func (u *Updater) installed() string {
	target, err := os.Readlink(filepath.Join(u.dir, "current"))
	if err != nil {
		return ""
	}
	v := filepath.Base(target)
	if !versionRE.MatchString(v) {
		return ""
	}
	if _, err := os.Stat(filepath.Join(u.dir, "versions", v, "agy")); err != nil {
		return ""
	}
	return v
}

type manifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA512  string `json:"sha512"`
}

// Check installs the latest release if it is newer than the installed one.
func (u *Updater) Check(ctx context.Context) error {
	m, err := u.manifest(ctx)
	if err != nil {
		return err
	}
	cur := u.installed()
	if cur != "" && models.CompareVersions(m.Version, cur) <= 0 {
		return nil
	}
	if err := u.install(ctx, m); err != nil {
		return fmt.Errorf("install %s: %w", m.Version, err)
	}
	slog.Info("agy cli updated", "version", m.Version, "previous", cur)
	u.prune(m.Version)
	return nil
}

func (u *Updater) manifest(ctx context.Context) (manifest, error) {
	var m manifest
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := u.do(ctx, u.baseURL+"/"+u.platform+".json")
	if err != nil {
		return m, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	if !versionRE.MatchString(m.Version) || m.URL == "" || len(m.SHA512) != 128 {
		return m, fmt.Errorf("manifest has no usable %s build", u.platform)
	}
	return m, nil
}

func (u *Updater) do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

func (u *Updater) install(ctx context.Context, m manifest) error {
	versions := filepath.Join(u.dir, "versions")
	if err := os.MkdirAll(versions, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(versions, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tarball := filepath.Join(tmp, "agy.tar.gz")
	if err := u.download(ctx, m.URL, tarball, m.SHA512); err != nil {
		return err
	}
	if err := extract(tarball, filepath.Join(tmp, "agy")); err != nil {
		return err
	}
	_ = os.Remove(tarball)
	dst := filepath.Join(versions, m.Version)
	_ = os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	_ = os.Chmod(dst, 0o755)
	// Repoint current atomically.
	link := filepath.Join(u.dir, ".current-new")
	_ = os.Remove(link)
	if err := os.Symlink(filepath.Join("versions", m.Version), link); err != nil {
		return err
	}
	return os.Rename(link, filepath.Join(u.dir, "current"))
}

func (u *Updater) download(ctx context.Context, url, dst, checksum string) error {
	resp, err := u.do(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	h := sha512.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxBinary))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != checksum {
		return errors.New("checksum mismatch")
	}
	return nil
}

// extract writes the tarball's agy binary to dst.
func extract(tarball, dst string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("tarball has no %s", binaryName)
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || filepath.Clean(h.Name) != binaryName {
			continue
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(tr, maxBinary))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	}
}

// prune keeps the newest versions (current among them) and drops the rest.
func (u *Updater) prune(current string) {
	versions := filepath.Join(u.dir, "versions")
	entries, err := os.ReadDir(versions)
	if err != nil {
		return
	}
	var vs []string
	for _, e := range entries {
		if e.IsDir() && versionRE.MatchString(e.Name()) && e.Name() != current {
			vs = append(vs, e.Name())
		}
	}
	slices.SortFunc(vs, func(a, b string) int { return models.CompareVersions(b, a) })
	for i, v := range vs {
		if i >= keep-1 {
			_ = os.RemoveAll(filepath.Join(versions, v))
		}
	}
}

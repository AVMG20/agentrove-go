// Package claudecli keeps a server-managed copy of the newest Claude Code
// release. The API downloads each release once (checked against the
// release manifest's SHA-256) into STORAGE_PATH/claude-cli, which every box
// mounts read-only, so boxes run the newest CLI without an image rebuild.
//
// Layout: versions/<version>/claude, plus current -> versions/<version> for
// the box terminal's PATH. Sessions launch the versioned path, so an update
// never swaps a running binary and the new version starts a new process.
package claudecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"agentrove/internal/claude"
	"agentrove/internal/config"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

// ReleasesURL serves "latest" (the newest version) and, per version,
// manifest.json and <platform>/claude.
const ReleasesURL = "https://downloads.claude.ai/claude-code-releases"

// keep is how many versions stay on disk: the current one and the one
// before it, which sessions started before the update may still run.
const keep = 2

var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type Updater struct {
	dir      string // STORAGE_PATH/claude-cli
	enabled  bool
	interval time.Duration
	// fallback is the image's CLI, used while no newer managed copy exists.
	fallback string
	baseURL  string
	client   *http.Client
	platform string
	probe    func(ctx context.Context, bin string) (json.RawMessage, error)

	mu              sync.RWMutex
	version         string // managed version in use; "" for the image's CLI
	fallbackVersion string
}

func New(cfg *config.Config) *Updater {
	return &Updater{
		dir:      cfg.ClaudeCLIDir(),
		enabled:  cfg.ClaudeAutoUpdate,
		interval: cfg.ClaudeUpdateInterval,
		fallback: cfg.ClaudeBinary,
		baseURL:  ReleasesURL,
		client:   &http.Client{Timeout: 15 * time.Minute},
		platform: platform(),
		probe:    claude.ProbeModels,
	}
}

func platform() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return "linux-" + arch
}

// Version is the managed release sessions should run, or "" to run the
// image's CLI.
func (u *Updater) Version() string {
	if u == nil {
		return ""
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.version
}

// HostBinary is the CLI the API itself runs (one-shot generations).
func (u *Updater) HostBinary() string {
	if v := u.Version(); v != "" {
		return filepath.Join(u.dir, "versions", v, "claude")
	}
	if u == nil {
		return "claude"
	}
	return u.fallback
}

// BoxBinary is the managed CLI's path inside a box, or "" for the image's.
func (u *Updater) BoxBinary() string {
	if v := u.Version(); v != "" {
		return path.Join(sandbox.ContainerClaudeCLIDir, "versions", v, "claude")
	}
	return ""
}

// Run adopts what is on disk, reports the models of the CLI in use, then
// checks for a new release every interval while auto-update is on.
func (u *Updater) Run(ctx context.Context) {
	u.fallbackVersion = binaryVersion(ctx, u.fallback)
	if u.enabled {
		u.adopt(u.installed())
	}
	u.reportModels(ctx)
	if !u.enabled {
		return
	}
	for {
		if err := u.Check(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("claude cli update", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(u.interval):
		}
	}
}

// adopt makes v the managed version when it is newer than the image's CLI.
func (u *Updater) adopt(v string) {
	if v == "" || (u.fallbackVersion != "" && models.CompareVersions(v, u.fallbackVersion) <= 0) {
		return
	}
	u.mu.Lock()
	u.version = v
	u.mu.Unlock()
}

func (u *Updater) reportModels(ctx context.Context) {
	reply, err := u.probe(ctx, u.HostBinary())
	if err != nil {
		slog.Warn("claude cli: list models", "err", err)
		return
	}
	claude.RecordModels(reply)
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
	if _, err := os.Stat(filepath.Join(u.dir, "versions", v, "claude")); err != nil {
		return ""
	}
	return v
}

// Check installs the latest release if it is newer than the CLI in use.
func (u *Updater) Check(ctx context.Context) error {
	latest, err := u.get(ctx, u.baseURL+"/latest", 64)
	if err != nil {
		return err
	}
	v := strings.TrimSpace(string(latest))
	if !versionRE.MatchString(v) {
		return fmt.Errorf("unexpected latest version %q", firstLine(v))
	}
	cur := u.Version()
	if cur == "" {
		cur = u.fallbackVersion
	}
	if cur != "" && models.CompareVersions(v, cur) <= 0 {
		return nil
	}
	if err := u.install(ctx, v); err != nil {
		return fmt.Errorf("install %s: %w", v, err)
	}
	slog.Info("claude cli updated", "version", v, "previous", cur)
	u.adopt(v)
	u.reportModels(ctx)
	u.prune(v)
	return nil
}

func (u *Updater) install(ctx context.Context, v string) error {
	raw, err := u.get(ctx, u.baseURL+"/"+v+"/manifest.json", 1<<20)
	if err != nil {
		return err
	}
	var manifest struct {
		Platforms map[string]struct {
			Checksum string `json:"checksum"`
			Size     int64  `json:"size"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	p, ok := manifest.Platforms[u.platform]
	if !ok || len(p.Checksum) != 64 || p.Size <= 0 {
		return fmt.Errorf("manifest has no %s build", u.platform)
	}
	versions := filepath.Join(u.dir, "versions")
	if err := os.MkdirAll(versions, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(versions, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "claude")
	if err := u.download(ctx, u.baseURL+"/"+v+"/"+u.platform+"/claude", bin, p.Size, p.Checksum); err != nil {
		return err
	}
	if got := binaryVersion(ctx, bin); got != v {
		return fmt.Errorf("downloaded binary reports version %q", got)
	}
	dst := filepath.Join(versions, v)
	_ = os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	_ = os.Chmod(dst, 0o755)
	// Repoint current atomically.
	link := filepath.Join(u.dir, ".current-new")
	_ = os.Remove(link)
	if err := os.Symlink(filepath.Join("versions", v), link); err != nil {
		return err
	}
	return os.Rename(link, filepath.Join(u.dir, "current"))
}

func (u *Updater) download(ctx context.Context, url, dst string, size int64, checksum string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("size %d, manifest says %d", n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != checksum {
		return errors.New("checksum mismatch")
	}
	return nil
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// prune keeps the newest versions (current among them) and drops the rest.
// A session still running a dropped binary keeps running: the file stays
// open until its process exits.
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

// binaryVersion runs bin --version ("2.1.321 (Claude Code)"); "" if it fails.
func binaryVersion(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.TempDir(), "DISABLE_AUTOUPDATER=1"}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	v, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	if !versionRE.MatchString(v) {
		return ""
	}
	return v
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

package claudecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func fakeCLI(version string) []byte {
	return []byte("#!/bin/sh\necho '" + version + " (Claude Code)'\n")
}

// release serves latest and each version's manifest and binary; corrupt
// serves a binary that doesn't match its manifest checksum.
func release(t *testing.T, latest *string, corrupt bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /latest", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, *latest) })
	mux.HandleFunc("GET /{v}/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		bin := fakeCLI(r.PathValue("v"))
		sum := sha256.Sum256(bin)
		_ = json.NewEncoder(w).Encode(map[string]any{"platforms": map[string]any{
			"linux-x64": map[string]any{"checksum": hex.EncodeToString(sum[:]), "size": len(bin)},
		}})
	})
	mux.HandleFunc("GET /{v}/linux-x64/claude", func(w http.ResponseWriter, r *http.Request) {
		bin := fakeCLI(r.PathValue("v"))
		if corrupt {
			bin[len(bin)-2] = 'X'
		}
		_, _ = w.Write(bin)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestUpdater(t *testing.T, srv *httptest.Server, imageVersion string) (*Updater, *int) {
	t.Helper()
	dir := t.TempDir()
	image := filepath.Join(dir, "image-claude")
	if err := os.WriteFile(image, fakeCLI(imageVersion), 0o755); err != nil {
		t.Fatal(err)
	}
	probes := 0
	u := &Updater{dir: filepath.Join(dir, "claude-cli"), enabled: true, fallback: image, baseURL: srv.URL,
		client: srv.Client(), platform: "linux-x64",
		probe: func(context.Context, string) (json.RawMessage, error) { probes++; return json.RawMessage(`{}`), nil }}
	u.fallbackVersion = binaryVersion(context.Background(), image)
	return u, &probes
}

// A newer release is installed, becomes the sessions' binary, and is asked
// for its models; older versions beyond the previous one are pruned.
func TestCheckInstallsNewerRelease(t *testing.T) {
	latest := "2.1.400"
	srv := release(t, &latest, false)
	u, probes := newTestUpdater(t, srv, "2.1.321")
	ctx := context.Background()
	for _, v := range []string{"2.1.400", "2.1.401", "2.1.402"} {
		latest = v
		if err := u.Check(ctx); err != nil {
			t.Fatal(err)
		}
		if u.Version() != v || u.BoxBinary() != "/opt/agentrove/claude-cli/versions/"+v+"/claude" {
			t.Fatalf("version %q, box binary %q", u.Version(), u.BoxBinary())
		}
	}
	if *probes != 3 {
		t.Fatalf("probes = %d", *probes)
	}
	if got := binaryVersion(ctx, u.HostBinary()); got != "2.1.402" {
		t.Fatalf("host binary runs %q", got)
	}
	if u.installed() != "2.1.402" {
		t.Fatalf("current -> %q", u.installed())
	}
	entries, _ := os.ReadDir(filepath.Join(u.dir, "versions"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[0] != "2.1.401" || names[1] != "2.1.402" {
		t.Fatalf("versions on disk = %v", names)
	}
	// Nothing newer: no download, no probe.
	if err := u.Check(ctx); err != nil || *probes != 3 {
		t.Fatalf("err %v, probes %d", err, *probes)
	}
}

// The image's CLI keeps running when it is as new as the latest release.
func TestCheckSkipsReleaseNoNewerThanImage(t *testing.T) {
	latest := "2.1.321"
	u, _ := newTestUpdater(t, release(t, &latest, false), "2.1.321")
	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if u.Version() != "" || u.BoxBinary() != "" || u.HostBinary() != u.fallback {
		t.Fatalf("managed %q", u.Version())
	}
}

// A download that fails its checksum is never installed.
func TestCheckRejectsChecksumMismatch(t *testing.T) {
	latest := "2.1.400"
	u, _ := newTestUpdater(t, release(t, &latest, true), "2.1.321")
	if err := u.Check(context.Background()); err == nil {
		t.Fatal("corrupt release installed")
	}
	if u.Version() != "" || u.installed() != "" {
		t.Fatalf("managed %q", u.Version())
	}
}

// A managed copy older than a rebuilt image is ignored on start.
func TestAdoptIgnoresOlderThanImage(t *testing.T) {
	latest := "2.1.300"
	u, _ := newTestUpdater(t, release(t, &latest, false), "2.1.200")
	if err := u.Check(context.Background()); err != nil || u.Version() != "2.1.300" {
		t.Fatalf("err %v, version %q", err, u.Version())
	}
	newer, _ := newTestUpdater(t, release(t, &latest, false), "2.1.321")
	newer.dir = u.dir
	newer.adopt(newer.installed())
	if newer.Version() != "" {
		t.Fatalf("adopted %q over a newer image", newer.Version())
	}
}

// A nil updater (tests, no managed CLI) runs the image's claude.
func TestNilUpdater(t *testing.T) {
	var u *Updater
	if u.Version() != "" || u.BoxBinary() != "" || u.HostBinary() != "claude" {
		t.Fatal("nil updater")
	}
}

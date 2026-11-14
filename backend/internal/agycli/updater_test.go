package agycli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func tarball(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range [][2]string{{"README", "docs"}, {binaryName, "#!/bin/sh\necho " + version + "\n"}} {
		name, body := f[0], f[1]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// release serves the manifest for *latest and its tarball; corrupt makes the
// tarball not match the manifest's checksum.
func release(t *testing.T, latest *string, corrupt *bool) *Updater {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("GET /linux_amd64.json", func(w http.ResponseWriter, _ *http.Request) {
		sum := sha512.Sum512(tarball(t, *latest))
		_ = json.NewEncoder(w).Encode(manifest{Version: *latest, URL: srv.URL + "/" + *latest + ".tar.gz", SHA512: hex.EncodeToString(sum[:])})
	})
	mux.HandleFunc("GET /{file}", func(w http.ResponseWriter, _ *http.Request) {
		b := tarball(t, *latest)
		if *corrupt {
			b = append(b, 0)
		}
		_, _ = w.Write(b)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Updater{dir: filepath.Join(t.TempDir(), "agy-cli"), enabled: true, baseURL: srv.URL, client: srv.Client(), platform: "linux_amd64"}
}

func TestCheckInstallsAndPrunes(t *testing.T) {
	latest, corrupt := "1.5.6", false
	u := release(t, &latest, &corrupt)
	ctx := context.Background()
	for _, v := range []string{"1.5.6", "1.5.8", "1.5.9"} {
		latest = v
		if err := u.Check(ctx); err != nil {
			t.Fatal(err)
		}
		if got := u.installed(); got != v {
			t.Fatalf("installed = %q, want %q", got, v)
		}
		b, err := os.ReadFile(filepath.Join(u.dir, "current", "agy"))
		if err != nil || !bytes.Contains(b, []byte("echo "+v)) {
			t.Fatalf("current/agy = %q, %v", b, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(u.dir, "versions"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[0] != "1.5.8" || names[1] != "1.5.9" {
		t.Fatalf("versions = %v, want the newest two", names)
	}

	// The same or an older release changes nothing.
	latest = "1.5.7"
	if err := u.Check(ctx); err != nil || u.installed() != "1.5.9" {
		t.Fatalf("older release: installed %q, err %v", u.installed(), err)
	}
}

func TestCheckRejectsChecksumMismatch(t *testing.T) {
	latest, corrupt := "1.5.9", true
	u := release(t, &latest, &corrupt)
	if err := u.Check(context.Background()); err == nil {
		t.Fatal("corrupt tarball installed")
	}
	if u.installed() != "" {
		t.Fatalf("installed = %q after a failed download", u.installed())
	}
	entries, _ := os.ReadDir(filepath.Join(u.dir, "versions"))
	if len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestRunDisabledDropsManagedCopy(t *testing.T) {
	latest, corrupt := "1.5.9", false
	u := release(t, &latest, &corrupt)
	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	u.enabled = false
	u.Run(context.Background())
	if u.installed() != "" {
		t.Fatal("disabled updater left current in place")
	}
}

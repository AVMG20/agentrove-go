package server

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

func init() {
	// The runtime image has no /etc/mime.types, and Go's built-in table
	// lacks these.
	for ext, typ := range map[string]string{
		".webmanifest": "application/manifest+json",
		".woff":        "font/woff",
		".woff2":       "font/woff2",
		".ttf":         "font/ttf",
		".ico":         "image/x-icon",
		".txt":         "text/plain; charset=utf-8",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

// Frontend serves the built web app from dist, so no separate web server is
// needed in front of the API. Unknown paths get index.html (client-side
// routes), except under /api/ and /assets/, which 404.
func Frontend(dist fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(p, "/")
		if name == "" {
			name = "index.html"
		}
		assets := strings.HasPrefix(p, "/assets/")
		f, mod, err := openFile(dist, name)
		if err != nil && !assets {
			name = "index.html"
			f, mod, err = openFile(dist, name)
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		// Vite content-hashes everything under /assets/, so those are cached
		// forever. Everything else (index.html above all) is revalidated, or
		// browsers heuristically cache index.html and keep loading the previous
		// deploy's bundles in new tabs.
		if assets {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, name, mod, f)
	})
}

// openFile opens a regular file in dist; directories count as missing.
func openFile(dist fs.FS, name string) (io.ReadSeekCloser, time.Time, error) {
	f, err := dist.Open(name)
	if err != nil {
		return nil, time.Time{}, err
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return nil, time.Time{}, fs.ErrNotExist
	}
	rs, ok := f.(io.ReadSeekCloser)
	if !ok {
		f.Close()
		return nil, time.Time{}, errors.New("frontend: file is not seekable")
	}
	return rs, st.ModTime(), nil
}

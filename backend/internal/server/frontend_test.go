package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestFrontend(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":             {Data: []byte("<!doctype html>index")},
		"assets/app-abc123.js":   {Data: []byte("console.log(1)")},
		"manifest.webmanifest":   {Data: []byte("{}")},
		"fonts/inter.woff2":      {Data: []byte("wOF2")},
		"artifact-kit/kit.css":   {Data: []byte("body{}")},
		"icons/icon-192x192.png": {Data: []byte("\x89PNG\r\n\x1a\n")},
	}
	h := Frontend(dist)
	cases := []struct {
		path             string
		status           int
		body, cache, typ string
	}{
		{"/", 200, "<!doctype html>index", "no-cache", "text/html; charset=utf-8"},
		{"/index.html", 200, "<!doctype html>index", "no-cache", "text/html; charset=utf-8"},
		{"/chat/123", 200, "<!doctype html>index", "no-cache", "text/html; charset=utf-8"},
		{"/artifact-kit", 200, "<!doctype html>index", "no-cache", "text/html; charset=utf-8"},
		{"/assets/app-abc123.js", 200, "console.log(1)", "public, max-age=31536000, immutable", "text/javascript; charset=utf-8"},
		{"/assets/gone.js", 404, "", "", ""},
		{"/api/v1/nope", 404, "", "", ""},
		{"/api", 404, "", "", ""},
		{"/manifest.webmanifest", 200, "{}", "no-cache", "application/manifest+json"},
		{"/fonts/inter.woff2", 200, "wOF2", "no-cache", "font/woff2"},
		{"/artifact-kit/kit.css", 200, "body{}", "no-cache", "text/css; charset=utf-8"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if tc.status != http.StatusOK {
				return
			}
			if rec.Body.String() != tc.body {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.body)
			}
			if got := rec.Header().Get("Cache-Control"); got != tc.cache {
				t.Errorf("Cache-Control = %q, want %q", got, tc.cache)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.typ {
				t.Errorf("Content-Type = %q, want %q", got, tc.typ)
			}
		})
	}
}

// Package web holds the built frontend, compiled into the binary so one
// executable serves the whole app. The Dockerfile copies frontend/dist into
// dist/ before go build; without that the binary is API only.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Dist is the embedded frontend, or nil when the binary was built without one.
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

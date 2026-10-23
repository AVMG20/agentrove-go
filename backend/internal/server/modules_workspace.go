package server

import (
	"agentrove/internal/app"
	"agentrove/internal/github"
	"agentrove/internal/sandboxapi"
	"agentrove/internal/settings"
	"agentrove/internal/skills"
	"agentrove/internal/usage"
	"agentrove/internal/workspace"
)

// workspaceModules: workspaces, sandbox files/git/search/terminal, GitHub,
// settings, skills, usage.
func workspaceModules(a *app.App) ([]Module, error) {
	sbx := sandboxapi.New(a)
	return []Module{
		workspace.New(a, sbx.TerminateSandbox),
		sbx,
		github.New(a),
		settings.New(a),
		skills.New(a),
		usage.New(a),
	}, nil
}

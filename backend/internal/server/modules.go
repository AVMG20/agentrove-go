package server

import "agentrove/internal/app"

// buildModules constructs every feature module. Order matters only for hooks:
// chat comes first so later modules can rely on a.Hooks being set.
func buildModules(a *app.App) ([]Module, error) {
	var out []Module
	for _, group := range []func(*app.App) ([]Module, error){chatModules, workspaceModules, collabModules} {
		mods, err := group(a)
		if err != nil {
			return nil, err
		}
		out = append(out, mods...)
	}
	return out, nil
}

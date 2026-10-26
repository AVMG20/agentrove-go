package server

import (
	"agentrove/internal/app"
	"agentrove/internal/artifact"
	"agentrove/internal/automation"
	"agentrove/internal/mcpserver"
)

// collabModules: automations, artifacts, MCP server.
func collabModules(a *app.App) ([]Module, error) {
	au := automation.New(a)
	ar := artifact.New(a)
	mcp := mcpserver.New(a, au.Service(), ar.Service())
	return []Module{au, ar, mcp}, nil
}

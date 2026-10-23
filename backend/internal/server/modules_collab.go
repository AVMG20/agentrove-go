package server

import (
	"agentrove/internal/app"
	"agentrove/internal/artifact"
	"agentrove/internal/automation"
	"agentrove/internal/channel"
	"agentrove/internal/mcpserver"
)

// collabModules: channels, automations, artifacts, MCP server.
func collabModules(a *app.App) ([]Module, error) {
	ch := channel.New(a)
	au := automation.New(a)
	ar := artifact.New(a)
	mcp := mcpserver.New(a, ch.Service(), au.Service(), ar.Service())
	return []Module{ch, au, ar, mcp}, nil
}

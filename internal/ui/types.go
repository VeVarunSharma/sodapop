package ui

import (
	"context"

	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/skills"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
)

type Workspace interface {
	Status(context.Context) (workspace.Status, error)
	Diff(context.Context, string) (workspace.Diff, error)
	CaptureBaseline(context.Context) (workspace.Baseline, error)
}

// Capability describes an MCP server or skill shown in the sidebar.
// Availability is informational; it does not enable the capability.
type Capability struct {
	Name   string
	Active bool
}

type Options struct {
	Context         context.Context
	Project         string
	Version         string
	NoBanner        bool
	Preferences     config.Preferences
	SavePreferences func(config.Preferences) error
	LoadMCP         func(context.Context, auth.Account) (config.MCPRegistry, error)
	SaveMCP         func(context.Context, auth.Account, config.MCPRegistry) error
	ManageSkills    func(context.Context, skills.Action) (skills.State, error)
	Auth            auth.Service
	NewEngine       func(context.Context, auth.Account) (engine.Engine, error)
	Workspace       Workspace
	MCPServers      []Capability
	Skills          []Capability
}

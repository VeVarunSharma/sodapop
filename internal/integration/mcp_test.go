package integration

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func TestMCPRegistryIsAccountScopedAtEngineBoundary(t *testing.T) {
	state := t.TempDir()
	paths := config.Paths{StateDir: state}
	accountA, err := paths.AccountHome("account-a")
	if err != nil {
		t.Fatal(err)
	}
	accountB, err := paths.AccountHome("account-b")
	if err != nil {
		t.Fatal(err)
	}
	registryPath, err := paths.AccountMCPFile("account-a")
	if err != nil {
		t.Fatal(err)
	}
	registry := config.MCPRegistry{Version: 1, Servers: []config.MCPServer{
		{Name: "enabled", Command: "synthetic-mcp", Args: []string{"--stdio"}, Tools: []string{"read"}, Enabled: true},
		{Name: "disabled", Command: "must-not-run", Enabled: false},
	}}
	if err := config.SaveMCP(registryPath, registry); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadMCP(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	otherPath, err := paths.AccountMCPFile("account-b")
	if err != nil {
		t.Fatal(err)
	}
	other, err := config.LoadMCP(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Servers) != 0 || filepath.Dir(registryPath) != accountA || filepath.Dir(otherPath) != accountB {
		t.Fatal("MCP registry crossed account boundaries")
	}

	var servers []engine.MCPServer
	for _, server := range loaded.Servers {
		if server.Enabled {
			servers = append(servers, engine.MCPServer{
				Name: server.Name, Command: server.Command,
				Args:  append([]string(nil), server.Args...),
				Tools: append([]string(nil), server.Tools...),
			})
		}
	}
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backend, err := engine.New(engine.Config{
		Project: project, Home: accountA, AccountID: "account-a", MCPServers: servers,
		TokenSource: func(context.Context) (string, error) { return "synthetic-token", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "enabled" {
		t.Fatal("disabled MCP server reached the engine boundary")
	}
}

func TestMCPPermissionEventIsDeniedExactlyOnce(t *testing.T) {
	trace := newTurnTrace(newFixture(t), "synthetic-session")
	responses := 0
	event := engine.Event{
		Kind: engine.EventPermission, ID: "mcp-permission", SessionID: trace.sessionID, ToolID: "mcp-tool",
		Permission: &engine.Permission{
			ID: "mcp-request", Kind: "mcp",
			Description: "External MCP tool filesystem / read\n{\"path\":\"fixture.go\"}",
			Respond: func(allow bool) error {
				responses++
				if allow {
					t.Fatal("integration policy approved an MCP request")
				}
				return nil
			},
		},
	}
	if err := trace.accept(event); err == nil {
		t.Fatal("MCP permission event qualified without explicit fixture policy")
	}
	if err := trace.accept(event); err == nil {
		t.Fatal("duplicate MCP permission event qualified")
	}
	if responses != 1 {
		t.Fatalf("MCP permission response count = %d, want 1", responses)
	}
}

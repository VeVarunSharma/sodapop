package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/config"
)

func TestDecodeMCPServerIsStrictAndDefaultsDisabled(t *testing.T) {
	server, err := decodeMCPServer("filesystem", `{"command":"npx","args":["-y","server"],"env":["TOKEN"],"tools":["read_file"],"timeout_seconds":30}`)
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "filesystem" || server.Command != "npx" || server.Enabled ||
		len(server.Args) != 2 || len(server.Env) != 1 || server.TimeoutSeconds != 30 {
		t.Fatalf("decoded server = %#v", server)
	}
	for _, payload := range []string{
		`{"command":"npx","secret":"value"}`,
		`{"command":""}`,
		`{"command":"npx"} {}`,
	} {
		if _, err := decodeMCPServer("filesystem", payload); err == nil {
			t.Errorf("accepted invalid payload %q", payload)
		}
	}
}

func TestManageMCPPersistsAndUpdatesSidebar(t *testing.T) {
	m, _ := readyModel(t)
	var saved config.MCPRegistry
	m.opts.SaveMCP = func(_ context.Context, account auth.Account, registry config.MCPRegistry) error {
		if account.ID != "account-a" {
			t.Fatalf("saved for account %q", account.ID)
		}
		saved = cloneMCPRegistry(registry)
		return nil
	}
	cmd, ok := m.manageMCP(`add filesystem {"command":"npx","args":["-y","server"]}`)
	if !ok || cmd == nil {
		t.Fatal("add did not schedule persistence")
	}
	if second, accepted := m.manageMCP("enable filesystem"); accepted || second != nil {
		t.Fatal("overlapping MCP save was accepted")
	}
	m.Update(cmd())
	if len(saved.Servers) != 1 || saved.Servers[0].Enabled || len(m.mcpServers) != 1 || m.mcpServers[0].Active {
		t.Fatalf("added registry = %#v, sidebar = %#v", saved, m.mcpServers)
	}
	cmd, ok = m.manageMCP("enable filesystem")
	if !ok || cmd == nil {
		t.Fatal("enable did not schedule persistence")
	}
	m.Update(cmd())
	if len(saved.Servers) != 1 || !saved.Servers[0].Enabled || !m.mcpServers[0].Active {
		t.Fatalf("enabled registry = %#v, sidebar = %#v", saved, m.mcpServers)
	}
	m.manageMCP("")
	if m.overlay == nil || m.overlay.kind != dialogMCP || !strings.Contains(m.overlay.body, "filesystem") {
		t.Fatal("MCP status dialog omitted configured server")
	}
}

func TestManageMCPRestoresPreviousRegistryOnSaveFailure(t *testing.T) {
	m, _ := readyModel(t)
	m.setMCPRegistry(config.MCPRegistry{Version: 1, Servers: []config.MCPServer{{
		Name: "server", Command: "mcp-server", Enabled: false,
	}}})
	m.opts.SaveMCP = func(context.Context, auth.Account, config.MCPRegistry) error {
		return errors.New("disk full")
	}
	cmd, ok := m.manageMCP("enable server")
	if !ok || cmd == nil || !m.mcpServers[0].Active {
		t.Fatal("optimistic update was not applied")
	}
	m.Update(cmd())
	if m.mcpServers[0].Active || !m.notice.error || !strings.Contains(m.notice.text, "not saved") {
		t.Fatalf("failed save was not restored: %#v, %#v", m.mcpServers, m.notice)
	}
}

func TestMCPLoadAndManagementErrors(t *testing.T) {
	m, _ := readyModel(t)
	registry := config.MCPRegistry{Version: 1, Servers: []config.MCPServer{{
		Name: "server", Command: "mcp-server", Enabled: true,
	}}}
	m.opts.LoadMCP = func(context.Context, auth.Account) (config.MCPRegistry, error) {
		return registry, nil
	}
	cmd := m.loadMCP()
	if cmd == nil {
		t.Fatal("MCP load was not scheduled")
	}
	m.Update(cmd())
	if len(m.mcpServers) != 1 || !m.mcpServers[0].Active {
		t.Fatalf("loaded MCP servers = %#v", m.mcpServers)
	}

	m.opts.LoadMCP = func(context.Context, auth.Account) (config.MCPRegistry, error) {
		return config.MCPRegistry{}, errors.New("read failed")
	}
	cmd = m.loadMCP()
	m.Update(cmd())
	if !m.notice.error || !strings.Contains(m.notice.text, "could not be loaded") {
		t.Fatal("MCP load error was hidden")
	}

	for _, args := range []string{
		"unknown", "enable missing", "enable server extra",
		`add server {"command":"other"}`,
		`add bad {"command":""}`,
		"add incomplete",
	} {
		if command, accepted := m.manageMCP(args); accepted || command != nil {
			t.Errorf("invalid MCP command %q was accepted", args)
		}
	}

	m.opts.SaveMCP = nil
	if command, accepted := m.manageMCP("disable server"); !accepted || command != nil {
		t.Fatal("unavailable persistence did not remain a local handled error")
	}
	if !m.notice.error || !strings.Contains(m.notice.text, "persistence is unavailable") {
		t.Fatal("missing persistence was not reported")
	}
}

func TestMCPCommandsDisableRemoveAndRequireAccount(t *testing.T) {
	m, _ := readyModel(t)
	m.setMCPRegistry(config.MCPRegistry{Version: 1, Servers: []config.MCPServer{{
		Name: "server", Command: "mcp-server", Enabled: true,
	}}})
	m.opts.SaveMCP = func(context.Context, auth.Account, config.MCPRegistry) error { return nil }

	cmd, ok := m.manageMCP("disable server")
	if !ok || cmd == nil {
		t.Fatal("disable was not scheduled")
	}
	m.Update(cmd())
	if m.mcpServers[0].Active {
		t.Fatal("server remained enabled")
	}
	cmd, ok = m.manageMCP("remove server")
	if !ok || cmd == nil {
		t.Fatal("remove was not scheduled")
	}
	m.Update(cmd())
	if len(m.mcpServers) != 0 {
		t.Fatal("server remained after removal")
	}

	m.account = auth.Account{}
	if command, accepted := m.manageMCP(""); accepted || command != nil {
		t.Fatal("signed-out MCP management was accepted")
	}
	if m.loadMCP() != nil {
		t.Fatal("signed-out MCP load was scheduled")
	}
}

func TestMCPHelpersRejectIncompleteInput(t *testing.T) {
	if _, _, ok := splitMCPAdd("add"); ok {
		t.Fatal("missing add arguments accepted")
	}
	if _, _, ok := splitMCPAdd("add server"); ok {
		t.Fatal("missing JSON payload accepted")
	}
	registry := config.DefaultMCPRegistry()
	registry.Servers = []config.MCPServer{{Name: "server", Command: "mcp"}}
	m := testModel(t, testOptions())
	m.mcpLoadResult(mcpLoadedMsg{generation: 99, accountID: "other", registry: registry})
	if len(m.mcpServers) != 0 {
		t.Fatal("stale MCP load changed state")
	}
	m.mcpSaveResult(mcpSavedMsg{generation: 99, accountID: "other", previous: registry})
}

func TestMCPHelpersRenderEmptyStateAndCloneDeeply(t *testing.T) {
	name, payload, ok := splitMCPAdd("add filesystem \t {\"command\":\"server\"}")
	if !ok || name != "filesystem" || payload != `{"command":"server"}` {
		t.Fatalf("split MCP add = %q, %q, %t", name, payload, ok)
	}
	registry := config.MCPRegistry{Version: 1, Servers: []config.MCPServer{{
		Name: "FileSystem", Command: "server", Args: []string{"one"},
		Env: []string{"TOKEN"}, Tools: []string{"read_file"},
	}}}
	clone := cloneMCPRegistry(registry)
	clone.Servers[0].Args[0] = "changed"
	clone.Servers[0].Env[0] = "OTHER"
	clone.Servers[0].Tools[0] = "write_file"
	if registry.Servers[0].Args[0] != "one" || registry.Servers[0].Env[0] != "TOKEN" ||
		registry.Servers[0].Tools[0] != "read_file" {
		t.Fatal("MCP registry clone shared nested slices")
	}

	m, _ := readyModel(t)
	m.setMCPRegistry(registry)
	if index := m.mcpServerIndex("filesystem"); index != 0 {
		t.Fatalf("case-insensitive MCP index = %d", index)
	}
	m.setMCPRegistry(config.DefaultMCPRegistry())
	m.showMCP()
	if !strings.Contains(m.overlay.body, "No MCP servers configured") {
		t.Fatalf("empty MCP dialog = %q", m.overlay.body)
	}
	if command, accepted := m.mcpUsage(); command != nil || accepted || !m.notice.error {
		t.Fatal("MCP usage failure was not reported")
	}
}

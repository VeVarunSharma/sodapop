package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/config"
)

type mcpServerInput struct {
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	Env            []string `json:"env,omitempty"`
	Tools          []string `json:"tools,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

func (m *Model) loadMCP() tea.Cmd {
	if m.account.ID == "" || m.opts.LoadMCP == nil {
		return nil
	}
	m.mcpGeneration++
	generation, account, load := m.mcpGeneration, m.account, m.opts.LoadMCP
	ctx := m.life.ctx
	return func() tea.Msg {
		registry, err := load(ctx, account)
		return mcpLoadedMsg{generation: generation, accountID: account.ID, registry: registry, err: err}
	}
}

func (m *Model) mcpLoadResult(msg mcpLoadedMsg) {
	if msg.generation != m.mcpGeneration || msg.accountID != m.account.ID {
		return
	}
	if msg.err != nil {
		m.report("MCP configuration could not be loaded: "+msg.err.Error(), true)
		return
	}
	m.setMCPRegistry(msg.registry)
}

func (m *Model) setMCPRegistry(registry config.MCPRegistry) {
	m.mcpRegistry = cloneMCPRegistry(registry)
	m.mcpServers = make([]Capability, 0, len(registry.Servers))
	for _, server := range registry.Servers {
		m.mcpServers = append(m.mcpServers, Capability{Name: server.Name, Active: server.Enabled})
	}
	slices.SortFunc(m.mcpServers, func(a, b Capability) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	m.sidebarCache = ""
}

func (m *Model) manageMCP(args string) (tea.Cmd, bool) {
	if m.account.ID == "" {
		m.report("Sign in to GitHub before managing account-scoped MCP servers.", true)
		return nil, false
	}
	if m.mcpSaving {
		m.report("MCP configuration is still saving. Wait for it to finish before making another change.", false)
		return nil, false
	}
	fields := strings.Fields(args)
	if len(fields) == 0 || strings.EqualFold(fields[0], "list") || strings.EqualFold(fields[0], "status") {
		m.showMCP()
		return nil, true
	}
	switch strings.ToLower(fields[0]) {
	case "reconnect":
		if len(fields) != 1 {
			return m.mcpUsage()
		}
		return m.connect(), true
	case "enable", "disable", "remove":
		if len(fields) != 2 {
			return m.mcpUsage()
		}
		index := m.mcpServerIndex(fields[1])
		if index < 0 {
			m.report("Unknown MCP server: "+singleLine(fields[1])+". Open /mcp to list configured servers.", true)
			return nil, false
		}
		next := cloneMCPRegistry(m.mcpRegistry)
		switch strings.ToLower(fields[0]) {
		case "enable":
			next.Servers[index].Enabled = true
		case "disable":
			next.Servers[index].Enabled = false
		case "remove":
			next.Servers = append(next.Servers[:index], next.Servers[index+1:]...)
		}
		return m.saveMCP(next), true
	case "add":
		name, payload, ok := splitMCPAdd(args)
		if !ok {
			return m.mcpUsage()
		}
		server, err := decodeMCPServer(name, payload)
		if err != nil {
			m.report("Invalid MCP server: "+err.Error(), true)
			return nil, false
		}
		if m.mcpServerIndex(name) >= 0 {
			m.report("MCP server "+singleLine(name)+" already exists. Remove it before adding a replacement.", true)
			return nil, false
		}
		next := cloneMCPRegistry(m.mcpRegistry)
		next.Servers = append(next.Servers, server)
		return m.saveMCP(next), true
	default:
		return m.mcpUsage()
	}
}

func (m *Model) mcpUsage() (tea.Cmd, bool) {
	m.report("Usage: /mcp, /mcp add <name> <JSON>, /mcp enable|disable|remove <name>, or /mcp reconnect.", true)
	return nil, false
}

func (m *Model) showMCP() {
	var body strings.Builder
	body.WriteString("Only servers configured explicitly for this Sodapop account are shown. Ambient Copilot MCP configuration is never imported.\n\n")
	if len(m.mcpRegistry.Servers) == 0 {
		body.WriteString("No MCP servers configured.\n\n")
	} else {
		for _, server := range m.mcpRegistry.Servers {
			state := "disabled"
			if server.Enabled {
				state = "enabled"
			}
			fmt.Fprintf(&body, "%s  %s\n  command: %s", server.Name, state, server.Command)
			if len(server.Args) > 0 {
				fmt.Fprintf(&body, "\n  args: %s", strings.Join(server.Args, " "))
			}
			if len(server.Env) > 0 {
				fmt.Fprintf(&body, "\n  environment references: %s", strings.Join(server.Env, ", "))
			}
			body.WriteString("\n\n")
		}
	}
	body.WriteString("Add: /mcp add <name> {\"command\":\"npx\",\"args\":[\"-y\",\"server\"]}\n")
	body.WriteString("Manage: /mcp enable|disable|remove <name>\n")
	body.WriteString("Run /mcp reconnect after changes. MCP tool calls still require normal approval.")
	m.newDialog(dialogMCP, "MCP SERVERS / explicit and account-scoped", body.String())
}

func (m *Model) saveMCP(next config.MCPRegistry) tea.Cmd {
	if m.opts.SaveMCP == nil {
		m.report("MCP configuration persistence is unavailable in this build.", true)
		return nil
	}
	if err := config.ValidateMCP(next); err != nil {
		m.report("Invalid MCP configuration: "+err.Error(), true)
		return nil
	}
	m.mcpGeneration++
	m.mcpSaving = true
	generation, account := m.mcpGeneration, m.account
	previous := cloneMCPRegistry(m.mcpRegistry)
	m.setMCPRegistry(next)
	save, ctx := m.opts.SaveMCP, m.life.ctx
	return func() tea.Msg {
		err := save(ctx, account, next)
		return mcpSavedMsg{generation: generation, accountID: account.ID, previous: previous, err: err}
	}
}

func (m *Model) mcpSaveResult(msg mcpSavedMsg) tea.Cmd {
	if msg.generation != m.mcpGeneration || msg.accountID != m.account.ID {
		return nil
	}
	m.mcpSaving = false
	if msg.err != nil {
		m.setMCPRegistry(msg.previous)
		m.report("MCP configuration was not saved: "+msg.err.Error(), true)
		return nil
	}
	m.report("MCP configuration saved. Run /mcp reconnect to apply it; active work was not changed.", false)
	return nil
}

func (m *Model) mcpServerIndex(name string) int {
	for index, server := range m.mcpRegistry.Servers {
		if strings.EqualFold(server.Name, name) {
			return index
		}
	}
	return -1
}

func splitMCPAdd(args string) (string, string, bool) {
	rest := strings.TrimSpace(args)
	if index := strings.IndexAny(rest, " \t\r\n"); index >= 0 {
		rest = strings.TrimSpace(rest[index:])
	} else {
		return "", "", false
	}
	index := strings.IndexAny(rest, " \t\r\n")
	if index <= 0 {
		return "", "", false
	}
	name, payload := rest[:index], strings.TrimSpace(rest[index:])
	return name, payload, payload != ""
}

func decodeMCPServer(name, payload string) (config.MCPServer, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	decoder.DisallowUnknownFields()
	var input mcpServerInput
	if err := decoder.Decode(&input); err != nil {
		return config.MCPServer{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return config.MCPServer{}, errors.New("server definition must contain exactly one JSON object")
	}
	server := config.MCPServer{
		Name: name, Command: input.Command, Args: input.Args, Env: input.Env,
		Tools: input.Tools, TimeoutSeconds: input.TimeoutSeconds, Enabled: false,
	}
	registry := config.MCPRegistry{Version: config.DefaultMCPRegistry().Version, Servers: []config.MCPServer{server}}
	if err := config.ValidateMCP(registry); err != nil {
		return config.MCPServer{}, err
	}
	return server, nil
}

func cloneMCPRegistry(registry config.MCPRegistry) config.MCPRegistry {
	result := config.MCPRegistry{Version: registry.Version, Servers: make([]config.MCPServer, len(registry.Servers))}
	for index, server := range registry.Servers {
		server.Args = append([]string(nil), server.Args...)
		server.Env = append([]string(nil), server.Env...)
		server.Tools = append([]string(nil), server.Tools...)
		result.Servers[index] = server
	}
	return result
}

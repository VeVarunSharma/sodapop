package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
)

const (
	currentMCPVersion = 1
	maxMCPConfigBytes = 64 * 1024
)

var (
	mcpNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type MCPServer struct {
	Name           string   `json:"name"`
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	Env            []string `json:"env,omitempty"`
	Tools          []string `json:"tools,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Enabled        bool     `json:"enabled"`
}

type MCPRegistry struct {
	Version int         `json:"version"`
	Servers []MCPServer `json:"servers"`
}

func DefaultMCPRegistry() MCPRegistry {
	return MCPRegistry{Version: currentMCPVersion, Servers: []MCPServer{}}
}

func (p Paths) AccountMCPFile(accountID string) (string, error) {
	home, err := p.AccountHome(accountID)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "mcp.json"), nil
}

func LoadMCP(path string) (MCPRegistry, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultMCPRegistry(), nil
	}
	if err != nil {
		return MCPRegistry{}, fmt.Errorf("open Sodapop MCP configuration: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxMCPConfigBytes+1))
	if err != nil {
		return MCPRegistry{}, fmt.Errorf("read Sodapop MCP configuration: %w", err)
	}
	if len(data) > maxMCPConfigBytes {
		return MCPRegistry{}, errors.New("Sodapop MCP configuration exceeds the supported size")
	}
	var registry MCPRegistry
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&registry); err != nil {
		return MCPRegistry{}, fmt.Errorf("decode Sodapop MCP configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return MCPRegistry{}, errors.New("Sodapop MCP configuration must contain exactly one JSON object")
	}
	if err := ValidateMCP(registry); err != nil {
		return MCPRegistry{}, err
	}
	return registry, nil
}

func SaveMCP(path string, registry MCPRegistry) error {
	if err := ValidateMCP(registry); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Sodapop MCP configuration: %w", err)
	}
	if err := securefs.MkdirAllPrivate(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create Sodapop MCP configuration directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".sodapop-mcp-*.json")
	if err != nil {
		return fmt.Errorf("create temporary Sodapop MCP configuration: %w", err)
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := securefs.ProtectFile(file); err != nil {
		file.Close()
		return fmt.Errorf("protect temporary Sodapop MCP configuration: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return fmt.Errorf("write Sodapop MCP configuration: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync Sodapop MCP configuration: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Sodapop MCP configuration: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("replace Sodapop MCP configuration: %w", err)
	}
	return nil
}

func ValidateMCP(registry MCPRegistry) error {
	if registry.Version != currentMCPVersion {
		return fmt.Errorf("unsupported Sodapop MCP configuration version %d; expected version %d", registry.Version, currentMCPVersion)
	}
	if len(registry.Servers) > 32 {
		return errors.New("Sodapop MCP configuration supports at most 32 servers")
	}
	seen := make(map[string]struct{}, len(registry.Servers))
	for _, server := range registry.Servers {
		if !mcpNamePattern.MatchString(server.Name) {
			return fmt.Errorf("MCP server name %q is invalid", server.Name)
		}
		key := strings.ToLower(server.Name)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("MCP server name %q is duplicated", server.Name)
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(server.Command) == "" || len(server.Command) > 1024 || hasControl(server.Command) {
			return fmt.Errorf("MCP server %q requires a valid command", server.Name)
		}
		if len(server.Args) > 64 {
			return fmt.Errorf("MCP server %q has too many arguments", server.Name)
		}
		for _, arg := range server.Args {
			if len(arg) > 4096 || hasControl(arg) {
				return fmt.Errorf("MCP server %q has an invalid argument", server.Name)
			}
		}
		if len(server.Env) > 32 {
			return fmt.Errorf("MCP server %q references too many environment variables", server.Name)
		}
		envSeen := make(map[string]struct{}, len(server.Env))
		for _, name := range server.Env {
			if !envNamePattern.MatchString(name) {
				return fmt.Errorf("MCP server %q has invalid environment variable %q", server.Name, name)
			}
			if _, exists := envSeen[name]; exists {
				return fmt.Errorf("MCP server %q repeats environment variable %q", server.Name, name)
			}
			envSeen[name] = struct{}{}
		}
		if len(server.Tools) > 128 {
			return fmt.Errorf("MCP server %q exposes too many tools", server.Name)
		}
		for _, tool := range server.Tools {
			if strings.TrimSpace(tool) == "" || len(tool) > 256 || hasControl(tool) {
				return fmt.Errorf("MCP server %q has an invalid tool filter", server.Name)
			}
		}
		if server.TimeoutSeconds < 0 || server.TimeoutSeconds > 300 {
			return fmt.Errorf("MCP server %q timeout must be between 0 and 300 seconds", server.Name)
		}
	}
	return nil
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

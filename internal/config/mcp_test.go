package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
)

func TestMCPRegistryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account", "mcp.json")
	missing, err := LoadMCP(path)
	if err != nil || !reflect.DeepEqual(missing, DefaultMCPRegistry()) {
		t.Fatalf("missing registry: %#v, %v", missing, err)
	}
	want := MCPRegistry{Version: 1, Servers: []MCPServer{{
		Name: "filesystem", Command: "npx", Args: []string{"-y", "server", "."},
		Env: []string{"MCP_TOKEN"}, Tools: []string{"read_file"}, TimeoutSeconds: 30, Enabled: true,
	}}}
	if err := SaveMCP(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMCP(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %#v, %v", got, err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	private, privacyErr := securefs.IsPrivateRegularFile(file)
	closeErr := file.Close()
	if privacyErr != nil || closeErr != nil || !private {
		t.Fatalf("expected private registry: private=%t, err=%v, close=%v", private, privacyErr, closeErr)
	}
}

func TestMCPRegistryRejectsMalformedData(t *testing.T) {
	cases := []string{
		``,
		`{`,
		`{"version":2,"servers":[]}`,
		`{"version":1,"servers":[],"unknown":true}`,
		`{"version":1,"servers":[]} {}`,
		`{"version":1,"servers":[{"name":"bad name","command":"npx","enabled":true}]}`,
		`{"version":1,"servers":[{"name":"one","command":"","enabled":true}]}`,
		`{"version":1,"servers":[{"name":"one","command":"npx","unknown":true,"enabled":true}]}`,
		`{"version":1,"servers":[{"name":"One","command":"a","enabled":true},{"name":"one","command":"b","enabled":false}]}`,
		`{"version":1,"servers":[{"name":"one","command":"npx","env":["TOKEN=secret"],"enabled":true}]}`,
		strings.Repeat(" ", maxMCPConfigBytes+1),
	}
	for _, contents := range cases {
		path := filepath.Join(t.TempDir(), "mcp.json")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadMCP(path); err == nil {
			t.Errorf("accepted malformed MCP configuration: %q", contents)
		}
	}
}

func TestMCPRegistryValidationBoundaries(t *testing.T) {
	valid := MCPRegistry{Version: 1, Servers: []MCPServer{{
		Name: "server-1", Command: "/usr/local/bin/mcp", Args: []string{"--safe"},
		Env: []string{"TOKEN_NAME"}, Tools: []string{"*"}, TimeoutSeconds: 300,
	}}}
	if err := ValidateMCP(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Servers = append(invalid.Servers, valid.Servers[0])
	if err := ValidateMCP(invalid); err == nil {
		t.Fatal("duplicate server accepted")
	}
	invalid = valid
	invalid.Servers[0].Args = []string{"bad\nargument"}
	if err := ValidateMCP(invalid); err == nil {
		t.Fatal("control character accepted")
	}
	cases := []MCPRegistry{
		{Version: 2, Servers: []MCPServer{}},
		{Version: 1, Servers: make([]MCPServer, 33)},
		{Version: 1, Servers: []MCPServer{{Name: "server", Command: strings.Repeat("x", 1025)}}},
		{Version: 1, Servers: []MCPServer{{Name: "server", Command: "mcp", Args: make([]string, 65)}}},
		{Version: 1, Servers: []MCPServer{{Name: "server", Command: "mcp", Args: []string{strings.Repeat("x", 4097)}}}},
		{Version: 1, Servers: []MCPServer{{Name: "server", Command: "mcp", Env: make([]string, 33)}}},
		{Version: 1, Servers: []MCPServer{{Name: "server", Command: "mcp", Env: []string{"TOKEN", "TOKEN"}}}},
		{Version: 1, Servers: []MCPServer{{Name: "server", Command: "mcp", Tools: make([]string, 129)}}},
		{Version: 1, Servers: []MCPServer{{Name: "server", Command: "mcp", Tools: []string{""}}}},
		{Version: 1, Servers: []MCPServer{{Name: "server", Command: "mcp", TimeoutSeconds: -1}}},
	}
	for index, registry := range cases {
		if err := ValidateMCP(registry); err == nil {
			t.Errorf("invalid boundary case %d accepted", index)
		}
	}
}

func TestAccountMCPFileIsAccountScoped(t *testing.T) {
	paths := Paths{StateDir: t.TempDir()}
	first, err := paths.AccountMCPFile("account-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := paths.AccountMCPFile("account-b")
	if err != nil || first == second || filepath.Base(first) != "mcp.json" {
		t.Fatalf("account MCP files: %q %q %v", first, second, err)
	}
	if _, err := paths.AccountMCPFile(""); err == nil {
		t.Fatal("missing account accepted")
	}
}

func TestMCPRegistryIOErrorsAreVisible(t *testing.T) {
	directory := t.TempDir()
	if _, err := LoadMCP(directory); err == nil {
		t.Fatal("directory was accepted as an MCP registry")
	}
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveMCP(filepath.Join(parentFile, "mcp.json"), DefaultMCPRegistry()); err == nil {
		t.Fatal("invalid registry directory was accepted")
	}
	if err := SaveMCP(filepath.Join(t.TempDir(), "mcp.json"), MCPRegistry{}); err == nil {
		t.Fatal("invalid registry was saved")
	}
}

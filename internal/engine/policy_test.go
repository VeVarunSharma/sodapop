package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func policyFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	outside := filepath.Join(root, "project-other")
	for _, dir := range []string{project, outside, filepath.Join(project, "sub")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(project, "file.txt"), filepath.Join(outside, "secret.txt")} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	project, _ = canonicalDirectory(project)
	outside, _ = canonicalDirectory(outside)
	for name, target := range map[string]string{
		"inside": filepath.Join(project, "file.txt"),
		"escape": outside,
		"broken": filepath.Join(outside, "missing"),
		"loop":   filepath.Join(project, "loop"),
	} {
		if err := os.Symlink(target, filepath.Join(project, name)); err != nil {
			t.Fatal(err)
		}
	}
	return project, outside
}

func TestCanonicalReadPolicy(t *testing.T) {
	project, outside := policyFixture(t)
	for _, test := range []struct {
		name string
		path string
		auto bool
	}{
		{"relative file", "file.txt", true},
		{"absolute file", filepath.Join(project, "file.txt"), true},
		{"directory", ".", true},
		{"normalized in project", "sub/../file.txt", true},
		{"inside symlink", "inside", true},
		{"outside absolute", filepath.Join(outside, "secret.txt"), false},
		{"prefix sibling", "../project-other/secret.txt", false},
		{"escape symlink", "escape/secret.txt", false},
		{"symlink before dotdot", "escape/../project-other/secret.txt", false},
		{"dangling symlink", "broken", false},
		{"symlink loop", "loop", false},
		{"missing path", "missing", false},
		{"empty", "", false},
		{"whitespace", "  ", false},
		{"home expansion", "~/secret", false},
		{"URI", "file:///etc/passwd", false},
		{"NUL", "file\x00.txt", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			permission, _, auto := permissionDetails(project, &copilot.PermissionRequestRead{Path: test.path, Intention: "Read fixture"})
			if auto != test.auto {
				t.Fatalf("auto=%v, want %v; %s", auto, test.auto, permission.Description)
			}
			if permission.Path != test.path {
				t.Fatalf("lost original path: %q", permission.Path)
			}
		})
	}
}

func TestOnlyStructuredReadsAutoApprove(t *testing.T) {
	project, _ := policyFixture(t)
	for _, request := range []copilot.PermissionRequest{
		nil,
		(*copilot.PermissionRequestRead)(nil),
		copilot.PermissionRequestRead{Path: "file.txt"},
		&copilot.PermissionRequestRead{Path: "file.txt", ManagedApprovalRequired: copilot.Bool(true)},
		&copilot.PermissionRequestRead{Path: "file.txt", RequestSandboxBypass: copilot.Bool(true)},
		&copilot.PermissionRequestShell{
			FullCommandText: "cat file.txt",
			Commands:        []copilot.PermissionRequestShellCommand{{Identifier: "cat", ReadOnly: true}},
		},
		&copilot.PermissionRequestWrite{FileName: "file.txt"},
		&copilot.PermissionRequestMCP{ReadOnly: true, ServerName: "external", ToolName: "read"},
		&copilot.PermissionRequestHook{ToolName: "view", ToolArgs: map[string]any{"path": "file.txt"}},
		copilot.RawPermissionRequest{Discriminator: copilot.PermissionRequestKindRead, Raw: json.RawMessage(`{"kind":"read","path":"file.txt"}`)},
	} {
		if permission, _, auto := permissionDetails(project, request); auto {
			t.Fatalf("auto-approved %T: %+v", request, permission)
		}
	}
}

func TestPermissionMetadata(t *testing.T) {
	project, _ := policyFixture(t)
	toolID := "tool-123"
	write, gotToolID, _ := permissionDetails(project, &copilot.PermissionRequestWrite{
		FileName: "file.txt", Intention: "Update file", Diff: "-old\n+new", ToolCallID: &toolID,
		ManagedApprovalRequired: copilot.Bool(true), RequestSandboxBypass: copilot.Bool(true),
	})
	if gotToolID != toolID || write.Path != "file.txt" ||
		!strings.Contains(write.Description, "-old\n+new") || !strings.Contains(write.Description, "Enterprise policy") ||
		!strings.Contains(write.Description, "outside the runtime sandbox") {
		t.Fatalf("incomplete write metadata: %+v", write)
	}
	shell, _, _ := permissionDetails(project, &copilot.PermissionRequestShell{FullCommandText: "echo ok", Intention: "Run command"})
	if shell.Command != "echo ok" {
		t.Fatal("lost shell command")
	}
	url, _, _ := permissionDetails(project, &copilot.PermissionRequestURL{URL: "https://example.invalid", Intention: "Fetch"})
	if url.URL != "https://example.invalid" {
		t.Fatal("lost URL")
	}
}

func TestPreToolHookNeverGrantsShellOrWrites(t *testing.T) {
	project, _ := policyFixture(t)
	for _, name := range []string{"bash", "write_bash", "read_bash", "edit", "create", "apply_patch", "unknown", "web_fetch"} {
		output := preToolDecision(project, copilot.PreToolUseHookInput{
			ToolName: name, WorkingDirectory: project, ToolArgs: map[string]any{"path": "file.txt", "command": "true"},
		})
		if output == nil || output.PermissionDecision != "ask" {
			t.Fatalf("%s did not require an ask gate: %+v", name, output)
		}
	}
	for _, args := range []map[string]any{
		{"path": "escape/secret.txt"}, {"path": 1}, {"path": ""}, {"path": "file.txt", "paths": []string{"/etc"}},
	} {
		output := preToolDecision(project, copilot.PreToolUseHookInput{ToolName: "view", WorkingDirectory: project, ToolArgs: args})
		if output == nil || output.PermissionDecision != "ask" {
			t.Fatalf("ambiguous read skipped ask: %#v", args)
		}
	}
	for _, name := range []string{"view", "glob", "grep", "rg"} {
		if output := preToolDecision(project, copilot.PreToolUseHookInput{
			ToolName: name, WorkingDirectory: project, ToolArgs: map[string]any{"path": "file.txt"},
		}); output != nil {
			t.Fatalf("%s project read should defer to structured native read policy, not override it: %+v", name, output)
		}
	}
}

func TestProjectInstructionsStayInsideProject(t *testing.T) {
	project, outside := policyFixture(t)
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("Use project conventions."), 0600); err != nil {
		t.Fatal(err)
	}
	instructions, err := projectInstructions(project)
	if err != nil || !strings.Contains(instructions, "Use project conventions.") {
		t.Fatalf("project instructions: %q, %v", instructions, err)
	}
	if err := os.Remove(filepath.Join(project, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(project, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := projectInstructions(project); err == nil {
		t.Fatal("loaded instructions from a symlink outside the project")
	}
}

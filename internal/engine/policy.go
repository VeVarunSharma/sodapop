package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
)

func canonicalDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("directory must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return resolved, nil
}

func withinDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && !filepath.IsAbs(relative) && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolveProjectRead(project, path string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.IndexByte(path, 0) >= 0 {
		return "", errors.New("read request has no valid path")
	}
	if strings.Contains(path, "://") || strings.HasPrefix(path, "~") {
		return "", errors.New("read request is not an explicit filesystem path")
	}
	// Do not Clean/Join before EvalSymlinks: link/../file follows the link
	// before traversing '..', which can differ from lexical path cleaning.
	if !filepath.IsAbs(path) {
		path = project + string(filepath.Separator) + path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve read path: %w", err)
	}
	currentProject, err := canonicalDirectory(project)
	if err != nil || currentProject != project {
		return "", errors.New("project directory changed since the session was opened")
	}
	if !withinDirectory(project, resolved) {
		return resolved, errors.New("read path is outside the project")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return resolved, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return resolved, errors.New("read target is not a regular file or directory")
	}
	return resolved, nil
}

func permissionDetails(project string, request copilot.PermissionRequest) (Permission, string, bool) {
	p := Permission{Kind: "unknown", Description: "Unrecognized permission request"}
	if nilPermission(request) {
		return p, "", false
	}
	p.Kind = string(request.Kind())
	toolID := ""
	auto := false
	var bypass *bool
	var bypassReason *string
	switch req := request.(type) {
	case *copilot.PermissionRequestRead:
		if req == nil {
			break
		}
		p.Path, p.Description = req.Path, req.Intention
		toolID = stringValue(req.ToolCallID)
		resolved, err := resolveProjectRead(project, req.Path)
		if resolved != "" && resolved != req.Path {
			p.Description += "\nResolved path: " + resolved
		}
		if err != nil {
			p.Description += "\n" + err.Error()
		}
		auto = err == nil && !request.RequiresManagedApproval() && !boolValue(req.RequestSandboxBypass)
		bypass, bypassReason = req.RequestSandboxBypass, req.RequestSandboxBypassReason
	case *copilot.PermissionRequestWrite:
		if req == nil {
			break
		}
		p.Path, p.Description = req.FileName, req.Intention
		toolID = stringValue(req.ToolCallID)
		if req.Diff != "" {
			p.Description += "\nProposed changes:\n" + req.Diff
		} else if req.NewFileContents != nil {
			p.Description += "\nNew file contents:\n" + *req.NewFileContents
		}
		bypass, bypassReason = req.RequestSandboxBypass, req.RequestSandboxBypassReason
	case *copilot.PermissionRequestShell:
		if req == nil {
			break
		}
		p.Command, p.Description = req.FullCommandText, req.Intention
		toolID = stringValue(req.ToolCallID)
		if req.Warning != nil {
			p.Description += "\n" + *req.Warning
		}
		bypass, bypassReason = req.RequestSandboxBypass, req.RequestSandboxBypassReason
	case *copilot.PermissionRequestURL:
		if req == nil {
			break
		}
		p.URL, p.Description = req.URL, req.Intention
		toolID = stringValue(req.ToolCallID)
		if req.RedirectedFrom != nil {
			p.Description += "\nRedirected from: " + *req.RedirectedFrom
		}
		bypass, bypassReason = req.RequestSandboxBypass, req.RequestSandboxBypassReason
	case *copilot.PermissionRequestMCP:
		if req == nil {
			break
		}
		toolID = stringValue(req.ToolCallID)
		p.Description = fmt.Sprintf("External MCP tool %s / %s\n%s", req.ServerName, req.ToolName, describeJSON(req.Args))
	case *copilot.PermissionRequestHook:
		if req == nil {
			break
		}
		toolID = stringValue(req.ToolCallID)
		p.Description = fmt.Sprintf("Tool %s requires approval\n%s", req.ToolName, describeJSON(req.ToolArgs))
		if req.HookMessage != nil {
			p.Description += "\n" + *req.HookMessage
		}
		if args, ok := req.ToolArgs.(map[string]any); ok {
			p.Path, _ = args["path"].(string)
			p.Command, _ = args["command"].(string)
			p.URL, _ = args["url"].(string)
		}
	default:
		p.Description = fmt.Sprintf("Permission request (%s)\n%s", request.Kind(), describeJSON(request))
	}
	if strings.TrimSpace(p.Description) == "" {
		p.Description = "Approval required for " + p.Kind
	}
	if boolValue(bypass) {
		p.Description += "\nWARNING: this action requests execution outside the runtime sandbox."
		if bypassReason != nil {
			p.Description += "\n" + *bypassReason
		}
	}
	// Typed-nil variants are malformed and must never be approved.
	if request.RequiresManagedApproval() {
		p.Description += "\nEnterprise policy requires an explicit human decision."
	}
	return p, toolID, auto
}

func describeJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "Details could not be encoded; do not approve an action you cannot identify."
	}
	return string(data)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolValue(value *bool) bool { return value != nil && *value }

// The pre-tool hook never grants permissions. It forces otherwise "safe"
// shell commands, writes, and unknown tools through the runtime's ask gate.
func preToolDecision(project string, input copilot.PreToolUseHookInput) *copilot.PreToolUseHookOutput {
	if input.ToolName == "ask_user" || input.ToolName == "task_complete" {
		return nil
	}
	if input.ToolName == "view" || input.ToolName == "glob" || input.ToolName == "grep" || input.ToolName == "rg" {
		args, ok := input.ToolArgs.(map[string]any)
		if ok {
			value, present := args["path"]
			path, explicit := value.(string)
			if !present && input.ToolName != "view" {
				path = input.WorkingDirectory
			}
			// Multiple path arguments are deliberately left to the ask gate.
			_, multiple := args["paths"]
			if _, err := resolveProjectRead(project, path); err == nil && !multiple && (!present || explicit) {
				return nil
			}
		}
	}
	return &copilot.PreToolUseHookOutput{
		PermissionDecision:       "ask",
		PermissionDecisionReason: "Sodapop requires an allow-once decision for this tool and its actual arguments.",
	}
}

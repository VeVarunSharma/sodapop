package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
)

const (
	fixtureName   = "fixture.go"
	fixtureBefore = "package fixture\n\nfunc Answer() int { return 1 }\n"
	fixtureAfter  = "package fixture\n\nfunc Answer() int { return 2 }\n"
	hookReason    = "Sodapop requires an allow-once decision for this tool and its actual arguments."
	livePrompt    = `Use only view and edit: view fixture.go, edit with {"path":"fixture.go","old_str":"return 1","new_str":"return 2"}, then view fixture.go again. Reply briefly. Do not run commands, access URLs, create files, use other tools, or ask questions. If denied or a tool fails, stop without retrying.`
)

type liveOptions struct{ clientID, model string }

func qualificationOptions(getenv func(string) string) (liveOptions, bool, error) {
	if getenv("SODAPOP_LIVE_QUALIFY") != "1" {
		return liveOptions{}, false, nil
	}
	options := liveOptions{
		clientID: strings.TrimSpace(getenv("SODAPOP_GITHUB_CLIENT_ID")),
		model:    strings.TrimSpace(getenv("SODAPOP_LIVE_MODEL")),
	}
	var missing []string
	if options.clientID == "" {
		missing = append(missing, "SODAPOP_GITHUB_CLIENT_ID")
	}
	if options.model == "" {
		missing = append(missing, "SODAPOP_LIVE_MODEL")
	}
	if len(missing) != 0 {
		return options, true, errors.New("live qualification requires nonempty " + strings.Join(missing, " and ") + "; no authentication or model request was attempted")
	}
	return options, true, nil
}

func selectedModel(models []engine.Model, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", errors.New("SODAPOP_LIVE_MODEL must select a model explicitly")
	}
	for _, model := range models {
		if model.ID != "" && model.ID == requested {
			return model.ID, nil
		}
	}
	var matches []string
	for _, model := range models {
		if model.ID != "" && strings.EqualFold(model.Name, requested) {
			matches = append(matches, model.ID)
		}
	}
	if len(matches) != 1 {
		return "", errors.New("SODAPOP_LIVE_MODEL must match an exact available model ID or a unique case-insensitive catalog display name; no substitute model was selected")
	}
	return matches[0], nil
}

type fixture struct{ project, home, path string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("could not canonicalize the disposable project")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("could not canonicalize the disposable runtime state")
	}
	f := fixture{project: project, home: home, path: filepath.Join(project, fixtureName)}
	if err := os.WriteFile(f.path, []byte(fixtureBefore), 0600); err != nil {
		t.Fatal("could not create the synthetic fixture")
	}
	return f
}

func (f fixture) exactPath(path string) bool {
	if path != fixtureName && path != "./"+fixtureName && path != f.path {
		return false
	}
	project, err := filepath.EvalSymlinks(f.project)
	if err != nil || project != f.project {
		return false
	}
	info, err := os.Lstat(f.path)
	return err == nil && info.Mode().IsRegular()
}

type fixtureOperation string

const (
	readFixture fixtureOperation = "view"
	editFixture fixtureOperation = "edit"
)

// Decode the whole object, rejecting duplicate keys as well as trailing JSON.
func argumentObject(raw string) (map[string]json.RawMessage, bool) {
	if len(raw) > 4096 {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	result := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || result[key] != nil {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		result[key] = value
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, false
	}
	var extra any
	return result, decoder.Decode(&extra) == io.EOF
}

func (f fixture) toolOperation(name, raw string) fixtureOperation {
	args, ok := argumentObject(raw)
	if !ok {
		return ""
	}
	var path string
	if json.Unmarshal(args["path"], &path) != nil || !f.exactPath(path) {
		return ""
	}
	switch name {
	case "view":
		if len(args) == 1 {
			return readFixture
		}
		var lines []int
		if len(args) == 2 && json.Unmarshal(args["view_range"], &lines) == nil &&
			len(lines) == 2 && lines[0] >= 1 && (lines[1] == -1 || lines[1] >= lines[0]) {
			return readFixture
		}
	case "edit":
		var old, new string
		if len(args) == 3 && json.Unmarshal(args["old_str"], &old) == nil &&
			json.Unmarshal(args["new_str"], &new) == nil && old == "return 1" && new == "return 2" {
			return editFixture
		}
	}
	return ""
}

func (f fixture) hookOperation(p *engine.Permission) fixtureOperation {
	header, body, ok := strings.Cut(p.Description, "\n")
	if !ok {
		return ""
	}
	raw, suffix, hasSuffix := strings.Cut(body, "\n")
	if hasSuffix && suffix != hookReason {
		return ""
	}
	var name string
	switch header {
	case "Tool view requires approval":
		name = "view"
	case "Tool edit requires approval":
		name = "edit"
	default:
		return ""
	}
	args, ok := argumentObject(raw)
	var path string
	if !ok || json.Unmarshal(args["path"], &path) != nil || path != p.Path {
		return ""
	}
	return f.toolOperation(name, raw)
}

type fixturePolicy struct {
	fixture
	tools                         map[string]fixtureOperation
	started, completed            map[string]bool
	editID, approvedEditID        string
	firstReadID, secondReadID     string
	readBefore, edited, readAfter bool
}

func newFixturePolicy(f fixture) *fixturePolicy {
	return &fixturePolicy{
		fixture: f, tools: make(map[string]fixtureOperation),
		started: make(map[string]bool), completed: make(map[string]bool),
	}
}

func (p *fixturePolicy) bind(id string, operation fixtureOperation) bool {
	if id == "" || operation == "" || (p.tools[id] != "" && p.tools[id] != operation) || p.completed[id] {
		return false
	}
	if operation == editFixture {
		if !p.readBefore || (p.editID != "" && p.editID != id) {
			return false
		}
		p.editID = id
	} else {
		readID := &p.firstReadID
		if p.edited {
			readID = &p.secondReadID
		}
		if *readID != "" && *readID != id {
			return false
		}
		*readID = id
	}
	p.tools[id] = operation
	return true
}

func (p *fixturePolicy) allow(event engine.Event) bool {
	request := event.Permission
	if request == nil || request.ID == "" || request.Respond == nil ||
		request.Command != "" || request.URL != "" || !p.exactPath(request.Path) {
		return false
	}
	var operation fixtureOperation
	switch request.Kind {
	case "hook":
		// Permission has no tool-name/args fields. This is the adapter's exact
		// hook envelope, not an intention-text or substring-based allowlist.
		operation = p.hookOperation(request)
	case "read", "write":
		if strings.Contains(request.Description, "\nWARNING:") ||
			strings.Contains(request.Description, "\nEnterprise policy requires an explicit human decision.") {
			return false
		}
		operation = p.tools[event.ToolID]
		if (request.Kind == "read" && operation != readFixture) ||
			(request.Kind == "write" && operation != editFixture) {
			return false
		}
	default:
		return false
	}
	return p.bind(event.ToolID, operation)
}

func (p *fixturePolicy) observeTool(event engine.Event) error {
	operation := p.toolOperation(event.Name, event.Arguments)
	if event.Kind == engine.EventToolStart {
		if p.started[event.ToolID] || !p.bind(event.ToolID, operation) {
			return errors.New("unexpected tool or arguments; only the exact fixture view/edit is qualified")
		}
		p.started[event.ToolID] = true
		return nil
	}
	if event.Failed || !p.started[event.ToolID] || p.completed[event.ToolID] ||
		operation == "" || p.tools[event.ToolID] != operation {
		return errors.New("fixture tool failed or its completion could not be correlated; no retry")
	}
	p.completed[event.ToolID] = true
	if operation == editFixture {
		if p.approvedEditID != event.ToolID {
			return errors.New("fixture edit completed without the harness's explicit approval")
		}
		p.edited = true
	} else if p.edited {
		p.readAfter = true
	} else {
		p.readBefore = true
	}
	return nil
}

func fixtureContents(f fixture, expected string) error {
	if !f.exactPath(f.path) {
		return errors.New("fixture is no longer an exact regular file")
	}
	entries, err := os.ReadDir(f.project)
	if err != nil || len(entries) != 1 || entries[0].Name() != fixtureName {
		return errors.New("the disposable project contains unexpected files or cannot be inspected")
	}
	content, err := os.ReadFile(f.path)
	if err != nil || string(content) != expected {
		return errors.New("fixture contents did not match the exact expected coding edit")
	}
	return nil
}

func contextFailure(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("qualification deadline exceeded; no prompt will be retried")
	}
	return errors.New("qualification canceled; no prompt will be retried")
}

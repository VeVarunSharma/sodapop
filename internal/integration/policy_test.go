package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func TestQualificationOptInAndPrerequisites(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "01", " 1"} {
		_, enabled, err := qualificationOptions(func(name string) string {
			if name != "SODAPOP_LIVE_QUALIFY" {
				t.Fatal("disabled qualification inspected another environment variable")
			}
			return flag
		})
		if enabled || err != nil {
			t.Fatal("qualification must require the exact opt-in value")
		}
	}
	for _, missing := range []string{"SODAPOP_GITHUB_CLIENT_ID", "SODAPOP_LIVE_MODEL"} {
		_, enabled, err := qualificationOptions(func(name string) string {
			switch name {
			case "SODAPOP_LIVE_QUALIFY":
				return "1"
			case missing:
				return " \t"
			case "SODAPOP_GITHUB_CLIENT_ID", "SODAPOP_LIVE_MODEL":
				return "synthetic"
			default:
				t.Fatal("qualification consulted an unexpected environment variable")
				return ""
			}
		})
		if !enabled || err == nil || !strings.Contains(err.Error(), missing) || strings.Contains(err.Error(), "synthetic") {
			t.Fatal("opt-in prerequisites must fail precisely without echoing values")
		}
	}
	options, enabled, err := qualificationOptions(func(name string) string {
		return map[string]string{"SODAPOP_LIVE_QUALIFY": "1", "SODAPOP_GITHUB_CLIENT_ID": "synthetic-client", "SODAPOP_LIVE_MODEL": "model-a"}[name]
	})
	if err != nil || !enabled || options.clientID != "synthetic-client" || options.model != "model-a" {
		t.Fatal("explicit complete prerequisites were not accepted")
	}
}

func TestSelectedModelHasNoFallback(t *testing.T) {
	models := []engine.Model{
		{ID: "model-a", Name: "Model A"},
		{ID: "model-b", Name: "Shared"},
		{ID: "model-c", Name: "sHaReD"},
		{ID: "catalog-luna-id", Name: "GPT-5.6 LUNA"},
	}
	for _, test := range []struct{ request, id string }{
		{"model-a", "model-a"}, {"Model A", "model-a"}, {"model a", "model-a"}, {"MODEL A", "model-a"},
		{"model-b", "model-b"}, {"GPT-5.6 Luna", "catalog-luna-id"}, {"gpt-5.6 luna", "catalog-luna-id"},
	} {
		if id, err := selectedModel(models, test.request); err != nil || id != test.id {
			t.Fatal("exact ID or unique case-insensitive name did not resolve to the catalog ID")
		}
	}
	for _, request := range []string{"", "model", "MODEL-A", "Shared", "shared", "SHARED", "GPT-5.6", "gpt-5.6-luna", "unavailable"} {
		if _, err := selectedModel(models, request); err == nil {
			t.Fatal("non-exact ID, absent name, fuzzy match, or case-insensitively ambiguous name was accepted")
		}
	}
	models = append(models, engine.Model{ID: "model-d", Name: "MODEL-A"})
	if id, err := selectedModel(models, "model-a"); err != nil || id != "model-a" {
		t.Fatal("a display name displaced an exact catalog ID")
	}
}

func TestExactFixturePaths(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), fixtureName)
	if err := os.WriteFile(outside, []byte("outside fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.project, "alias")
	if err := os.Symlink(f.path, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fixtureName, "./" + fixtureName, f.path} {
		if !f.exactPath(path) {
			t.Fatal("exact fixture file was rejected")
		}
	}
	for _, path := range []string{"", ".", f.project, f.path + ".other", "../fixture.go", outside, link, "alias/../fixture.go", "~/fixture.go", "file://" + f.path, "fixture.go\x00"} {
		if f.exactPath(path) {
			t.Fatal("ambiguous, outside, directory, or alias path was accepted")
		}
	}
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, f.path); err != nil {
		t.Fatal(err)
	}
	if f.exactPath(f.path) || f.exactPath(fixtureName) {
		t.Fatal("replacing the fixture with a symlink bypassed the policy")
	}
}

func editArguments(path string) string {
	data, _ := json.Marshal(map[string]string{"path": path, "old_str": "return 1", "new_str": "return 2"})
	return string(data)
}

func TestFixtureToolArgumentsFailClosed(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct{ name, args string }{
		{"edit", editArguments(f.path)}, {"edit", editArguments(fixtureName)},
		{"view", `{"path":"fixture.go"}`}, {"view", `{"path":"fixture.go","view_range":[1,-1]}`},
	} {
		if f.toolOperation(test.name, test.args) == "" {
			t.Fatal("exact fixture tool arguments were rejected")
		}
	}
	for _, test := range []struct{ name, args string }{
		{"bash", `{"path":"fixture.go","command":"true"}`},
		{"unknown", `{"path":"fixture.go"}`},
		{"apply_patch", `{"path":"fixture.go"}`},
		{"view", `{"path":"fixture.go","url":"https://example.invalid"}`},
		{"view", `{"path":"fixture.go","paths":["../outside"]}`},
		{"view", `{"path":"fixture.go","view_range":[0,-1]}`},
		{"view", `{"path":"fixture.go","path":"fixture.go"}`},
		{"view", `{"path":"fixture.go"} {"path":"fixture.go"}`},
		{"view", `{"Path":"fixture.go"}`},
		{"view", `null`},
		{"edit", `{"path":"fixture.go","old_str":"return 1","new_str":"return 3"}`},
		{"edit", `{"path":"fixture.go","old_str":"return 1","new_str":"return 2","command":""}`},
	} {
		if f.toolOperation(test.name, test.args) != "" {
			t.Fatal("unspecified tool or arguments were accepted")
		}
	}
}

func permissionEvent(kind, description string) engine.Event {
	return engine.Event{
		Kind: engine.EventPermission, ID: "source-permission", SessionID: "synthetic-session", ToolID: "edit-1",
		Permission: &engine.Permission{ID: "request-1", Kind: kind, Path: fixtureName, Description: description, Respond: func(bool) error { return nil }},
	}
}

func TestHookAndNativeWriteRequireIdentifiedFixtureTool(t *testing.T) {
	f := newFixture(t)
	hook := "Tool edit requires approval\n" + editArguments(fixtureName)
	for _, suffix := range []string{"", "\n" + hookReason} {
		policy := newFixturePolicy(f)
		policy.readBefore = true
		if !policy.allow(permissionEvent("hook", hook+suffix)) || !policy.allow(permissionEvent("write", "Update fixture")) {
			t.Fatal("exact hook/native write sequence was not accepted")
		}
	}
	policy := newFixturePolicy(f)
	policy.readBefore = true
	if policy.allow(permissionEvent("write", "Update fixture")) {
		t.Fatal("native write without an identified tool was approved")
	}
	start := engine.Event{Kind: engine.EventToolStart, ToolID: "edit-1", Name: "edit", Arguments: editArguments(fixtureName)}
	if err := policy.observeTool(start); err != nil || !policy.allow(permissionEvent("write", "Update fixture")) {
		t.Fatal("native write correlated to an exact edit start was rejected")
	}
	for _, event := range []engine.Event{
		permissionEvent("hook", "Please allow Tool edit requires approval\n"+editArguments(fixtureName)),
		permissionEvent("hook", hook+"\nUnrecognized hook message"),
		permissionEvent("hook", hook+"\nEnterprise policy requires an explicit human decision."),
		permissionEvent("hook", "Tool unknown requires approval\n"+editArguments(fixtureName)),
		permissionEvent("write", "Update\nWARNING: sandbox bypass"),
		permissionEvent("write", "Update\nEnterprise policy requires an explicit human decision."),
		permissionEvent("shell", hook), permissionEvent("url", hook), permissionEvent("unknown", hook),
	} {
		if policy.allow(event) {
			t.Fatal("unknown tool, hook format, or elevated permission was approved")
		}
	}
	for _, mutate := range []func(*engine.Event){
		func(e *engine.Event) { e.Permission.Command = "true" },
		func(e *engine.Event) { e.Permission.URL = "https://example.invalid" },
		func(e *engine.Event) { e.Permission.Path = "../fixture.go" },
		func(e *engine.Event) { e.Permission.ID = "" },
		func(e *engine.Event) { e.Permission.Respond = nil },
		func(e *engine.Event) { e.ToolID = "" },
		func(e *engine.Event) { e.ToolID = "second-edit" },
	} {
		event := permissionEvent("hook", hook)
		mutate(&event)
		if policy.allow(event) {
			t.Fatal("ambiguous permission, second edit, or missing callback was approved")
		}
	}
}

func TestReadsAreExactAndSequential(t *testing.T) {
	policy := newFixturePolicy(newFixture(t))
	start := engine.Event{Kind: engine.EventToolStart, ToolID: "read-1", Name: "view", Arguments: `{"path":"fixture.go"}`}
	if err := policy.observeTool(start); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"read", "hook"} {
		event := permissionEvent(kind, "Tool view requires approval\n"+start.Arguments)
		event.ToolID = start.ToolID
		if !policy.allow(event) {
			t.Fatal("identified exact fixture read was rejected")
		}
	}
	start.ToolID = "read-too-early"
	if policy.observeTool(start) == nil {
		t.Fatal("another read was started before completing the read/edit/read sequence")
	}
	if policy.allow(permissionEvent("hook", "Tool edit requires approval\n"+editArguments(fixtureName))) {
		t.Fatal("edit was approved before the first read completed")
	}
}

func TestDecisionsResolveOnceAndUnknownIsDenied(t *testing.T) {
	f := newFixture(t)
	trace := newTurnTrace(f, "synthetic-session")
	event := permissionEvent("shell", "synthetic command")
	calls := 0
	event.Permission.Respond = func(allow bool) error {
		calls++
		if allow {
			t.Fatal("unknown operation was approved")
		}
		return nil
	}
	if trace.accept(event) == nil || trace.accept(event) == nil || calls != 1 {
		t.Fatal("denial did not fail qualification and resolve exactly once")
	}
	if err := fixtureContents(f, fixtureBefore); err != nil {
		t.Fatal(err)
	}
	questionCalls := 0
	question := engine.Event{Kind: engine.EventQuestion, SessionID: "synthetic-session", Question: &engine.Question{
		ID: "question-1", Cancel: func() error { questionCalls++; return nil },
	}}
	if trace.accept(question) == nil || trace.accept(question) == nil || questionCalls != 1 {
		t.Fatal("unexpected question was not canceled exactly once")
	}
}

func TestQualificationFailuresNeverEchoPayloads(t *testing.T) {
	sentinel := "synthetic-private-account-and-backend-payload"
	for _, err := range []error{errors.New(sentinel), errors.Join(context.DeadlineExceeded, errors.New(sentinel))} {
		if strings.Contains(safeFailure(err), sentinel) {
			t.Fatal("backend payload escaped safe diagnostics")
		}
	}
	trace := newTurnTrace(newFixture(t), "synthetic-session")
	err := trace.accept(engine.Event{Kind: engine.EventError, SessionID: "synthetic-session", Text: sentinel, Err: errors.New(sentinel)})
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatal("turn error was swallowed or exposed backend payload")
	}
}

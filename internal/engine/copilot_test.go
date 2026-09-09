package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestStartUsesOnlyExplicitSodapopIdentityAndBundle(t *testing.T) {
	for key, value := range map[string]string{
		"COPILOT_CLI_PATH": "/ambient/copilot", "COPILOT_SDK_DEFAULT_CONNECTION": "inprocess",
		"COPILOT_HOME": "/ambient/state", "COPILOT_GITHUB_TOKEN": "ambient-copilot-token",
		"GH_TOKEN": "ambient-gh-token", "GITHUB_TOKEN": "ambient-github-token",
		"GH_HOST": "other.example", "NODE_OPTIONS": "--require surprise",
		"OTEL_EXPORTER_OTLP_ENDPOINT":  "https://example.invalid",
		"SODAPOP_GITHUB_CLIENT_ID":     "app-public-client",
		"SODAPOP_GITHUB_CLIENT_SECRET": "app-only-test-secret",
		"SODAPOP_LIVE_MODEL":           "only-app-control",
	} {
		t.Setenv(key, value)
	}
	client := newFakeClient()
	engine := testEngine(t, testConfig(t), client, nil)
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	options := client.options
	connection, ok := options.Connection.(copilot.StdioConnection)
	if !ok || !filepath.IsAbs(connection.Path) || filepath.Base(connection.Path) != "copilot-runtime" || len(connection.Args) != 0 {
		t.Fatalf("wrong transport: %#v", options.Connection)
	}
	if options.GitHubToken != "sodapop-private-test-token" || options.UseLoggedInUser == nil || *options.UseLoggedInUser ||
		options.BaseDirectory != engine.cfg.Home || options.WorkingDirectory != engine.cfg.Project ||
		options.Mode != copilot.ModeEmpty || options.EnableRemoteSessions || client.starts != 1 {
		t.Fatal("unsafe client configuration")
	}
	if options.ClientInfo == nil || options.ClientInfo.ApplicationName != "Sodapop" ||
		options.ClientInfo.IntegrationName != "Sodapop Go SDK" {
		t.Fatalf("incorrect client branding: %+v", options.ClientInfo)
	}
	for _, value := range options.Env {
		if strings.HasPrefix(value, "COPILOT_") || strings.HasPrefix(value, "GH_") || strings.HasPrefix(value, "GITHUB_") ||
			strings.HasPrefix(value, "SODAPOP_") || strings.HasPrefix(value, "NODE_OPTIONS=") ||
			strings.HasPrefix(value, "OTEL_") || strings.Contains(value, "ambient-") {
			t.Fatalf("inherited unsafe environment: %s", value)
		}
	}
}

func TestMissingTokenAndBundleCannotStartAClient(t *testing.T) {
	for _, name := range []string{"no token", "token source failed", "no bundle", "relative bundle"} {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(t)
			bundleCalls := 0
			if name == "no token" {
				cfg.TokenSource = func(context.Context) (string, error) { return "", nil }
			}
			if name == "token source failed" {
				cfg.TokenSource = func(context.Context) (string, error) {
					return "", errors.New("Sodapop OAuth client ID is not configured")
				}
			}
			client := newFakeClient()
			engine := testEngine(t, cfg, client, func(deps *dependencies) {
				deps.bundlePath = func() (string, error) {
					bundleCalls++
					if name == "relative bundle" {
						return "copilot", nil
					}
					return "", errors.New("bundle unavailable")
				}
			})
			err := engine.Start(t.Context())
			if err == nil || client.starts != 0 || client.options != nil {
				t.Fatalf("started without prerequisites: %v", err)
			}
			if (name == "no token" || name == "token source failed") && bundleCalls != 0 {
				t.Fatal("looked for a runtime before obtaining Sodapop identity")
			}
		})
	}
}

func TestNewValidatesIsolatedStorageAndIdentity(t *testing.T) {
	cfg := testConfig(t)
	cfg.TokenSource = nil
	if _, err := New(cfg); !errors.Is(err, ErrNoToken) {
		t.Fatalf("missing token source: %v", err)
	}
	cfg = testConfig(t)
	cfg.AccountID = " "
	if _, err := New(cfg); err == nil {
		t.Fatal("accepted missing account")
	}
	cfg = testConfig(t)
	cfg.Home = ""
	if _, err := New(cfg); err == nil {
		t.Fatal("accepted missing home")
	}
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	cfg = testConfig(t)
	cfg.Home = filepath.Join(userHome, ".copilot")
	if _, err := New(cfg); err == nil {
		t.Fatal("accepted default Copilot state")
	}
	if err := os.MkdirAll(cfg.Home, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(cfg.Home, alias); err != nil {
		t.Fatal(err)
	}
	cfg.Home = alias
	if _, err := New(cfg); err == nil {
		t.Fatal("accepted symlink to default Copilot state")
	}
}

func TestCreateSubscribesBeforeRPCAndDisablesDiscovery(t *testing.T) {
	client := newFakeClient()
	client.createEvent = &copilot.SessionEvent{ID: "early", Data: &copilot.AssistantMessageData{MessageID: "early-message", Content: "early event"}}
	engine := testEngine(t, testConfig(t), client, nil)
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	meta, err := engine.NewSession(t.Context(), ModelSelection{ModelID: "model-a", ContextTier: "default"})
	if err != nil {
		t.Fatal(err)
	}
	event := nextKind(t, engine, EventMessage)
	if event.ID != "early" || event.SessionID != meta.ID || event.History {
		t.Fatalf("lost early event: %+v", event)
	}
	config := client.created[0]
	for name, value := range map[string]*bool{
		"discovery": config.EnableConfigDiscovery, "file hooks": config.EnableFileHooks,
		"skills": config.EnableSkills, "session store": config.EnableSessionStore,
		"instruction discovery": config.EnableOnDemandInstructionDiscovery,
		"host git":              config.EnableHostGitOperations, "experiments": config.EnableExperimentalMode,
		"telemetry": config.EnableSessionTelemetry, "extensions": config.RequestExtensions,
		"schedules": config.ManageScheduleEnabled,
	} {
		if value == nil || *value {
			t.Fatalf("%s not explicitly disabled", name)
		}
	}
	if !boolValue(config.Streaming) || !boolValue(config.EnableManagedSettings) || config.GitHubToken == "" ||
		config.GitHubTokenProvider != nil || config.Memory == nil || config.Memory.Enabled ||
		config.OnPermissionRequest == nil || config.OnEvent == nil || config.OnUserInputRequest == nil || config.Hooks == nil ||
		len(config.PluginDirectories) != 0 || len(config.MCPServers) != 0 {
		t.Fatal("session configuration omitted a safety or callback requirement")
	}
}

func TestSessionConfigsPropagateContextTierAndReasoningEffort(t *testing.T) {
	live := &liveSession{meta: Session{
		ID: "sodapop-00000000000000000000000000000000", Project: t.TempDir(), Model: "model-a",
		ContextTier: "long_context", ReasoningEffort: "high",
	}}
	created := sessionConfig(live, t.TempDir(), "token", "instructions", nil, nil)
	if created.ContextTier != copilot.ContextTier(rpc.ContextTierLongContext) || created.ReasoningEffort != "high" {
		t.Fatalf("create selection = %q, %q", created.ContextTier, created.ReasoningEffort)
	}
	resumed := resumeConfig(created)
	if resumed.ContextTier != created.ContextTier || resumed.ReasoningEffort != created.ReasoningEffort {
		t.Fatalf("resume selection = %q, %q", resumed.ContextTier, resumed.ReasoningEffort)
	}
}

func TestSessionConfigsIncludeExplicitMCPServers(t *testing.T) {
	project := t.TempDir()
	live := &liveSession{meta: Session{
		ID: "sodapop-00000000000000000000000000000000", Project: project, Model: "model-a",
	}}
	created := sessionConfig(live, t.TempDir(), "token", "instructions", []MCPServer{{
		Name: "filesystem", Command: "server", Args: []string{"."},
		Env: map[string]string{"TOKEN": "value"}, Tools: []string{"read_file"}, TimeoutSeconds: 20,
	}}, nil)
	raw, ok := created.MCPServers["filesystem"]
	if !ok {
		t.Fatal("configured MCP server was omitted")
	}

	server, ok := raw.(copilot.MCPStdioServerConfig)
	if !ok || server.Command != "server" || server.WorkingDirectory != project ||
		len(server.Tools) != 1 || server.Tools[0] != "read_file" || server.Env["TOKEN"] != "value" {
		t.Fatalf("MCP server config = %#v", raw)
	}
	resumed := resumeConfig(created)
	if len(resumed.MCPServers) != 1 {
		t.Fatal("resume configuration omitted MCP servers")
	}
	defaults := sessionConfig(live, t.TempDir(), "token", "instructions", []MCPServer{{
		Name: "all-tools", Command: "server",
	}}, nil)
	raw = defaults.MCPServers["all-tools"]
	server, ok = raw.(copilot.MCPStdioServerConfig)
	if !ok || len(server.Tools) != 1 || server.Tools[0] != "*" || server.Env != nil {
		t.Fatalf("default MCP exposure = %#v", raw)
	}
	withSkills := sessionConfig(live, t.TempDir(), "token", "instructions", nil, []string{"/skills"})
	if !boolValue(withSkills.EnableSkills) || len(withSkills.SkillDirectories) != 1 {
		t.Fatalf("explicit skills were not preserved alongside MCP configuration: %#v", withSkills.SkillDirectories)
	}
}

func TestSessionConfigsIncludeOnlyExplicitSkills(t *testing.T) {
	parent := t.TempDir()
	skill := filepath.Join(parent, "review")
	if err := os.Mkdir(skill, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: review\ndescription: Review code\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	live := &liveSession{meta: Session{
		ID: "sodapop-00000000000000000000000000000000", Project: t.TempDir(), Model: "model-a",
	}}
	created := sessionConfig(live, t.TempDir(), "token", "instructions", nil, []string{parent})
	if !boolValue(created.EnableSkills) || len(created.SkillDirectories) != 1 ||
		len(created.IncludedBuiltinSkills) != 0 || len(created.PluginDirectories) != 0 {
		t.Fatalf("skill config = %#v", created)
	}
	resumed := resumeConfig(created)
	if !boolValue(resumed.EnableSkills) || len(resumed.SkillDirectories) != 1 ||
		len(resumed.DisabledSkills) != 0 {
		t.Fatalf("resume skill config = %#v", resumed)
	}
}

func TestEngineRejectsMalformedMCPServers(t *testing.T) {
	base := Config{
		Project: t.TempDir(), Home: t.TempDir(), AccountID: "account",
		TokenSource: func(context.Context) (string, error) { return "token", nil },
	}
	for _, server := range []MCPServer{
		{Name: "", Command: "server"},
		{Name: " server", Command: "server"},
		{Name: "server\n", Command: "server"},
		{Name: "server", Command: ""},
		{Name: "server", Command: "bad\ncommand"},
		{Name: "server", Command: "server", TimeoutSeconds: 301},
		{Name: "server", Command: "server", Env: map[string]string{"": "value"}},
	} {
		cfg := base
		cfg.MCPServers = []MCPServer{server}
		if _, err := newCopilot(cfg, dependencies{}); err == nil {
			t.Fatalf("accepted malformed MCP server: %#v", server)
		}
		if err := validateMCPServers([]MCPServer{
			{Name: "Server", Command: "one"},
			{Name: "server", Command: "two"},
		}); err == nil {
			t.Fatal("duplicate MCP server names were accepted")
		}
		if err := validateMCPServers([]MCPServer{{
			Name: "server", Command: "mcp-server", Args: []string{"."},
			Env: map[string]string{"TOKEN": "value"}, TimeoutSeconds: 300,
		}}); err != nil {
			t.Fatalf("valid MCP server rejected: %v", err)
		}
	}
}

func TestSkillConfigurationValidationAndResolution(t *testing.T) {
	parent := t.TempDir()
	name := "review"
	digest := strings.Repeat("a", 64)
	directory := filepath.Join(parent, name)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("skill"), 0600); err != nil {
		t.Fatal(err)
	}
	skill := Skill{Name: name, Digest: digest, Directory: parent}
	if err := validateSkills([]Skill{skill}, []string{digest}); err != nil {
		t.Fatal(err)
	}
	c := &Copilot{cfg: Config{Skills: []Skill{skill}}}
	directories, err := c.skillDirectories([]string{digest})
	if err != nil || len(directories) != 1 || directories[0] != parent {
		t.Fatalf("skill directories = %#v, %v", directories, err)
	}
	if _, err := c.skillDirectories([]string{strings.Repeat("b", 64)}); err == nil {
		t.Fatal("resolved an uninstalled skill digest")
	}
	if err := os.Remove(filepath.Join(directory, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.skillDirectories([]string{digest}); err == nil {
		t.Fatal("resolved a skill without a regular manifest")
	}
}

func TestSkillConfigurationRejectsMalformedEntries(t *testing.T) {
	parent := t.TempDir()
	digestA, digestB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	valid := Skill{Name: "review", Digest: digestA, Directory: parent}
	tests := []struct {
		name   string
		skills []Skill
		active []string
	}{
		{name: "blank name", skills: []Skill{{Name: "", Digest: digestA, Directory: parent}}},
		{name: "padded name", skills: []Skill{{Name: " review", Digest: digestA, Directory: parent}}},
		{name: "short digest", skills: []Skill{{Name: "review", Digest: "abc", Directory: parent}}},
		{name: "uppercase digest", skills: []Skill{{Name: "review", Digest: strings.Repeat("A", 64), Directory: parent}}},
		{name: "non hex digest", skills: []Skill{{Name: "review", Digest: strings.Repeat("z", 64), Directory: parent}}},
		{name: "relative directory", skills: []Skill{{Name: "review", Digest: digestA, Directory: "skills"}}},
		{name: "duplicate name", skills: []Skill{valid, {Name: "REVIEW", Digest: digestB, Directory: parent}}},
		{name: "duplicate digest", skills: []Skill{valid, {Name: "other", Digest: digestA, Directory: parent}}},
		{name: "missing active", skills: []Skill{valid}, active: []string{digestB}},
		{name: "duplicate active", skills: []Skill{valid}, active: []string{digestA, digestA}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSkills(test.skills, test.active); err == nil {
				t.Fatal("accepted malformed skill configuration")
			}
		})
	}
}

func TestInputAndModelValidationDoNotSendFallbacks(t *testing.T) {
	engine, _, session := startedEngine(t)
	for _, model := range []string{"", " ", "model-a\n", "unknown-model"} {
		if err := engine.SetModel(t.Context(), ModelSelection{ModelID: model, ContextTier: "default"}); err == nil {
			t.Fatalf("accepted model %q", model)
		}
		if _, err := engine.NewSession(t.Context(), ModelSelection{ModelID: model, ContextTier: "default"}); err == nil {
			t.Fatalf("created with model %q", model)
		}
	}
	for _, text := range []string{"", "\n \t", "hello\x00world"} {
		if err := engine.Send(t.Context(), Message{Text: text}); err == nil {
			t.Fatalf("accepted prompt %q", text)
		}
	}
	if len(session.sends) != 0 {
		t.Fatal("validation sent model work")
	}
	if err := engine.Send(t.Context(), Message{Text: "Design the change", Planning: true}); err != nil {
		t.Fatal(err)
	}
	sent := session.sends[0]
	if sent.AgentMode != copilot.AgentModeInteractive || sent.DisplayPrompt != "Design the change" ||
		!strings.Contains(sent.Prompt, "advisory") || !strings.Contains(sent.Prompt, "normal Sodapop approvals") {
		t.Fatalf("planning changed native behavior: %+v", sent)
	}
	if err := engine.Send(t.Context(), Message{Text: "second"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("accepted concurrent turn: %v", err)
	}
}

func TestModelSwitchPreservesSessionAndUpdatesIndex(t *testing.T) {
	engine, _, session := startedEngine(t)
	selection := ModelSelection{ModelID: "model-b", ContextTier: "default", ReasoningEffort: "high"}
	if err := engine.SetModel(t.Context(), selection); err != nil {
		t.Fatal(err)
	}
	current := engine.active.snapshot()
	saved, err := engine.index.find(t.Context(), session.id)
	if err != nil || current.ID != session.id || saved.Model != "model-b" ||
		saved.ContextTier != "default" || saved.ReasoningEffort != "high" ||
		session.model != "model-b" || session.reasoningEffort != "high" {
		t.Fatalf("model switch lost context/metadata: %+v, %v", saved, err)
	}
	if session.configures != 2 {
		t.Fatal("model switch did not recheck the effective tool surface")
	}
	session.setModelErr = errors.New("switch rejected")
	if err := engine.SetModel(t.Context(), ModelSelection{ModelID: "model-a", ContextTier: "default"}); err == nil ||
		engine.active.snapshot().Model != "model-b" {
		t.Fatal("silently substituted or persisted a rejected model")
	}
}

func TestCompactUsesManualRuntimeCompactionAndMapsMetrics(t *testing.T) {
	engine, _, session := startedEngine(t)
	session.compactResult = &rpc.HistoryCompactResult{
		Success: true, MessagesRemoved: 7, TokensRemoved: 1200,
		ContextWindow: &rpc.HistoryCompactContextWindow{
			CurrentTokens: 800, TokenLimit: 4000, MessagesLength: 5,
		},
	}

	result, err := engine.Compact(t.Context(), "preserve decisions\nand failures")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.compactions) != 1 ||
		session.compactions[0].instructions != "preserve decisions\nand failures" ||
		session.compactions[0].trigger != rpc.SessionHistoryCompactRequestTriggerManual {
		t.Fatalf("wrong compaction request: %+v", session.compactions)
	}
	if !result.Success || result.MessagesRemoved != 7 || result.TokensRemoved != 1200 ||
		result.CurrentTokens != 800 || result.TokenLimit != 4000 || result.MessagesRemaining != 5 {
		t.Fatalf("wrong compaction result: %+v", result)
	}
	if err := engine.Send(t.Context(), Message{Text: "continue"}); err != nil {
		t.Fatalf("session unusable after compaction: %v", err)
	}
}

func TestContextUsesAuthoritativeAttributionAndMapsDetails(t *testing.T) {
	engine, _, session := startedEngine(t)
	parent := "system-tools"
	session.contextResult = &rpc.MetadataContextAttributionResult{
		ContextAttribution: &rpc.SessionContextAttribution{
			ModelID: "model-a", ModelSource: "selected", TotalTokens: 15000,
			PromptTokenLimit: 128000, Limit: 134400, BufferTokens: 6400,
			CompactionThreshold: 121600,
			Categories: rpc.SessionContextAttributionCategories{
				SystemPrompt: 7100, CustomInstructions: 300, SystemTools: 8200,
				MCPTools: 0, Messages: 71, FreeSpace: 112329, Buffer: 6400,
			},
			Compactions: rpc.SessionContextAttributionCompactions{Count: 2},
			Entries: []rpc.SessionContextAttributionEntriesItem{
				{ID: "system-tools", Kind: "system", Label: "Tool definitions", Tokens: 8200},
				{ID: "tool:bash", Kind: "toolDefinition", Label: "bash", ParentID: &parent, Tokens: 1200},
			},
		},
	}
	session.heaviestResult = &rpc.MetadataContextHeaviestMessagesResult{
		TotalTokens: 15000,
		Messages:    []rpc.ContextHeaviestMessage{{ID: "message-1", Label: "tool: bash", Role: "tool", Tokens: 900}},
	}
	usage, err := engine.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if usage.Model != "model-a" || usage.TotalTokens != 15000 || usage.SystemPrompt.Tokens != 7100 ||
		usage.CustomInstructions.Tokens != 300 || usage.Compactions != 2 ||
		len(usage.Entries) != 2 || usage.Entries[1].ParentID != parent ||
		len(usage.HeaviestMessages) != 1 || usage.HeaviestMessages[0].Tokens != 900 ||
		session.heaviestLimit != 5 {
		t.Fatalf("context mapping lost metadata: %#v", usage)
	}

	session.heaviestErr = errors.New("details unavailable")
	usage, err = engine.Context(t.Context())
	if err != nil || len(usage.Warnings) != 1 || !strings.Contains(usage.Warnings[0], "details unavailable") {
		t.Fatalf("optional detail failure hid the primary grid: %#v, %v", usage, err)
	}
}

func TestContextFailsClosedForUnavailableOrInvalidData(t *testing.T) {
	engine, _, session := startedEngine(t)
	if _, err := engine.Context(t.Context()); !errors.Is(err, ErrContextUnavailable) {
		t.Fatalf("uninitialized context = %v", err)
	}
	session.contextResult = &rpc.MetadataContextAttributionResult{
		ContextAttribution: &rpc.SessionContextAttribution{
			ModelID: "model-a", Limit: 100, TotalTokens: 20,
			Categories: rpc.SessionContextAttributionCategories{Messages: -1},
		},
	}
	if _, err := engine.Context(t.Context()); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("invalid context accepted: %v", err)
	}
	engine.active.mu.Lock()
	engine.active.meta.Model = "hydrafusion"
	engine.active.mu.Unlock()
	if _, err := engine.Context(t.Context()); !errors.Is(err, ErrVariableContext) {
		t.Fatalf("variable context model accepted: %v", err)
	}
}

func TestCompactRejectsInvalidMissingBusyAndFailedRequests(t *testing.T) {
	engine, _, session := startedEngine(t)
	if _, err := engine.Compact(t.Context(), "bad\x00focus"); err == nil {
		t.Fatal("accepted NUL compaction instructions")
	}
	if len(session.compactions) != 0 {
		t.Fatal("invalid compaction reached runtime")
	}
	if err := engine.Send(t.Context(), Message{Text: "active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Compact(t.Context(), ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("accepted compaction during active turn: %v", err)
	}
	session.emit(copilot.SessionEvent{ID: "idle-compact-test", Data: &copilot.SessionIdleData{}})
	session.compactResult = &rpc.HistoryCompactResult{Success: false}
	if _, err := engine.Compact(t.Context(), ""); err == nil {
		t.Fatal("reported failed runtime compaction as success")
	}
}

func TestNewPreservesSessionAndResumeRestoresOnlyHistory(t *testing.T) {
	engine, client, first := startedEngine(t)
	if err := engine.Send(t.Context(), Message{Text: "first prompt"}); err != nil {
		t.Fatal(err)
	}
	first.emit(copilot.SessionEvent{ID: "delta", Data: &copilot.AssistantMessageDeltaData{MessageID: "assistant", DeltaContent: "answer"}})
	first.emit(copilot.SessionEvent{ID: "final", Data: &copilot.AssistantMessageData{MessageID: "assistant", Content: "answer"}})
	first.emit(copilot.SessionEvent{ID: "idle", Data: &copilot.SessionIdleData{}})
	if _, err := engine.NewSession(t.Context(), ModelSelection{
		ModelID: "model-b", ContextTier: "default", ReasoningEffort: "medium",
	}); err != nil {
		t.Fatal(err)
	}
	if first.disconnects != 1 {
		t.Fatal("new did not disconnect the prior session")
	}
	meta, err := engine.ResumeSession(t.Context(), first.id)
	if err != nil || meta.ID != first.id {
		t.Fatalf("resume: %+v, %v", meta, err)
	}
	for _, role := range []string{"user", "assistant"} {
		event := receive(t, engine.Events())
		if event.Kind != EventMessage || !event.History || event.Role != role || event.SessionID != first.id {
			t.Fatalf("replayed action/delta or lost history: %+v", event)
		}
	}
	config := client.resumed[0]
	if config.ContinuePendingWork == nil || *config.ContinuePendingWork || config.OnEvent == nil ||
		config.OnPermissionRequest == nil || config.OnUserInputRequest == nil || config.Hooks == nil ||
		config.GitHubToken == "" || config.GitHubTokenProvider != nil || !boolValue(config.Streaming) ||
		config.EnableSkills == nil || *config.EnableSkills || config.EnableFileHooks == nil || *config.EnableFileHooks {
		t.Fatal("resume did not reapply auth and safety handlers")
	}
	if sessions, err := engine.Sessions(t.Context()); err != nil || len(sessions) != 2 {
		t.Fatalf("new deleted a record: %+v, %v", sessions, err)
	}
}

func TestResumeRejectsForeignScopeBeforeRuntime(t *testing.T) {
	engine, client, session := startedEngine(t)
	cfg := engine.cfg
	cfg.AccountID = "account-two"
	other := testEngine(t, cfg, client, nil)
	if err := other.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := other.ResumeSession(t.Context(), session.id); err == nil {
		t.Fatal("resumed another account's history")
	}
	if len(client.resumed) != 0 {
		t.Fatal("contacted runtime for a foreign session")
	}
}

func TestFailedSendIsNotReplayed(t *testing.T) {
	engine, _, session := startedEngine(t)
	session.sendHook = func(context.Context) error { return errors.New("connection interrupted") }
	if err := engine.Send(t.Context(), Message{Text: "change files"}); err == nil {
		t.Fatal("swallowed send failure")
	}
	if err := engine.Send(t.Context(), Message{Text: "change files"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("allowed retry without explicit cancellation: %v", err)
	}
	if len(session.sends) != 1 {
		t.Fatal("automatically replayed prompt")
	}
	if err := engine.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	session.sendHook = nil
	if err := engine.Send(t.Context(), Message{Text: "a deliberate new prompt"}); err != nil {
		t.Fatal(err)
	}
}

func TestAbortCancelsAnInflightSendWithoutDeadlock(t *testing.T) {
	engine, _, session := startedEngine(t)
	entered := make(chan struct{})
	sendFinished := make(chan struct{})
	session.sendHook = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(sendFinished)
		return ctx.Err()
	}
	session.abortHook = func(context.Context) error {
		select {
		case <-sendFinished:
			return nil
		default:
			t.Error("abort frame raced ahead of the canceled send frame")
			return nil
		}
	}
	done := make(chan error, 1)
	go func() { done <- engine.Send(t.Context(), Message{Text: "work"}) }()
	awaitSignal(t, entered)
	if err := engine.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("send was not canceled: %v", err)
	}
}

func TestTokenRefreshUsesExplicitCredentialsWithoutInventingExpiry(t *testing.T) {
	cfg := testConfig(t)
	token := "first-private-token"
	cfg.TokenSource = func(context.Context) (string, error) { return token, nil }
	client := newFakeClient()
	engine := testEngine(t, cfg, client, nil)
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	meta, err := engine.NewSession(t.Context(), ModelSelection{ModelID: "model-a", ContextTier: "default"})
	if err != nil {
		t.Fatal(err)
	}
	token = "second-private-token"
	if err := engine.Send(t.Context(), Message{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	session := client.session(t, meta.ID)
	if len(session.tokens) != 1 || session.tokens[0] != token || client.created[0].GitHubTokenProvider != nil {
		t.Fatal("did not install the refreshed explicit token")
	}
}

func TestCloseIsConcurrentIdempotentAndErrorAware(t *testing.T) {
	engine, client, _ := startedEngine(t)
	client.stopErr = errors.New("failed shutdown sodapop-private-test-token")
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			err := engine.Close()
			if err == nil || strings.Contains(err.Error(), "sodapop-private-test-token") {
				t.Errorf("close error missing or unredacted: %v", err)
			}
		})
	}
	workers.Wait()
	if client.stops != 1 {
		t.Fatalf("stopped SDK %d times", client.stops)
	}
	if _, open := <-engine.Events(); open {
		t.Fatal("event channel was not closed by its owner")
	}
	if err := engine.Send(t.Context(), Message{Text: "after close"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("send after close: %v", err)
	}
}

func TestRuntimeDisconnectIsReportedWithoutRestartOrReplay(t *testing.T) {
	client := newFakeClient()
	engine := testEngine(t, testConfig(t), client, func(deps *dependencies) { deps.healthEvery = time.Millisecond })
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	client.health = func(context.Context) error { return errors.New("process exited sodapop-private-test-token") }
	client.mu.Unlock()
	event := nextKind(t, engine, EventError)
	if event.Err == nil || strings.Contains(event.Err.Error(), "sodapop-private-test-token") {
		t.Fatalf("bad runtime error: %+v", event)
	}
	if _, open := <-engine.Events(); open {
		t.Fatal("stream did not finish after the transport error")
	}
	if err := engine.Start(t.Context()); err == nil {
		t.Fatal("silently restarted the runtime")
	}
	if client.starts != 1 {
		t.Fatal("runtime was restarted")
	}
}

package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/commands"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/skills"
	"github.com/VeVarunSharma/sodapop/internal/ui"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
)

func TestInformationFlagsDoNotNeedTerminalOrIdentity(t *testing.T) {
	t.Setenv("SODAPOP_GITHUB_CLIENT_ID", "")
	for _, arg := range []string{"--help", "-h", "--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(context.Background(), []string{arg}, nil, &output); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.ToLower(output.String()), "sodapop") {
				t.Fatalf("unexpected information output: %q", output.String())
			}
			if arg == "--help" || arg == "-h" {
				for _, text := range []string{"Sodapop -", "Usage: sodapop [options]", "SODAPOP_GITHUB_CLIENT_ID"} {
					if !strings.Contains(output.String(), text) {
						t.Fatalf("help missing %q: %q", text, output.String())
					}
				}
			} else if !strings.HasPrefix(output.String(), "sodapop "+Version+"\n") {
				t.Fatalf("version did not identify the executable: %q", output.String())
			}
			if arg == "--help" || arg == "-h" {
				for _, command := range commands.All() {
					if !strings.Contains(output.String(), "/"+command.Name) {
						t.Fatalf("help omitted /%s: %q", command.Name, output.String())
					}
				}
				if strings.Contains(output.String(), "/new") {
					t.Fatalf("help advertised a retired command: %q", output.String())
				}
			}
		})
	}
}

func TestRejectsNonInteractiveLaunch(t *testing.T) {
	var output bytes.Buffer
	err := Run(context.Background(), nil, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("expected useful non-TTY error, got %v", err)
	}
}

func TestNoBannerIsARunLocalPresentationFlag(t *testing.T) {
	for _, args := range [][]string{{"--no-banner"}, {"--no-banner", "--ascii", "--reduced-motion"}} {
		var output bytes.Buffer
		err := Run(context.Background(), args, nil, &output)
		if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
			t.Fatalf("banner option bypassed the terminal requirement or was not recognized: %v", err)
		}
	}
	var output bytes.Buffer
	if err := Run(context.Background(), []string{"--no-banner", "--help"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "-no-banner") {
		t.Fatal("help did not advertise the banner opt-out")
	}
	for _, disabled := range []bool{false, true} {
		deps, state := testRunDependencies(t)
		var args []string
		if disabled {
			args = []string{"--no-banner"}
		}
		if err := run(context.Background(), args, state.input, state.output, deps); err != nil {
			t.Fatal(err)
		}
		if state.options.NoBanner != disabled {
			t.Fatal("application did not forward the run-local banner choice to the UI")
		}
	}
}

func TestRejectsUnknownOptionsAndPositionalArguments(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {"some-prompt"}} {
		var output bytes.Buffer
		if err := Run(context.Background(), args, nil, &output); err == nil {
			t.Fatalf("accepted unsupported input: %v", args)
		}
	}
}

func TestRuntimeCheckUsesBoundedContextAndReportsResult(t *testing.T) {
	deps := defaultRunDependencies()
	deps.checkRuntime = func(ctx context.Context) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 46*time.Second {
			t.Fatal("runtime check did not receive a bounded context")
		}
		return "Copilot v1 (protocol 2)", nil
	}
	var output bytes.Buffer
	if err := run(context.Background(), []string{"--check-runtime"}, nil, &output, deps); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != "Copilot v1 (protocol 2)" {
		t.Fatalf("unexpected runtime status: %q", output.String())
	}

	want := errors.New("runtime unavailable")
	deps.checkRuntime = func(context.Context) (string, error) { return "", want }
	if err := run(context.Background(), []string{"--check-runtime"}, nil, io.Discard, deps); !errors.Is(err, want) {
		t.Fatalf("runtime error = %v", err)
	}
}

func TestInteractiveRunWiresOptionsAndDoesNotPersistTemporaryOverrides(t *testing.T) {
	deps, state := testRunDependencies(t)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	t.Setenv("SODAPOP_GITHUB_CLIENT_ID", " environment-client ")
	previous := OAuthClientID
	OAuthClientID = "linked-client"
	t.Cleanup(func() { OAuthClientID = previous })

	if err := run(context.Background(), []string{"--reduced-motion"}, state.input, state.output, deps); err != nil {
		t.Fatal(err)
	}
	if state.authClientID != "environment-client" {
		t.Fatalf("client ID = %q", state.authClientID)
	}
	if state.options.Project != state.project || state.options.Version != Version ||
		!state.options.Preferences.NoColor || !state.options.Preferences.ReducedMotion || !state.options.Preferences.ASCII {
		t.Fatalf("unexpected UI options: %+v", state.options)
	}
	next := config.DefaultPreferences()
	next.Theme = "other"
	next.NoColor = true
	next.ReducedMotion = true
	next.ASCII = true
	if err := state.options.SavePreferences(next); err != nil {
		t.Fatal(err)
	}
	if state.saved.NoColor || state.saved.ReducedMotion || state.saved.ASCII || state.saved.Theme != "other" {
		t.Fatalf("temporary overrides were persisted: %+v", state.saved)
	}
}

func TestInteractiveRunBuildsAccountBoundEngine(t *testing.T) {
	deps, state := testRunDependencies(t)
	t.Setenv("MCP_TOKEN", "secret-value")
	state.mcpRegistry = config.MCPRegistry{Version: 1, Servers: []config.MCPServer{
		{Name: "filesystem", Command: "mcp-server", Args: []string{"."}, Env: []string{"MCP_TOKEN"}, Enabled: true},
		{Name: "disabled", Command: "disabled-server", Enabled: false},
	}}
	backend := newTestEngine()
	deps.newEngine = func(cfg engine.Config) (engine.Engine, error) {
		state.engineConfig = cfg
		return backend, nil
	}
	if err := run(context.Background(), nil, state.input, state.output, deps); err != nil {
		t.Fatal(err)
	}
	skillRoot := t.TempDir()
	manifest := "---\nname: app-skill\ndescription: App wiring fixture\n---\n"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []skills.Action{
		{Operation: "trust", Value: skillRoot},
		{Operation: "install", Value: skillRoot},
		{Operation: "enable", Value: "app-skill"},
	} {
		if _, err := state.options.ManageSkills(context.Background(), action); err != nil {
			t.Fatal(err)
		}
	}
	got, err := state.options.NewEngine(context.Background(), auth.Account{ID: "account-a"})
	if err != nil || got != backend || backend.starts != 1 {
		t.Fatalf("engine result = %#v, starts=%d, err=%v", got, backend.starts, err)
	}
	if state.engineConfig.AccountID != "account-a" || state.engineConfig.Project != state.project ||
		filepath.Dir(state.engineConfig.Home) != filepath.Join(state.paths.StateDir, "copilot") ||
		strings.Contains(state.engineConfig.Home, "account-a") {
		t.Fatalf("engine config = %+v", state.engineConfig)
	}
	if len(state.engineConfig.MCPServers) != 1 || state.engineConfig.MCPServers[0].Name != "filesystem" ||
		state.engineConfig.MCPServers[0].Env["MCP_TOKEN"] != "secret-value" {
		t.Fatalf("MCP engine config = %+v", state.engineConfig.MCPServers)
	}
	if len(state.engineConfig.ActiveSkillDigests) != 1 {
		t.Fatalf("active skills = %+v", state.engineConfig.ActiveSkillDigests)
	}
	foundSkill := false
	for _, configured := range state.engineConfig.Skills {
		if configured.Name == "app-skill" && configured.Digest == state.engineConfig.ActiveSkillDigests[0] {
			foundSkill = true
		}
	}
	if !foundSkill {
		t.Fatalf("skill engine config = %+v", state.engineConfig.Skills)
	}
	token, err := state.engineConfig.TokenSource(context.Background())
	if err != nil || token != "token-for-account-a" || state.authTokenAccount != "account-a" {
		t.Fatalf("bound token = %q, account=%q, err=%v", token, state.authTokenAccount, err)
	}
}

func TestResolveMCPServersRequiresReferencedEnvironment(t *testing.T) {
	registry := config.MCPRegistry{Version: 1, Servers: []config.MCPServer{{
		Name: "server", Command: "mcp-server", Env: []string{"SODAPOP_TEST_MISSING_MCP_ENV"}, Enabled: true,
	}}}
	t.Setenv("SODAPOP_TEST_MISSING_MCP_ENV", "")
	if _, err := resolveMCPServers(registry); err != nil {
		t.Fatalf("present empty environment value was rejected: %v", err)
	}
	if err := os.Unsetenv("SODAPOP_TEST_MISSING_MCP_ENV"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveMCPServers(registry); err == nil {
		t.Fatal("missing referenced environment variable was accepted")
	}
}

func TestInteractiveRunClosesEngineWhenStartFails(t *testing.T) {
	deps, state := testRunDependencies(t)
	startErr, closeErr := errors.New("start failed"), errors.New("close failed")
	backend := newTestEngine()
	backend.startErr, backend.closeErr = startErr, closeErr
	deps.newEngine = func(engine.Config) (engine.Engine, error) { return backend, nil }
	if err := run(context.Background(), nil, state.input, state.output, deps); err != nil {
		t.Fatal(err)
	}
	_, err := state.options.NewEngine(context.Background(), auth.Account{ID: "account-a"})
	if !errors.Is(err, startErr) || !errors.Is(err, closeErr) || backend.closes != 1 {
		t.Fatalf("start cleanup = %v, closes=%d", err, backend.closes)
	}
}

func TestInteractiveRunReportsSetupFailures(t *testing.T) {
	stages := []struct {
		name string
		fail func(*runDependencies, error)
		want string
	}{
		{"paths", func(d *runDependencies, err error) {
			d.resolvePaths = func() (config.Paths, error) { return config.Paths{}, err }
		}, "paths"},
		{"preferences", func(d *runDependencies, err error) {
			d.loadPreferences = func(string) (config.Preferences, error) { return config.Preferences{}, err }
		}, "config.json"},
		{"cwd", func(d *runDependencies, err error) { d.getwd = func() (string, error) { return "", err } }, "locate current project"},
		{"symlink", func(d *runDependencies, err error) { d.evalSymlinks = func(string) (string, error) { return "", err } }, "resolve current project"},
		{"workspace", func(d *runDependencies, err error) {
			d.newWorkspace = func(string) (ui.Workspace, error) { return nil, err }
		}, "workspace"},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			deps, state := testRunDependencies(t)
			failure := errors.New(stage.want)
			stage.fail(&deps, failure)
			if err := run(context.Background(), nil, state.input, state.output, deps); err == nil ||
				!strings.Contains(err.Error(), stage.want) {
				t.Fatalf("setup error = %v", err)
			}
		})
	}
}

func TestInteractiveRunJoinsProgramAndShutdownErrors(t *testing.T) {
	deps, state := testRunDependencies(t)
	programErr, shutdownErr := errors.New("program failed"), errors.New("shutdown failed")
	state.model.shutdownErr = shutdownErr
	deps.runProgram = func(context.Context, applicationModel, *os.File, io.Writer) error { return programErr }
	err := run(context.Background(), nil, state.input, state.output, deps)
	if !errors.Is(err, programErr) || !errors.Is(err, shutdownErr) || state.model.shutdowns != 1 {
		t.Fatalf("lifecycle error = %v, shutdowns=%d", err, state.model.shutdowns)
	}
}

func TestCanceledProgramIsSuccessfulButStillShutsDown(t *testing.T) {
	deps, state := testRunDependencies(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deps.runProgram = func(context.Context, applicationModel, *os.File, io.Writer) error {
		return context.Canceled
	}
	if err := run(ctx, nil, state.input, state.output, deps); err != nil || state.model.shutdowns != 1 {
		t.Fatalf("canceled run = %v, shutdowns=%d", err, state.model.shutdowns)
	}
}

type runTestState struct {
	input, output    *os.File
	options          ui.Options
	model            *testApplicationModel
	authClientID     string
	authTokenAccount string
	saved            config.Preferences
	mcpRegistry      config.MCPRegistry
	engineConfig     engine.Config
	paths            config.Paths
	project          string
}

func testRunDependencies(t *testing.T) (runDependencies, *runTestState) {
	t.Helper()
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		_ = output.Close()
	})
	state := &runTestState{input: input, output: output, model: &testApplicationModel{}}
	paths := config.Paths{ConfigFile: filepath.Join(t.TempDir(), "config.json"), StateDir: filepath.Join(t.TempDir(), "state")}
	state.paths = paths
	project, canonicalProject := t.TempDir(), t.TempDir()
	state.project = canonicalProject
	deps := defaultRunDependencies()
	deps.isTerminal = func(uintptr) bool { return true }
	deps.resolvePaths = func() (config.Paths, error) { return paths, nil }
	deps.loadPreferences = func(string) (config.Preferences, error) { return config.DefaultPreferences(), nil }
	deps.savePreferences = func(_ string, prefs config.Preferences) error {
		state.saved = prefs
		return nil
	}
	state.mcpRegistry = config.DefaultMCPRegistry()
	deps.loadMCP = func(string) (config.MCPRegistry, error) { return state.mcpRegistry, nil }
	deps.saveMCP = func(_ string, registry config.MCPRegistry) error {
		state.mcpRegistry = registry
		return nil
	}
	deps.getwd = func() (string, error) { return project, nil }
	deps.evalSymlinks = func(string) (string, error) { return canonicalProject, nil }
	deps.newWorkspace = func(string) (ui.Workspace, error) { return testWorkspace{}, nil }
	deps.newAuth = func(clientID string) applicationAuth {
		state.authClientID = clientID
		return &testAuth{tokenAccount: &state.authTokenAccount}
	}
	deps.newModel = func(options ui.Options) applicationModel {
		state.options = options
		return state.model
	}
	deps.runProgram = func(context.Context, applicationModel, *os.File, io.Writer) error { return nil }
	return deps, state
}

type testApplicationModel struct {
	shutdownErr error
	shutdowns   int
}

func (*testApplicationModel) Init() tea.Cmd                         { return nil }
func (m *testApplicationModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (*testApplicationModel) View() tea.View                        { return tea.NewView("") }
func (m *testApplicationModel) Shutdown() error {
	m.shutdowns++
	return m.shutdownErr
}

type testAuth struct{ tokenAccount *string }

func (*testAuth) Current(context.Context) (auth.Account, error) { return auth.Account{}, nil }
func (*testAuth) Token(context.Context) (string, error)         { return "", nil }
func (*testAuth) Login(context.Context, bool, func(auth.DeviceCode)) (auth.Account, error) {
	return auth.Account{}, nil
}
func (*testAuth) SignOut(context.Context) error { return nil }
func (a *testAuth) TokenForAccount(_ context.Context, account string) (string, error) {
	*a.tokenAccount = account
	return "token-for-" + account, nil
}

type testWorkspace struct{}

func (testWorkspace) Status(context.Context) (workspace.Status, error) {
	return workspace.Status{}, nil
}
func (testWorkspace) Diff(context.Context, string) (workspace.Diff, error) {
	return workspace.Diff{}, nil
}

func (testWorkspace) CaptureBaseline(context.Context) (workspace.Baseline, error) {
	return nil, nil
}

type testEngine struct {
	events             chan engine.Event
	startErr, closeErr error
	starts, closes     int
}

func newTestEngine() *testEngine { return &testEngine{events: make(chan engine.Event)} }
func (e *testEngine) Start(context.Context) error {
	e.starts++
	return e.startErr
}
func (*testEngine) Models(context.Context) ([]engine.Model, error) { return nil, nil }
func (*testEngine) NewSession(context.Context, engine.ModelSelection) (engine.Session, error) {
	return engine.Session{}, nil
}
func (*testEngine) ResumeSession(context.Context, string) (engine.Session, error) {
	return engine.Session{}, nil
}
func (*testEngine) Sessions(context.Context) ([]engine.Session, error) { return nil, nil }
func (*testEngine) Send(context.Context, engine.Message) error         { return nil }
func (*testEngine) Compact(context.Context, string) (engine.CompactResult, error) {
	return engine.CompactResult{}, nil
}
func (*testEngine) Context(context.Context) (engine.ContextUsage, error) {
	return engine.ContextUsage{}, engine.ErrContextUnavailable
}
func (*testEngine) SetModel(context.Context, engine.ModelSelection) error { return nil }
func (*testEngine) Abort(context.Context) error                           { return nil }
func (e *testEngine) Events() <-chan engine.Event                         { return e.events }
func (e *testEngine) Close() error {
	e.closes++
	return e.closeErr
}

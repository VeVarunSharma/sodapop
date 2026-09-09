package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
)

type fakeAuth struct {
	currentCalls atomic.Int32
	loginCalls   atomic.Int32
	tokenCalls   atomic.Int32
	signOutCalls atomic.Int32
	account      auth.Account
	currentErr   error
	loginErr     error
	signOutErr   error
	signOutFn    func(context.Context) error
	loginFn      func(context.Context, bool, func(auth.DeviceCode)) (auth.Account, error)
}

func (f *fakeAuth) Current(context.Context) (auth.Account, error) {
	f.currentCalls.Add(1)
	return f.account, f.currentErr
}

func (f *fakeAuth) Token(context.Context) (string, error) {
	f.tokenCalls.Add(1)
	return "", errors.New("UI must not obtain a token")
}

func (f *fakeAuth) Login(ctx context.Context, sessionOnly bool, code func(auth.DeviceCode)) (auth.Account, error) {
	f.loginCalls.Add(1)
	if f.loginFn != nil {
		return f.loginFn(ctx, sessionOnly, code)
	}
	return f.account, f.loginErr
}

func (f *fakeAuth) SignOut(ctx context.Context) error {
	f.signOutCalls.Add(1)
	if f.signOutFn != nil {
		return f.signOutFn(ctx)
	}
	return f.signOutErr
}

type fakeEngine struct {
	mu            sync.Mutex
	events        chan engine.Event
	models        []engine.Model
	sessions      []engine.Session
	sent          []engine.Message
	newModels     []engine.ModelSelection
	resumed       []string
	setModels     []engine.ModelSelection
	compactions   []string
	starts        int
	closes        int
	aborts        int
	modelsCalls   int
	sendsErr      error
	modelsErr     error
	newErr        error
	resumeErr     error
	setModelErr   error
	compactErr    error
	compactResult engine.CompactResult
	contextResult engine.ContextUsage
	contextErr    error
	contextCalls  int
	abortErr      error
	closeErr      error
	closeFn       func() error
	newFn         func(context.Context, string) (engine.Session, error)
	resumeFn      func(context.Context, string) (engine.Session, error)
	sendFn        func(context.Context, engine.Message) error
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		events: make(chan engine.Event, 64),
		models: []engine.Model{
			{
				ID: "model-a", Name: "GPT-5.6 Luna", Family: "OpenAI",
				ContextOptions: []engine.ContextOption{
					{Tier: "default", Tokens: 128000},
					{Tier: "long_context", Tokens: 1000000},
				},
				ReasoningEfforts:   []string{"low", "medium", "high"},
				DefaultContextTier: "default", DefaultReasoningEffort: "medium",
			},
			{
				ID: "model-b", Name: "Claude Sonnet 5", Family: "Anthropic",
				ContextOptions:     []engine.ContextOption{{Tier: "default", Tokens: 200000}},
				DefaultContextTier: "default",
			},
		},
	}
}

func (f *fakeEngine) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return nil
}

func (f *fakeEngine) Models(context.Context) ([]engine.Model, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelsCalls++
	return append([]engine.Model(nil), f.models...), f.modelsErr
}

func (f *fakeEngine) NewSession(ctx context.Context, selection engine.ModelSelection) (engine.Session, error) {
	f.mu.Lock()
	f.newModels = append(f.newModels, selection)
	fn, err := f.newFn, f.newErr
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, selection.ModelID)
	}
	return engine.Session{
		ID: "new-session", Project: "/project", Model: selection.ModelID,
		ContextTier: selection.ContextTier, ReasoningEffort: selection.ReasoningEffort,
	}, err
}

func (f *fakeEngine) ResumeSession(ctx context.Context, id string) (engine.Session, error) {
	f.mu.Lock()
	f.resumed = append(f.resumed, id)
	fn, err := f.resumeFn, f.resumeErr
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return engine.Session{ID: id, Project: "/project", Model: "model-b", Title: "Restored work"}, err
}

func (f *fakeEngine) Sessions(context.Context) ([]engine.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]engine.Session(nil), f.sessions...), nil
}

func (f *fakeEngine) Send(ctx context.Context, message engine.Message) error {
	f.mu.Lock()
	f.sent = append(f.sent, message)
	fn, err := f.sendFn, f.sendsErr
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, message)
	}
	return err
}

func (f *fakeEngine) SetModel(_ context.Context, selection engine.ModelSelection) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setModels = append(f.setModels, selection)
	return f.setModelErr
}

func (f *fakeEngine) Compact(_ context.Context, instructions string) (engine.CompactResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compactions = append(f.compactions, instructions)
	return f.compactResult, f.compactErr
}

func (f *fakeEngine) Context(context.Context) (engine.ContextUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contextCalls++
	return f.contextResult, f.contextErr
}

func (f *fakeEngine) Abort(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborts++
	return f.abortErr
}

func (f *fakeEngine) Events() <-chan engine.Event { return f.events }

func (f *fakeEngine) Close() error {
	f.mu.Lock()
	f.closes++
	fn, err := f.closeFn, f.closeErr
	f.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return err
}

type fakeWorkspace struct {
	statusCalls   atomic.Int32
	diffCalls     atomic.Int32
	baselineCalls atomic.Int32
	status        workspace.Status
	diff          workspace.Diff
	err           error
	mode          string
	baseline      workspace.Baseline
}

type fakeBaseline struct {
	diff   workspace.Diff
	err    error
	closed atomic.Int32
}

func (f *fakeBaseline) Diff(context.Context) (workspace.Diff, error) {
	return f.diff, f.err
}

func (f *fakeBaseline) Close() error {
	f.closed.Add(1)
	return nil
}

func (f *fakeWorkspace) Status(context.Context) (workspace.Status, error) {
	f.statusCalls.Add(1)
	return f.status, f.err
}

func (f *fakeWorkspace) Diff(_ context.Context, mode string) (workspace.Diff, error) {
	f.diffCalls.Add(1)
	f.mode = mode
	return f.diff, f.err
}

func (f *fakeWorkspace) CaptureBaseline(context.Context) (workspace.Baseline, error) {
	f.baselineCalls.Add(1)
	return f.baseline, f.err
}

func testOptions() Options {
	prefs := config.DefaultPreferences()
	prefs.NoColor = true
	prefs.ReducedMotion = true
	prefs.ASCII = true
	return Options{
		Project: "/project", Version: "test",
		Preferences: prefs,
		Auth:        &fakeAuth{currentErr: auth.ErrNotSignedIn},
	}
}

func testModel(t *testing.T, options Options) *Model {
	t.Helper()
	m := New(options)
	t.Cleanup(func() { _ = m.Shutdown() })
	return m
}

func readyModel(t *testing.T) (*Model, *fakeEngine) {
	t.Helper()
	m := testModel(t, testOptions())
	f := newFakeEngine()
	ctx, cancel := context.WithCancel(m.life.ctx)
	m.lease = &engineLease{ctx: ctx, cancel: cancel, engine: f}
	m.life.adopt(m.lease)
	m.engineGeneration = 1
	m.account = auth.Account{ID: "account-a", Login: "octocat"}
	m.accountOwner = m.account.ID
	m.identityLoading = false
	m.models = f.models
	m.model = "model-a"
	m.contextTier = "default"
	m.reasoningEffort = "medium"
	m.prefs.Model = m.model
	m.prefs.ModelSettings[m.model] = config.ModelSettings{ContextTier: "default", ReasoningEffort: "medium"}
	return m, f
}

// Only finite application commands are passed here, not stream subscriptions
// or animation loops. There is no live service or wall-clock-driven UI loop.
func runFinite(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 1000 {
			t.Fatal("unexpected command loop")
		}
		current := queue[0]
		queue = queue[1:]
		if current == nil {
			continue
		}
		msg := current()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if msg != nil {
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}

func event(m *Model, e engine.Event) tea.Cmd {
	if e.SessionID == "" {
		e.SessionID = m.session.ID
	}
	return m.acceptEvent(eventEnvelope{event: e, decision: m.life.track(m.engineGeneration, e)})
}

func keyPress(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+j":
		return tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
	case "f2":
		return tea.KeyPressMsg{Code: tea.KeyF2}
	case "f3":
		return tea.KeyPressMsg{Code: tea.KeyF3}
	case "f4":
		return tea.KeyPressMsg{Code: tea.KeyF4}
	default:
		return tea.KeyPressMsg{Text: name}
	}
}

func TestConversationBaselinePartialCaptureKeepsStartupUsable(t *testing.T) {
	options := testOptions()
	partial := &fakeBaseline{diff: workspace.Diff{
		IsRepository: true,
		Truncated:    true,
		Text:         "PARTIAL BASELINE\n\"large.gif\": file exceeds the 64 KiB snapshot limit",
	}}
	w := &fakeWorkspace{baseline: partial}
	options.Workspace = w
	m := testModel(t, options)
	m.composer.SetValue("keep my draft")
	noticeBefore := m.notice
	cmd := m.captureBaseline()
	if w.baselineCalls.Load() != 0 {
		t.Fatal("baseline capture blocked the UI loop")
	}
	runFinite(t, m, cmd)
	if w.baselineCalls.Load() != 1 || m.baseline != partial || m.notice != noticeBefore ||
		m.composer.Value() != "keep my draft" {
		t.Fatal("partial baseline became a startup error or discarded the draft")
	}
}

func TestConversationBaselineStaleResultsAreClosed(t *testing.T) {
	options := testOptions()
	old := &fakeBaseline{}
	current := &fakeBaseline{}
	w := &fakeWorkspace{baseline: old}
	options.Workspace = w
	m := testModel(t, options)
	stale := m.captureBaseline()()
	w.baseline = current
	runFinite(t, m, m.captureBaseline())
	runFinite(t, m, func() tea.Msg { return stale })
	if m.baseline != current || old.closed.Load() != 1 || current.closed.Load() != 0 || m.notice.error {
		t.Fatal("stale capture replaced the current baseline or leaked its handle")
	}
	replacement := &fakeBaseline{}
	w.baseline = replacement
	runFinite(t, m, m.captureBaseline())
	if m.baseline != replacement || current.closed.Load() != 1 {
		t.Fatal("replacing a baseline leaked the previous handle")
	}
}

func TestConversationBaselineRealFailureRemainsVisible(t *testing.T) {
	options := testOptions()
	failed := &fakeBaseline{}
	options.Workspace = &fakeWorkspace{baseline: failed, err: errors.New("read denied")}
	m := testModel(t, options)
	m.composer.SetValue("keep my draft")
	runFinite(t, m, m.captureBaseline())
	if m.baseline != nil || failed.closed.Load() != 1 || !m.notice.error ||
		!strings.Contains(m.notice.text, "Conversation change baseline unavailable: read denied") ||
		m.composer.Value() != "keep my draft" {
		t.Fatalf("real failure was hidden or its handle leaked: %#v", m.notice)
	}
}

func TestNewAndDiscoveryDoNotCreateSessionOrSend(t *testing.T) {
	options := testOptions()
	f := newFakeEngine()
	a := &fakeAuth{account: auth.Account{ID: "account", Login: "octocat"}}
	w := &fakeWorkspace{status: workspace.Status{IsRepository: true, Branch: "main"}}
	var factories atomic.Int32
	options.Auth, options.Workspace = a, w
	options.Preferences.Model = "model-a"
	options.NewEngine = func(context.Context, auth.Account) (engine.Engine, error) {
		factories.Add(1)
		return f, nil
	}
	m := testModel(t, options)
	if a.currentCalls.Load() != 0 || w.statusCalls.Load() != 0 || factories.Load() != 0 {
		t.Fatal("New performed service I/O")
	}
	if len(m.entries) != 0 || m.session.ID != "" {
		t.Fatal("startup was not a fresh conversation")
	}
	start := m.Init()
	batch, ok := start().(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatalf("Init should independently discover identity, project, and change baseline, got %#v", batch)
	}
	identity := batch[0]().(identityMsg)
	connect := m.identityResult(identity)
	if factories.Load() != 0 {
		t.Fatal("identity Update blocked on engine startup")
	}
	connected := connect().(connectedMsg)
	subscriptions := m.connectedResult(connected)().(tea.BatchMsg)
	if len(subscriptions) != 2 {
		t.Fatal("expected event subscription before independent model discovery")
	}
	models := subscriptions[1]().(modelsMsg)
	runFinite(t, m, m.modelsResult(models))
	if factories.Load() != 1 || f.starts != 0 || len(f.newModels) != 0 || len(f.sent) != 0 {
		t.Fatalf("started engine was restarted or sent work: factories=%d starts=%d new=%v sends=%v", factories.Load(), f.starts, f.newModels, f.sent)
	}
	if m.model != "model-a" || m.overlay != nil {
		t.Fatal("valid saved model was not honored")
	}
	if a.tokenCalls.Load() != 0 {
		t.Fatal("UI obtained credentials directly")
	}
}

func TestInvalidSavedModelShowsPickerWithoutPrompt(t *testing.T) {
	m, f := readyModel(t)
	m.prefs.Model = "unavailable-model"
	runFinite(t, m, m.modelsResult(modelsMsg{generation: m.engineGeneration, models: f.models}))
	if m.model != "" || m.overlay == nil || m.overlay.kind != dialogModels {
		t.Fatal("invalid saved model did not open model picker")
	}
	if len(f.newModels) != 0 || len(f.sent) != 0 {
		t.Fatal("opening a model picker made a model request")
	}
}

func TestModelPickerGroupsAndAppliesContextAndReasoningAtomically(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{
		ID: "session", Model: m.model, ContextTier: m.contextTier, ReasoningEffort: m.reasoningEffort,
	}
	m.showModels()
	view := m.View().Content
	for _, expected := range []string{
		"OPENAI", "ANTHROPIC", "GPT-5.6 Luna", "Claude Sonnet 5",
		"MODEL", "CONTEXT", "THINKING", "Default · 128K", "Medium", "[current]",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("model picker omitted %q:\n%s", expected, view)
		}
	}
	if strings.Contains(view, "model-a") || strings.Contains(view, "model-b") {
		t.Fatalf("model picker exposed duplicate raw model IDs:\n%s", view)
	}

	m.handleKey(keyPress("tab"))
	m.handleKey(keyPress("right"))
	if len(f.setModels) != 0 || m.contextTier != "default" || m.reasoningEffort != "medium" {
		t.Fatal("picker navigation changed the live selection or called the engine")
	}
	draft := m.overlay.modelDrafts["model-a"]
	if draft.ContextTier != "long_context" || draft.ReasoningEffort != "high" {
		t.Fatalf("picker did not edit both axes: %+v", draft)
	}
	view = m.View().Content
	if !strings.Contains(view, "Long · 1M") || !strings.Contains(view, "High") {
		t.Fatalf("picker did not update the selected row inline:\n%s", view)
	}

	m.handleKey(keyPress("down"))
	locked := m.overlay.modelDrafts["model-b"]
	m.handleKey(keyPress("tab"))
	m.handleKey(keyPress("right"))
	if got := m.overlay.modelDrafts["model-b"]; got != locked ||
		!strings.Contains(m.View().Content, "fixed") {
		t.Fatalf("unsupported axes were not visibly locked: before=%+v after=%+v", locked, got)
	}
	m.handleKey(keyPress("up"))
	runFinite(t, m, m.handleKey(keyPress("enter")))
	if len(f.setModels) != 1 || f.setModels[0] != draft {
		t.Fatalf("selection was not applied exactly once: %+v", f.setModels)
	}
	if m.model != "model-a" || m.contextTier != "long_context" || m.reasoningEffort != "high" ||
		m.prefs.ModelSettings["model-a"].ContextTier != "long_context" ||
		m.prefs.ModelSettings["model-a"].ReasoningEffort != "high" {
		t.Fatal("committed selection was not reflected in UI preferences")
	}

	m.showModels()
	m.handleKey(keyPress("shift+tab"))
	m.handleKey(keyPress("left"))
	m.handleKey(keyPress("esc"))
	if m.contextTier != "long_context" || m.reasoningEffort != "high" || len(f.setModels) != 1 {
		t.Fatal("Escape committed a picker draft")
	}
}

func TestModelPickerUsesResponsiveColumnsAndPreservesDraftsAcrossResize(t *testing.T) {
	m, _ := readyModel(t)
	m.resize(100, 30)
	m.showModels()
	wide := m.View()
	assertBounds(t, wide, 100, 30)
	for _, want := range []string{"MODEL", "CONTEXT", "THINKING", "GPT-5.6 Luna", "< Default · 128K", "< Medium >"} {
		if !strings.Contains(wide.Content, want) {
			t.Fatalf("wide model table omitted %q:\n%s", want, wide.Content)
		}
	}

	m.handleKey(keyPress("tab"))
	m.handleKey(keyPress("right"))
	draft := m.overlay.modelDrafts["model-a"]
	m.resize(52, 24)
	narrow := m.View()
	assertBounds(t, narrow, 52, 24)
	for _, want := range []string{"MODEL / CONTEXT / THINKING", "GPT-5.6 Luna", "Context", "Long · 1M", "Thinking", "High"} {
		if !strings.Contains(narrow.Content, want) {
			t.Fatalf("stacked model picker omitted %q:\n%s", want, narrow.Content)
		}
	}
	if got := m.overlay.modelDrafts["model-a"]; got != draft {
		t.Fatalf("resize changed the picker draft: got %+v want %+v", got, draft)
	}
}

func TestModelPickerTruncatesLongNamesBeforeConfigurationColumns(t *testing.T) {
	m, f := readyModel(t)
	f.models[0].Name = strings.Repeat("Extremely Long Model Name ", 8)
	m.models = f.models
	m.resize(80, 24)
	m.showModels()
	view := m.View()
	assertBounds(t, view, 80, 24)
	for _, want := range []string{"CONTEXT", "THINKING", "Default · 128K", "Medium"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("long model name displaced %q:\n%s", want, view.Content)
		}
	}
}

func TestCommandsStayLocalAndPlanningIsAdvisory(t *testing.T) {
	m, f := readyModel(t)
	for _, text := range []string{"/help", "/help plan", "/theme midnight", "/plan", "/plan off"} {
		m.composer.SetValue(text)
		runFinite(t, m, m.submit())
		m.closeDialog()
	}
	if len(f.sent) != 0 || len(f.newModels) != 0 {
		t.Fatal("local commands sent a prompt or created a session")
	}
	if m.planning {
		t.Fatal("/plan off did not clear advisory focus")
	}
	m.composer.SetValue("/plan compare two approaches")
	cmd := m.submit()
	if len(f.newModels) != 0 || len(f.sent) != 0 {
		t.Fatal("submit performed engine I/O in the UI loop")
	}
	runFinite(t, m, cmd)
	if len(f.newModels) != 1 || f.newModels[0].ModelID != "model-a" || len(f.sent) != 1 {
		t.Fatalf("expected lazy session and one prompt: new=%v send=%v", f.newModels, f.sent)
	}
	if f.sent[0].Text != "compare two approaches" || !f.sent[0].Planning || !m.planning {
		t.Fatalf("wrong advisory message: %#v", f.sent[0])
	}
	if !strings.Contains(m.View().Content, "ADVISORY PLAN") {
		t.Fatal("advisory badge missing")
	}
}

func TestCommandParsingLiteralSlashUnknownAndBusyDraftProtection(t *testing.T) {
	m, f := readyModel(t)
	m.composer.SetValue("/modle")
	runFinite(t, m, m.submit())
	if m.composer.Value() != "/modle" || !m.notice.error || len(f.sent) != 0 {
		t.Fatal("unknown command was sent or draft was lost")
	}
	m.composer.SetValue("//not-a-command")
	runFinite(t, m, m.submit())
	if len(f.sent) != 1 || f.sent[0].Text != "/not-a-command" {
		t.Fatal("shared parser's literal-slash escape was not honored")
	}
	session := m.session.ID
	for _, command := range []string{"/clear", "/resume other", "/model model-b", "/compact preserve state", "/plan off", "another prompt"} {
		m.composer.SetValue(command)
		runFinite(t, m, m.submit())
		if m.composer.Value() != command || m.session.ID != session {
			t.Fatalf("busy action %s discarded draft or switched session", command)
		}
	}
	m.composer.SetValue("/theme high-contrast")
	runFinite(t, m, m.submit())
	if m.prefs.Theme != "high-contrast" || len(f.sent) != 1 {
		t.Fatal("harmless theme command was blocked or forwarded")
	}
}

func TestPaletteKeyboardCompletionAndComposerNewlines(t *testing.T) {
	m, _ := readyModel(t)
	m.handleKey(keyPress("/"))
	if !m.paletteOpen || len(m.paletteItems) != 18 {
		t.Fatalf("slash palette not driven by all registry commands: %#v", m.paletteItems)
	}
	first := m.paletteIndex
	m.handleKey(keyPress("down"))
	if m.paletteIndex == first {
		t.Fatal("arrow did not select next command")
	}
	m.handleKey(keyPress("esc"))
	if m.paletteOpen {
		t.Fatal("Escape did not dismiss palette")
	}
	m.updatePalette()
	if m.paletteOpen {
		t.Fatal("palette reopened without input changing")
	}
	m.composer.SetValue("/th")
	m.paletteHidden = ""
	m.updatePalette()
	m.handleKey(keyPress("tab"))
	if m.composer.Value() != "/theme " || m.paletteOpen {
		t.Fatalf("Tab did not complete command: %q", m.composer.Value())
	}
	m.composer.SetValue("first line")
	m.composer.MoveToEnd()
	m.handleKey(keyPress("ctrl+j"))
	m.handleKey(keyPress("second line"))
	if m.composer.Value() != "first line\nsecond line" {
		t.Fatalf("multiline fallback failed: %q", m.composer.Value())
	}
	m.composer.SetValue("/help")
	m.updatePalette()
	runFinite(t, m, m.handleKey(keyPress("enter")))
	if m.overlay == nil || m.overlay.kind != dialogHelp {
		t.Fatal("Enter did not execute selected local command")
	}
}

func TestCompactRunsLocallyAndKeepsVisibleTranscript(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session-a", Project: "/project", Model: "model-a"}
	m.entries = append(m.entries, &entry{id: "existing", role: "assistant", raw: "important history", final: true})
	f.compactResult = engine.CompactResult{
		Success: true, MessagesRemoved: 4, TokensRemoved: 900,
		CurrentTokens: 600, TokenLimit: 4000,
	}
	m.composer.SetValue("/compact preserve decisions\nand failures")
	cmd := m.submit()
	if len(f.compactions) != 0 {
		t.Fatal("compaction ran in the UI update loop")
	}
	if m.composer.Value() != "" || m.operation.kind != "compacting conversation" {
		t.Fatal("accepted compaction did not clear the command or start an operation")
	}
	runFinite(t, m, cmd)
	if len(f.compactions) != 1 || f.compactions[0] != "preserve decisions\nand failures" {
		t.Fatalf("wrong compaction instructions: %#v", f.compactions)
	}
	if len(m.entries) != 1 || m.entries[0].raw != "important history" {
		t.Fatal("compaction replaced the visible transcript")
	}
	if m.notice.error || !strings.Contains(m.notice.text, "4 messages") ||
		!strings.Contains(m.notice.text, "600 / 4000") {
		t.Fatalf("missing compaction metrics: %+v", m.notice)
	}
	if len(f.sent) != 0 || len(f.newModels) != 0 {
		t.Fatal("compaction sent a prompt or created a session")
	}
}

func TestCompactRequiresExistingSessionAndPreservesRejectedDraft(t *testing.T) {
	m, f := readyModel(t)
	m.composer.SetValue("/compact keep context")
	runFinite(t, m, m.submit())
	if m.composer.Value() != "/compact keep context" || !m.notice.error {
		t.Fatal("missing-session compaction discarded the draft or lacked an error")
	}

	if len(f.compactions) != 0 || len(f.newModels) != 0 {
		t.Fatal("missing-session compaction reached the engine")
	}
}

func TestCompactReportsNoOpAndFailureWithoutChangingTranscript(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session-a", Project: "/project", Model: "model-a"}
	m.entries = append(m.entries, &entry{id: "existing", role: "assistant", raw: "history", final: true})
	m.composer.SetValue("/compact")
	runFinite(t, m, m.submit())
	if m.notice.error || !strings.Contains(m.notice.text, "no context needed") || len(m.entries) != 1 {
		t.Fatalf("wrong no-op result: %+v / %#v", m.notice, m.entries)
	}
	f.compactErr = errors.New("runtime rejected compaction")
	m.composer.SetValue("/compact preserve failures")
	runFinite(t, m, m.submit())
	if !m.notice.error || !strings.Contains(m.notice.text, "runtime rejected compaction") ||
		len(m.entries) < 1 || m.entries[0].raw != "history" {
		t.Fatalf("wrong failure result: %+v / %#v", m.notice, m.entries)
	}
}

func TestCompactComposerGrowsAndShiftTabCyclesChatPlanAutopilot(t *testing.T) {
	m, f := readyModel(t)
	m.resize(80, 24)
	if m.layout.input != 1 || !strings.Contains(m.View().Content, "CHAT / Shift+Tab next: plan") {
		t.Fatalf("composer did not start compact: input=%d", m.layout.input)
	}
	m.handleKey(keyPress("shift+tab"))
	if !m.planning || m.autopilotEnabled || !strings.HasSuffix(m.composer.Prompt, "plan> ") ||
		!strings.Contains(m.View().Content, "ADVISORY PLAN / Shift+Tab next: Autopilot") {
		t.Fatal("Shift+Tab did not switch to visible advisory planning mode")
	}
	m.handleKey(keyPress("first line"))
	m.handleKey(keyPress("ctrl+j"))
	m.handleKey(keyPress("second line"))
	if m.layout.input != 2 {
		t.Fatalf("multiline draft did not grow the composer: input=%d", m.layout.input)
	}
	runFinite(t, m, m.handleKey(keyPress("enter")))
	if m.layout.input != 1 || len(f.sent) != 1 || !f.sent[0].Planning {
		t.Fatalf("send did not preserve mode and collapse composer: input=%d sent=%#v", m.layout.input, f.sent)
	}
	m.handleKey(keyPress("shift+tab"))
	if m.planning || !m.autopilotEnabled || !strings.HasSuffix(m.composer.Prompt, "auto> ") ||
		!strings.Contains(m.View().Content, "AUTOPILOT / Shift+Tab next: chat") {
		t.Fatal("second Shift+Tab did not switch to visible Autopilot mode")
	}
	m.handleKey(keyPress("shift+tab"))
	if m.planning || m.autopilotEnabled || !strings.HasSuffix(m.composer.Prompt, "chat> ") {
		t.Fatal("third Shift+Tab did not return to chat mode")
	}
}

func TestComposerAccentAndBubbleFollowModeAndActivity(t *testing.T) {
	m, _ := readyModel(t)
	m.resize(80, 24)
	if m.composerAccent() != m.color.cyan {
		t.Fatal("chat mode should use the cyan accent")
	}
	idle := m.composerBubble()
	m.handleKey(keyPress("shift+tab"))
	if m.composerAccent() != m.color.amber || m.composerAccentApplied != m.color.amber {
		t.Fatal("plan mode should repaint the composer with the amber accent")
	}
	m.handleKey(keyPress("shift+tab"))
	if m.autopilotAccent() != m.color.magenta || m.autopilotAccent() == m.color.red ||
		m.composerAccent() != m.autopilotAccent() || m.composerAccentApplied != m.autopilotAccent() {
		t.Fatal("Autopilot should use the magenta brand accent instead of a danger color")
	}
	m.handleKey(keyPress("shift+tab"))
	if m.planning || m.autopilotEnabled || m.composerAccent() != m.color.cyan {
		t.Fatal("mode cycle did not return to cyan chat mode")
	}
	m.turn = true
	seen := map[string]bool{}
	for frame := 0; frame < 4; frame++ {
		m.frame = frame
		seen[m.composerBubble()] = true
	}
	if len(seen) < 3 {
		t.Fatalf("thinking bubble did not animate: %v", seen)
	}
	m.turn = false
	if m.composerBubble() != idle {
		t.Fatal("idle bubble should stop pulsing")
	}
}

func TestDeltaFinalAndUserEchoAreReconciled(t *testing.T) {
	m, _ := readyModel(t)
	m.composer.SetValue("hello")
	runFinite(t, m, m.submit())
	event(m, engine.Event{Kind: engine.EventMessage, ID: "user-event", MessageID: "user-message", Role: "user", Text: "hello"})
	for _, e := range []engine.Event{
		{Kind: engine.EventDelta, ID: "d1", MessageID: "assistant-1", Text: "Hello "},
		{Kind: engine.EventDelta, ID: "d1", MessageID: "assistant-1", Text: "Hello "},
		{Kind: engine.EventDelta, ID: "d2", MessageID: "assistant-1", Text: "world"},
		{Kind: engine.EventMessage, ID: "final", MessageID: "assistant-1", Text: "Hello world"},
		{Kind: engine.EventMessage, ID: "final-repeat", MessageID: "assistant-1", Text: "Hello world"},
		{Kind: engine.EventDelta, ID: "late-delta", MessageID: "assistant-1", Text: " world"},
	} {
		event(m, e)
	}
	m.flushTimeline()
	if len(m.entries) != 2 || m.entries[1].raw != "Hello world" || !m.entries[1].final {
		t.Fatalf("stream/final duplicated messages: %#v", m.entries)
	}
}

func TestPermissionCancelAndShutdownResolveExactlyOnce(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session", Project: "/project"}
	m.turn = true
	m.composer.SetValue("my next draft")
	var calls, allows atomic.Int32
	p := &engine.Permission{
		ID: "permission", Kind: "shell", Command: "go test ./...",
		Respond: func(allow bool) error {
			calls.Add(1)
			if allow {
				allows.Add(1)
			}
			return nil
		},
	}
	e := engine.Event{Kind: engine.EventPermission, ID: "request-event", Permission: p}
	event(m, e)
	event(m, e)
	if len(m.requests) != 1 || m.overlay == nil || m.overlay.kind != dialogPermission || m.overlay.selected != 0 {
		t.Fatal("permission did not use a single default-deny overlay")
	}
	runFinite(t, m, m.handleKey(keyPress("ctrl+c")))
	if calls.Load() != 1 || allows.Load() != 0 || len(m.requests) != 0 || f.aborts != 1 {
		t.Fatal("cancellation did not deny the pending request exactly once")
	}
	if m.composer.Value() != "my next draft" {
		t.Fatal("cancellation lost composer draft")
	}
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || f.closes != 1 {
		t.Fatalf("Shutdown was not idempotent: permission=%d closes=%d", calls.Load(), f.closes)
	}
}

func TestAllowOnceAndQuestionChoicesAndFreeText(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turn = true
	var allows atomic.Int32
	event(m, engine.Event{Kind: engine.EventPermission, Permission: &engine.Permission{
		ID: "p", Kind: "write", Path: "main.go", Respond: func(allow bool) error {
			if allow {
				allows.Add(1)
			}
			return nil
		},
	}})
	runFinite(t, m, m.handleKey(keyPress("a")))
	if allows.Load() != 1 || len(m.requests) != 0 {
		t.Fatal("allow-once was not delivered")
	}
	var answers []string
	var cancelled atomic.Int32
	question := func(id string) *engine.Question {
		return &engine.Question{ID: id, Prompt: "Which approach?", Choices: []string{"Small", "Thorough"}, AllowFreeform: true,
			Respond: func(answer string) error { answers = append(answers, answer); return nil },
			Cancel:  func() error { cancelled.Add(1); return nil },
		}
	}
	event(m, engine.Event{Kind: engine.EventQuestion, Question: question("q1")})
	m.handleKey(keyPress("down"))
	runFinite(t, m, m.handleKey(keyPress("enter")))
	event(m, engine.Event{Kind: engine.EventQuestion, Question: question("q2")})
	m.overlay.selected = 2
	m.handleKey(keyPress("enter"))
	m.handlePaste("A different approach")
	runFinite(t, m, m.handleKey(keyPress("enter")))
	event(m, engine.Event{Kind: engine.EventQuestion, Question: question("q3")})
	runFinite(t, m, m.handleKey(keyPress("esc")))
	if strings.Join(answers, "|") != "Thorough|A different approach" || cancelled.Load() != 1 {
		t.Fatalf("question answers/cancellation incorrect: %v / %d", answers, cancelled.Load())
	}
	_ = m.Shutdown()
	if len(answers) != 2 || cancelled.Load() != 1 {
		t.Fatal("Shutdown resolved an already answered question again")
	}
}

func TestAutopilotCommandShortcutAliasAndPermissionOption(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turn = true
	var allows atomic.Int32
	permission := func(id string) engine.Event {
		return engine.Event{Kind: engine.EventPermission, Permission: &engine.Permission{
			ID: id, Kind: "shell", Command: "go test ./...", Respond: func(allow bool) error {
				if allow {
					allows.Add(1)
				}
				return nil
			},
		}}
	}

	m.composer.SetValue("/autopilot")
	runFinite(t, m, m.submit())
	if !m.autopilotEnabled {
		t.Fatal("/autopilot did not enable conversation-wide approval")
	}
	runFinite(t, m, event(m, permission("automatic")))
	if allows.Load() != 1 || len(m.requests) != 0 || m.overlay != nil {
		t.Fatal("autopilot did not approve a permission without a popup")
	}
	var questionCanceled atomic.Int32
	runFinite(t, m, event(m, engine.Event{Kind: engine.EventQuestion, Question: &engine.Question{
		ID: "question", Prompt: "Choose a direction", Cancel: func() error {
			questionCanceled.Add(1)
			return nil
		},
	}}))
	if m.overlay == nil || m.overlay.kind != dialogQuestion {
		t.Fatal("Autopilot answered or hid an agent question")
	}
	runFinite(t, m, m.handleKey(keyPress("esc")))
	if questionCanceled.Load() != 1 {
		t.Fatal("agent question was not cancelled exactly once")
	}

	m.handleKey(keyPress("f2"))
	runFinite(t, m, event(m, permission("prompted")))
	if m.autopilotEnabled || m.overlay == nil || m.overlay.kind != dialogPermission || len(m.overlay.items) != 3 || m.overlay.items[2].id != "autopilot" {
		t.Fatal("permission popup did not offer deny, allow once, and Autopilot")
	}
	runFinite(t, m, m.chooseItem(m.overlay, m.overlay.items[2]))
	if !m.autopilotEnabled || allows.Load() != 2 || len(m.requests) != 0 {
		t.Fatal("popup Autopilot did not approve the request and enable automatic approval")
	}

	runFinite(t, m, event(m, permission("following")))
	if allows.Load() != 3 || m.overlay != nil {
		t.Fatal("popup Autopilot did not apply to later permissions")
	}
	m.resetConversation()
	if m.autopilotEnabled {
		t.Fatal("Autopilot survived a conversation reset")
	}

	m.composer.SetValue("/allow-all")
	runFinite(t, m, m.submit())
	if !m.autopilotEnabled {
		t.Fatal("legacy /allow-all alias did not enable Autopilot")
	}
}

func TestAutopilotOffCommandImmediatelyRestoresPermissionPrompts(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turn = true
	m.setAutopilot(true)

	m.composer.SetValue("/autopilot off")
	runFinite(t, m, m.submit())
	if m.autopilotEnabled || !strings.Contains(m.notice.text, "require individual approval") {
		t.Fatalf("/autopilot off did not restore prompts: enabled=%t notice=%q", m.autopilotEnabled, m.notice.text)
	}

	var calls, allows atomic.Int32
	runFinite(t, m, event(m, engine.Event{Kind: engine.EventPermission, Permission: &engine.Permission{
		ID: "after-off", Kind: "write", Path: "main.go", Respond: func(allow bool) error {
			calls.Add(1)
			if allow {
				allows.Add(1)
			}
			return nil
		},
	}}))
	if calls.Load() != 0 || allows.Load() != 0 || m.overlay == nil || m.overlay.kind != dialogPermission {
		t.Fatal("permission arriving after /autopilot off was not held for an explicit decision")
	}
	runFinite(t, m, m.handleKey(keyPress("esc")))
	if calls.Load() != 1 || allows.Load() != 0 {
		t.Fatal("denied permission after /autopilot off was not resolved exactly once")
	}
}

func TestPlanAndAutopilotCommandsSelectMutuallyExclusiveModes(t *testing.T) {
	m, _ := readyModel(t)
	m.setAutopilot(true)

	m.composer.SetValue("/plan")
	runFinite(t, m, m.submit())
	if !m.planning || m.autopilotEnabled || !strings.HasSuffix(m.composer.Prompt, "plan> ") {
		t.Fatal("/plan did not leave Autopilot and select Plan mode")
	}

	m.composer.SetValue("/autopilot")
	runFinite(t, m, m.submit())
	if m.planning || !m.autopilotEnabled || !strings.HasSuffix(m.composer.Prompt, "auto> ") {
		t.Fatal("/autopilot did not leave Plan and select Autopilot mode")
	}
}

func TestF2EnablesAutopilotForQueuedPermissionsButPreservesQuestions(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turn = true
	var allowed, questionCanceled atomic.Int32

	runFinite(t, m, event(m, engine.Event{Kind: engine.EventPermission, Permission: &engine.Permission{
		ID: "queued-permission", Kind: "shell", Command: "go test ./...", Respond: func(allow bool) error {
			if allow {
				allowed.Add(1)
			}
			return nil
		},
	}}))
	runFinite(t, m, event(m, engine.Event{Kind: engine.EventQuestion, Question: &engine.Question{
		ID: "queued-question", Prompt: "Which option?", Cancel: func() error {
			questionCanceled.Add(1)
			return nil
		},
	}}))
	if len(m.requests) != 2 || m.overlay == nil || m.overlay.kind != dialogPermission {
		t.Fatal("permission and question were not queued in arrival order")
	}

	runFinite(t, m, m.handleKey(keyPress("f2")))
	if !m.autopilotEnabled || allowed.Load() != 1 || questionCanceled.Load() != 0 ||
		len(m.requests) != 1 || m.overlay == nil || m.overlay.kind != dialogQuestion {
		t.Fatal("F2 did not approve queued permissions while preserving the queued question")
	}
	runFinite(t, m, m.handleKey(keyPress("esc")))
	if questionCanceled.Load() != 1 {
		t.Fatal("preserved question was not cancelled exactly once")
	}
}

func TestF2DoesNotToggleAutopilotBehindNonPermissionDialogs(t *testing.T) {
	m, _ := readyModel(t)
	m.runLocal("help")
	if m.overlay == nil || m.overlay.kind != dialogHelp {
		t.Fatal("help dialog did not open")
	}
	dialogID := m.overlay.id
	m.handleKey(keyPress("f2"))
	if m.autopilotEnabled || m.overlay == nil || m.overlay.id != dialogID || m.overlay.kind != dialogHelp {
		t.Fatal("F2 changed Autopilot or replaced a non-permission dialog")
	}
}

func TestResumeTurnsOffConversationScopedAutopilot(t *testing.T) {
	m, _ := readyModel(t)
	m.setAutopilot(true)
	m.operation = operation{id: 7, kind: "resuming conversation"}
	cmd := m.sessionResult(sessionMsg{
		generation: m.engineGeneration,
		operation:  7,
		kind:       "resume",
		session:    engine.Session{ID: "resumed", Project: m.opts.Project},
	})
	if m.autopilotEnabled {
		t.Fatal("Autopilot survived a successful conversation resume")
	}
	runFinite(t, m, cmd)
}

func TestShutdownOwnsPermissionBeforeUpdateReceivesIt(t *testing.T) {
	m, f := readyModel(t)
	var denied atomic.Int32
	f.events <- engine.Event{Kind: engine.EventPermission, SessionID: "session", Permission: &engine.Permission{
		ID: "in-flight", Respond: func(allow bool) error {
			if !allow {
				denied.Add(1)
			}
			return nil
		},
	}}
	msg := m.waitEvents()().(eventsMsg)
	if len(msg.events) != 1 {
		t.Fatal("request not read from engine")
	}
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if denied.Load() != 1 {
		t.Fatal("an in-flight permission was stranded before Update")
	}
}

func TestResumeBuffersEarlyHistoryAndPreservesDraft(t *testing.T) {
	m, f := readyModel(t)
	m.composer.SetValue("keep this draft")
	cmd := m.resumeSession("saved-session")
	if len(f.resumed) != 0 {
		t.Fatal("resume blocked the UI loop")
	}
	for _, e := range []engine.Event{
		{Kind: engine.EventMessage, SessionID: "saved-session", MessageID: "old-u", Role: "user", Text: "old prompt", History: true},
		{Kind: engine.EventMessage, SessionID: "saved-session", MessageID: "old-a", Role: "assistant", Text: "old answer", History: true},
	} {
		m.receiveEvents(eventsMsg{generation: m.engineGeneration, events: []eventEnvelope{{event: e}}})
	}
	if len(m.entries) != 0 || len(m.sessionBuffer) != 2 {
		t.Fatal("early history was displayed in the wrong session")
	}
	runFinite(t, m, cmd)
	if m.session.ID != "saved-session" || m.model != "model-b" || len(m.entries) != 2 || len(f.sent) != 0 {
		t.Fatal("resume did not restore session/model/history without sending")
	}
	if m.composer.Value() != "keep this draft" {
		t.Fatal("resume discarded an unsent draft")
	}
	event(m, engine.Event{Kind: engine.EventMessage, MessageID: "old-a", Role: "assistant", Text: "old answer", History: true})
	if len(m.entries) != 2 {
		t.Fatal("repeated history duplicated a message")
	}
}

func TestClearIsLazyAndDraftIsExplicitlyProtected(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "old"}
	m.entries = append(m.entries, &entry{id: "old", role: "assistant", raw: "history", final: true})
	m.planning = true
	m.autopilotEnabled = true
	m.composer.SetValue("unfinished thought")
	m.runLocal("clear")
	if m.overlay == nil || m.overlay.kind != dialogConfirm || m.session.ID != "old" {
		t.Fatal("clear from palette did not protect a draft")
	}
	m.handleKey(keyPress("down"))
	runFinite(t, m, m.handleKey(keyPress("enter")))
	if m.session.ID != "" || len(m.entries) != 0 || len(f.newModels) != 0 || m.composer.Value() != "unfinished thought" {
		t.Fatal("clear deleted a draft or eagerly created a session")
	}
	if m.planning || m.autopilotEnabled {
		t.Fatal("clear retained conversation-scoped planning or Autopilot state")
	}

	m.session = engine.Session{ID: "second"}
	m.entries = append(m.entries, &entry{id: "second", role: "assistant", raw: "more history", final: true})
	m.composer.SetValue("/clear")
	runFinite(t, m, m.submit())
	if m.overlay != nil || m.session.ID != "" || len(m.entries) != 0 || m.composer.Value() != "" || len(f.newModels) != 0 {
		t.Fatal("typed /clear did not reset immediately and lazily")
	}
}

func TestStaleSessionAndGenerationCannotMutateConversation(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "current"}
	m.turn = true
	var denied atomic.Int32
	permission := &engine.Permission{ID: "old-request", Respond: func(bool) error { denied.Add(1); return nil }}
	stale := engine.Event{Kind: engine.EventPermission, SessionID: "previous", Permission: permission}
	runFinite(t, m, m.acceptEvent(eventEnvelope{event: stale, decision: m.life.track(m.engineGeneration, stale)}))
	event(m, engine.Event{Kind: engine.EventDelta, SessionID: "previous", MessageID: "old", Text: "must not appear"})
	if len(m.entries) != 0 || len(m.requests) != 0 || denied.Load() != 1 {
		t.Fatal("stale session was displayed or permission stranded")
	}
	msg := sessionMsg{generation: m.engineGeneration - 1, operation: 1, session: engine.Session{ID: "late"}, kind: "resume"}
	m.sessionResult(msg)
	if m.session.ID != "current" {
		t.Fatal("stale asynchronous result replaced current session")
	}
}

func TestLateEngineStartupIsClosedAfterShutdown(t *testing.T) {
	options := testOptions()
	f := newFakeEngine()
	started, release := make(chan struct{}), make(chan struct{})
	options.NewEngine = func(context.Context, auth.Account) (engine.Engine, error) {
		close(started)
		<-release
		return f, nil
	}
	m := testModel(t, options)
	m.account = auth.Account{ID: "account", Login: "octocat"}
	cmd := m.connect()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("late startup did not terminate")
	}
	if f.closes != 1 {
		t.Fatal("engine returned after Shutdown was leaked")
	}
}

func TestSessionCreationIdleCannotCancelFirstPrompt(t *testing.T) {
	m, f := readyModel(t)
	m.composer.SetValue("first prompt")
	cmd := m.submit()
	m.receiveEvents(eventsMsg{generation: m.engineGeneration, events: []eventEnvelope{
		{event: engine.Event{Kind: engine.EventIdle, ID: "startup-idle", SessionID: "new-session"}},
	}})
	f.sendFn = func(ctx context.Context, _ engine.Message) error { return ctx.Err() }
	runFinite(t, m, cmd)
	if !m.turn || len(f.sent) != 1 || m.notice.error {
		t.Fatalf("startup idle prevented the first prompt: turn=%t sent=%v error=%q", m.turn, f.sent, m.notice.text)
	}
}

func TestFailedSendAndSessionSwitchDoNotLoseDraftOrHistory(t *testing.T) {
	m, f := readyModel(t)
	f.newErr = errors.New("session creation failed")
	m.composer.SetValue("unsent prompt")
	runFinite(t, m, m.submit())
	if m.composer.Value() != "unsent prompt" || len(f.sent) != 0 {
		t.Fatal("failed session creation lost or sent a draft")
	}
	f.newErr = nil
	f.sendsErr = errors.New("send unavailable")
	runFinite(t, m, m.submit())
	if m.composer.Value() != "unsent prompt" || !m.needsAbort || m.sendPending {
		t.Fatal("send failure lost the prompt or incorrectly treated uncertain delivery as idle")
	}
	runFinite(t, m, m.cancelWork())
	event(m, engine.Event{Kind: engine.EventIdle, Name: "aborted", ID: "abort-barrier"})
	session, count := m.session.ID, len(m.entries)
	f.resumeErr = errors.New("session history unavailable")
	runFinite(t, m, m.resumeSession("missing"))
	if m.session.ID != session || len(m.entries) < count || m.composer.Value() != "unsent prompt" {
		t.Fatal("failed resume replaced history or draft")
	}
}

func TestModelSwitchFailureNeverSubstitutesAnotherModel(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session", Model: m.model}
	f.setModelErr = errors.New("model is temporarily unavailable")
	cmd, ok := m.selectModel("model-b")
	if !ok || len(f.setModels) != 0 {
		t.Fatal("model switching did not use an asynchronous command")
	}
	runFinite(t, m, cmd)
	if m.model != "model-a" || m.prefs.Model != "model-a" || !m.notice.error {
		t.Fatal("failed model change silently selected a replacement")
	}
}

func TestBusyExitRequiresExplicitStopAndReleasesRequest(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turn = true
	var calls, allows atomic.Int32
	event(m, engine.Event{Kind: engine.EventPermission, Permission: &engine.Permission{
		ID: "pending-exit", Command: "go test ./...",
		Respond: func(allow bool) error {
			calls.Add(1)
			if allow {
				allows.Add(1)
			}
			return nil
		},
	}})
	m.composer.SetValue("/exit")
	runFinite(t, m, m.submit())
	if m.overlay == nil || m.overlay.kind != dialogConfirm || calls.Load() != 0 || f.closes != 0 || m.quitting {
		t.Fatal("busy /exit stopped work without confirmation")
	}
	m.handleKey(keyPress("esc"))
	if m.overlay == nil || m.overlay.kind != dialogPermission || len(m.requests) != 1 {
		t.Fatal("declining exit lost the pending request")
	}
	m.confirmExit()
	m.handleKey(keyPress("down"))
	runFinite(t, m, m.handleKey(keyPress("enter")))
	if !m.quitting || calls.Load() != 1 || allows.Load() != 0 || f.closes != 1 {
		t.Fatal("confirmed exit did not deny the request and close the engine exactly once")
	}
}

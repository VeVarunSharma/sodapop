package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type permissionCall struct {
	id       string
	decision rpc.PermissionDecision
}

type compactCall struct {
	instructions string
	trigger      rpc.SessionHistoryCompactRequestTrigger
}

type fakeClient struct {
	mu          sync.Mutex
	options     *copilot.ClientOptions
	models      []copilot.ModelInfo
	sessions    map[string]*fakeSession
	created     []*copilot.SessionConfig
	resumed     []*copilot.ResumeSessionConfig
	starts      int
	stops       int
	startErr    error
	stopErr     error
	health      func(context.Context) error
	createEvent *copilot.SessionEvent
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		models: []copilot.ModelInfo{
			{ID: "model-a", Name: "Model A"},
			{ID: "model-b", Name: "Model B", Capabilities: copilot.ModelCapabilities{
				Supports: copilot.ModelSupports{ReasoningEffort: true},
			}, SupportedReasoningEfforts: []string{"low", "medium", "high"}, DefaultReasoningEffort: "medium"},
		},
		sessions: make(map[string]*fakeSession),
	}
}

func (c *fakeClient) Start(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts++
	return c.startErr
}

func (c *fakeClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops++
	return c.stopErr
}

func (c *fakeClient) Health(ctx context.Context) error {
	c.mu.Lock()
	fn := c.health
	c.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

func (c *fakeClient) ListModels(context.Context) ([]copilot.ModelInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.models), nil
}

func (c *fakeClient) CreateSession(ctx context.Context, config *copilot.SessionConfig) (runtimeSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &fakeSession{
		id: config.SessionID, project: config.WorkingDirectory, model: config.Model,
		contextTier: string(config.ContextTier), reasoningEffort: config.ReasoningEffort,
		onEvent: config.OnEvent, decisions: make(chan permissionCall, 256),
	}
	c.mu.Lock()
	c.created = append(c.created, config)
	c.sessions[s.id] = s
	early := c.createEvent
	c.mu.Unlock()
	s.emit(copilot.SessionEvent{ID: "session-start-" + s.id, Data: &copilot.SessionStartData{
		SessionID: s.id, Context: &copilot.WorkingDirectoryContext{Cwd: s.project},
	}})
	if early != nil {
		s.emit(*early)
	}
	return s, nil
}

func (c *fakeClient) ResumeSession(ctx context.Context, id string, config *copilot.ResumeSessionConfig) (runtimeSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	old := c.sessions[id]
	c.resumed = append(c.resumed, config)
	c.mu.Unlock()
	old.mu.Lock()
	history := slices.Clone(old.history)
	sends := slices.Clone(old.sends)
	old.mu.Unlock()
	s := &fakeSession{
		id: id, project: config.WorkingDirectory, model: config.Model,
		contextTier: string(config.ContextTier), reasoningEffort: config.ReasoningEffort, onEvent: config.OnEvent,
		decisions: make(chan permissionCall, 256), history: history, sends: sends,
	}
	c.mu.Lock()
	c.sessions[id] = s
	c.mu.Unlock()
	for _, event := range history {
		config.OnEvent(event)
	}
	return s, nil
}

func (c *fakeClient) ListSessions(context.Context) ([]copilot.SessionMetadata, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []copilot.SessionMetadata
	for _, session := range c.sessions {
		result = append(result, copilot.SessionMetadata{
			SessionID: session.id, Context: &copilot.SessionContext{WorkingDirectory: session.project},
		})
	}
	return result, nil
}

func (c *fakeClient) session(t *testing.T, id string) *fakeSession {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	result := c.sessions[id]
	if result == nil {
		t.Fatalf("missing fake session %s", id)
	}
	return result
}

type fakeSession struct {
	mu              sync.Mutex
	id              string
	project         string
	model           string
	contextTier     string
	reasoningEffort string
	onEvent         copilot.SessionEventHandler
	history         []copilot.SessionEvent
	sends           []copilot.MessageOptions
	tokens          []string
	decisions       chan permissionCall
	configures      int
	disconnects     int
	aborts          int
	sendHook        func(context.Context) error
	abortHook       func(context.Context) error
	decideHook      func(context.Context, rpc.PermissionDecision) error
	setModelErr     error
	compactErr      error
	compactResult   *rpc.HistoryCompactResult
	compactions     []compactCall
	contextResult   *rpc.MetadataContextAttributionResult
	contextErr      error
	heaviestResult  *rpc.MetadataContextHeaviestMessagesResult
	heaviestErr     error
	heaviestLimit   int64
	configErr       error
}

func (s *fakeSession) ID() string { return s.id }

func (s *fakeSession) Configure(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configures++
	return s.configErr
}

func (s *fakeSession) Send(ctx context.Context, options copilot.MessageOptions) (string, error) {
	s.mu.Lock()
	s.sends = append(s.sends, options)
	id := fmt.Sprintf("%s:user:%d", s.id, len(s.sends))
	hook := s.sendHook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prompt := options.Prompt
	if options.DisplayPrompt != "" {
		prompt = options.DisplayPrompt
	}
	s.emit(copilot.SessionEvent{ID: id + ":event", Data: &copilot.UserMessageData{MessageID: &id, Content: prompt}})
	return id, nil
}

func (s *fakeSession) Compact(_ context.Context, request *rpc.SessionHistoryCompactRequest) (*rpc.HistoryCompactResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := compactCall{}
	if request != nil {
		if request.CustomInstructions != nil {
			call.instructions = *request.CustomInstructions
		}
		if request.Trigger != nil {
			call.trigger = *request.Trigger
		}
	}
	s.compactions = append(s.compactions, call)
	if s.compactResult == nil {
		s.compactResult = &rpc.HistoryCompactResult{Success: true}
	}
	return s.compactResult, s.compactErr
}

func (s *fakeSession) ContextAttribution(context.Context) (*rpc.MetadataContextAttributionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextResult, s.contextErr
}

func (s *fakeSession) ContextHeaviestMessages(_ context.Context, limit int64) (*rpc.MetadataContextHeaviestMessagesResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heaviestLimit = limit
	return s.heaviestResult, s.heaviestErr
}

func (s *fakeSession) GetEvents(context.Context) ([]copilot.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.history), nil
}

func (s *fakeSession) SetModel(_ context.Context, selection ModelSelection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setModelErr != nil {
		return s.setModelErr
	}
	s.model = selection.ModelID
	s.contextTier = selection.ContextTier
	s.reasoningEffort = selection.ReasoningEffort
	return nil
}

func (s *fakeSession) SetToken(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = append(s.tokens, token)
	return nil
}

func (s *fakeSession) DecidePermission(ctx context.Context, id string, decision rpc.PermissionDecision, _ *rpc.PermissionDecisionContext) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	hook := s.decideHook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, decision); err != nil {
			return err
		}
	}
	s.decisions <- permissionCall{id: id, decision: decision}
	return nil
}

func (s *fakeSession) Abort(ctx context.Context) error {
	s.mu.Lock()
	s.aborts++
	hook := s.abortHook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return err
		}
	}
	s.emit(copilot.SessionEvent{ID: fmt.Sprintf("%s:abort:%d", s.id, s.aborts), Data: &copilot.SessionIdleData{Aborted: copilot.Bool(true)}})
	return nil
}

func (s *fakeSession) Disconnect() error {
	s.mu.Lock()
	s.disconnects++
	s.mu.Unlock()
	return nil
}

func (s *fakeSession) emit(event copilot.SessionEvent) {
	s.mu.Lock()
	s.history = append(s.history, event)
	handler := s.onEvent
	s.mu.Unlock()
	handler(event)
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Project: t.TempDir(), Home: t.TempDir(), AccountID: "account-one",
		TokenSource: func(context.Context) (string, error) { return "sodapop-private-test-token", nil },
	}
}

func testEngine(t *testing.T, cfg Config, client *fakeClient, alter func(*dependencies)) *Copilot {
	t.Helper()
	bundlePath := filepath.Join(t.TempDir(), "bundled", "copilot-runtime")
	deps := dependencies{
		bundlePath: func() (string, error) { return bundlePath, nil },
		client: func(options *copilot.ClientOptions) runtimeClient {
			client.mu.Lock()
			client.options = options
			client.mu.Unlock()
			return client
		},
		now: func() time.Time { return time.Unix(100, 0).UTC() },
	}
	if alter != nil {
		alter(&deps)
	}
	engine, err := newCopilot(cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func startedEngine(t *testing.T) (*Copilot, *fakeClient, *fakeSession) {
	t.Helper()
	client := newFakeClient()
	engine := testEngine(t, testConfig(t), client, nil)
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	meta, err := engine.NewSession(t.Context(), ModelSelection{ModelID: "model-a", ContextTier: "default"})
	if err != nil {
		t.Fatal(err)
	}
	return engine, client, client.session(t, meta.ID)
}

func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case value, open := <-channel:
		if !open {
			t.Fatal("channel closed unexpectedly")
		}
		return value
	case <-timer.C:
		t.Fatal("timed out waiting for deterministic worker")
		var zero T
		return zero
	}
}

func nextKind(t *testing.T, engine *Copilot, kind EventKind) Event {
	t.Helper()
	for {
		event := receive(t, engine.Events())
		if event.Kind == kind {
			return event
		}
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
		t.Fatal("timed out waiting for deterministic signal")
	}
}

package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/runtimebundle"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type fakeSDKClientBackend struct {
	startErr       error
	stopErr        error
	status         *copilot.GetStatusResponse
	statusErr      error
	pluginErr      error
	pluginPaths    []string
	models         []copilot.ModelInfo
	modelsErr      error
	sessions       []copilot.SessionMetadata
	sessionsErr    error
	session        sdkSessionBackend
	createErr      error
	resumeErr      error
	createConfig   *copilot.SessionConfig
	resumeID       string
	resumeConfig   *copilot.ResumeSessionConfig
	stopWait       <-chan struct{}
	forceRelease   chan struct{}
	forceOnce      sync.Once
	startCalls     int
	statusCalls    int
	pluginCalls    int
	stopCalls      int
	forceStopCalls int
}

func (f *fakeSDKClientBackend) Start(context.Context) error {
	f.startCalls++
	return f.startErr
}

func (f *fakeSDKClientBackend) Stop() error {
	f.stopCalls++
	if f.stopWait != nil {
		<-f.stopWait
	}
	return f.stopErr
}

func (f *fakeSDKClientBackend) ForceStop() {
	f.forceStopCalls++
	if f.forceRelease != nil {
		f.forceOnce.Do(func() { close(f.forceRelease) })
	}
}

func (f *fakeSDKClientBackend) Status(context.Context) (*copilot.GetStatusResponse, error) {
	f.statusCalls++
	return f.status, f.statusErr
}

func (f *fakeSDKClientBackend) SetBuiltinPlugins(_ context.Context, paths []string) error {
	f.pluginCalls++
	f.pluginPaths = make([]string, len(paths))
	copy(f.pluginPaths, paths)
	return f.pluginErr
}

func (f *fakeSDKClientBackend) ListModels(context.Context) ([]copilot.ModelInfo, error) {
	return f.models, f.modelsErr
}

func (f *fakeSDKClientBackend) ListSessions(context.Context) ([]copilot.SessionMetadata, error) {
	return f.sessions, f.sessionsErr
}

func (f *fakeSDKClientBackend) CreateSession(_ context.Context, config *copilot.SessionConfig) (sdkSessionBackend, error) {
	f.createConfig = config
	return f.session, f.createErr
}

func (f *fakeSDKClientBackend) ResumeSession(_ context.Context, id string, config *copilot.ResumeSessionConfig) (sdkSessionBackend, error) {
	f.resumeID, f.resumeConfig = id, config
	return f.session, f.resumeErr
}

type fakeSDKSessionBackend struct {
	id              string
	current         *rpc.CurrentModel
	currentErr      error
	mode            *rpc.PermissionsSetModeResult
	modeErr         error
	reset           *rpc.PermissionsResetSessionApprovalsResult
	resetErr        error
	policy          *rpc.PermissionsConfigureResult
	policyErr       error
	required        *rpc.PermissionsSetRequiredResult
	requiredErr     error
	interaction     *rpc.SessionMode
	interactionErr  error
	initializeErr   error
	tools           *rpc.ToolsGetCurrentMetadataResult
	toolsErr        error
	mcp             *rpc.MCPServerList
	mcpErr          error
	sendID          string
	sendErr         error
	compactResult   *rpc.HistoryCompactResult
	compactErr      error
	contextResult   *rpc.MetadataContextAttributionResult
	contextErr      error
	heaviestResult  *rpc.MetadataContextHeaviestMessagesResult
	heaviestErr     error
	heaviestRequest *rpc.MetadataContextHeaviestMessagesRequest
	events          []copilot.SessionEvent
	eventsErr       error
	setModelErr     error
	credentials     *rpc.SessionSetCredentialsResult
	credentialsErr  error
	permission      *rpc.PermissionRequestResult
	permissionErr   error
	abortErr        error
	disconnectErr   error
	disconnectWait  <-chan struct{}
	permissionMode  *rpc.PermissionsSetModeRequest
	resetRequest    *rpc.PermissionsResetSessionApprovalsRequest
	policyRequest   *rpc.PermissionsConfigureParams
	requiredRequest *rpc.PermissionsSetRequiredRequest
	sent            copilot.MessageOptions
	compacted       *rpc.SessionHistoryCompactRequest
	modelSet        string
	modelOptions    *copilot.SetModelOptions
	credentialSet   *rpc.SessionSetCredentialsParams
	permissionSet   *rpc.PermissionDecisionRequest
	abortCalls      int
	disconnectCalls int
}

func newFakeSDKSessionBackend() *fakeSDKSessionBackend {
	model := "model-a"
	interaction := rpc.SessionModeInteractive
	return &fakeSDKSessionBackend{
		id:          "session-a",
		current:     &rpc.CurrentModel{ModelID: &model},
		mode:        &rpc.PermissionsSetModeResult{Success: true, Mode: rpc.PermissionModeManual},
		reset:       &rpc.PermissionsResetSessionApprovalsResult{Success: true},
		policy:      &rpc.PermissionsConfigureResult{Success: true},
		required:    &rpc.PermissionsSetRequiredResult{Success: true},
		interaction: &interaction,
		tools: &rpc.ToolsGetCurrentMetadataResult{Tools: []rpc.CurrentToolMetadata{
			{Name: "view"}, {Name: "edit"}, {Name: "bash"}, {Name: "ask_user"},
		}},
		mcp:           &rpc.MCPServerList{},
		sendID:        "message-a",
		compactResult: &rpc.HistoryCompactResult{Success: true},
		credentials:   &rpc.SessionSetCredentialsResult{Success: true, CopilotUserResolved: copilot.Bool(true)},
		permission:    &rpc.PermissionRequestResult{Success: true},
	}
}

func (f *fakeSDKSessionBackend) ID() string { return f.id }

func (f *fakeSDKSessionBackend) CurrentModel(context.Context) (*rpc.CurrentModel, error) {
	return f.current, f.currentErr
}

func (f *fakeSDKSessionBackend) SetPermissionMode(_ context.Context, request *rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error) {
	f.permissionMode = request
	return f.mode, f.modeErr
}

func (f *fakeSDKSessionBackend) ResetSessionApprovals(_ context.Context, request *rpc.PermissionsResetSessionApprovalsRequest) (*rpc.PermissionsResetSessionApprovalsResult, error) {
	f.resetRequest = request
	return f.reset, f.resetErr
}

func (f *fakeSDKSessionBackend) ConfigurePermissions(_ context.Context, request *rpc.PermissionsConfigureParams) (*rpc.PermissionsConfigureResult, error) {
	f.policyRequest = request
	return f.policy, f.policyErr
}

func (f *fakeSDKSessionBackend) SetPermissionsRequired(_ context.Context, request *rpc.PermissionsSetRequiredRequest) (*rpc.PermissionsSetRequiredResult, error) {
	f.requiredRequest = request
	return f.required, f.requiredErr
}

func (f *fakeSDKSessionBackend) InteractionMode(context.Context) (*rpc.SessionMode, error) {
	return f.interaction, f.interactionErr
}

func (f *fakeSDKSessionBackend) InitializeTools(context.Context) error { return f.initializeErr }

func (f *fakeSDKSessionBackend) CurrentTools(context.Context) (*rpc.ToolsGetCurrentMetadataResult, error) {
	return f.tools, f.toolsErr
}

func (f *fakeSDKSessionBackend) MCPServers(context.Context) (*rpc.MCPServerList, error) {
	return f.mcp, f.mcpErr
}

func (f *fakeSDKSessionBackend) Send(_ context.Context, options copilot.MessageOptions) (string, error) {
	f.sent = options
	return f.sendID, f.sendErr
}

func (f *fakeSDKSessionBackend) Compact(_ context.Context, request *rpc.SessionHistoryCompactRequest) (*rpc.HistoryCompactResult, error) {
	f.compacted = request
	return f.compactResult, f.compactErr
}

func (f *fakeSDKSessionBackend) ContextAttribution(context.Context) (*rpc.MetadataContextAttributionResult, error) {
	return f.contextResult, f.contextErr
}

func (f *fakeSDKSessionBackend) ContextHeaviestMessages(_ context.Context, request *rpc.MetadataContextHeaviestMessagesRequest) (*rpc.MetadataContextHeaviestMessagesResult, error) {
	f.heaviestRequest = request
	return f.heaviestResult, f.heaviestErr
}

func (f *fakeSDKSessionBackend) GetEvents(context.Context) ([]copilot.SessionEvent, error) {
	return f.events, f.eventsErr
}

func (f *fakeSDKSessionBackend) SetModel(_ context.Context, model string, options *copilot.SetModelOptions) error {
	f.modelSet = model
	f.modelOptions = options
	return f.setModelErr
}

func (f *fakeSDKSessionBackend) SetCredentials(_ context.Context, request *rpc.SessionSetCredentialsParams) (*rpc.SessionSetCredentialsResult, error) {
	f.credentialSet = request
	return f.credentials, f.credentialsErr
}

func (f *fakeSDKSessionBackend) HandlePermission(_ context.Context, request *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error) {
	f.permissionSet = request
	return f.permission, f.permissionErr
}

func (f *fakeSDKSessionBackend) Abort(context.Context) error {
	f.abortCalls++
	return f.abortErr
}

func (f *fakeSDKSessionBackend) Disconnect() error {
	f.disconnectCalls++
	if f.disconnectWait != nil {
		<-f.disconnectWait
	}
	return f.disconnectErr
}

func immediateShutdownTimer(time.Duration) shutdownTimer {
	done := make(chan time.Time)
	close(done)
	return shutdownTimer{done: done, stop: func() {}}
}

func TestSDKClientStartVerifiesRuntimeAndResetsPlugins(t *testing.T) {
	testErr := errors.New("sdk failure")
	tests := []struct {
		name    string
		alter   func(*fakeSDKClientBackend)
		want    string
		started bool
	}{
		{name: "start error", alter: func(f *fakeSDKClientBackend) { f.startErr = testErr }, want: "sdk failure"},
		{name: "status error", alter: func(f *fakeSDKClientBackend) { f.statusErr = testErr }, want: "sdk failure", started: true},
		{name: "missing status", alter: func(f *fakeSDKClientBackend) { f.status = nil }, want: "returned no status", started: true},
		{name: "version mismatch", alter: func(f *fakeSDKClientBackend) {
			f.status = &copilot.GetStatusResponse{Version: "v0.0.0"}
		}, want: "version mismatch", started: true},
		{name: "plugin reset error", alter: func(f *fakeSDKClientBackend) { f.pluginErr = testErr }, want: "sdk failure", started: true},
		{name: "success", started: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeSDKClientBackend{status: &copilot.GetStatusResponse{Version: "v" + runtimebundle.Version}}
			if test.alter != nil {
				test.alter(backend)
			}
			client := &sdkClient{client: backend, newTimer: realShutdownTimer}
			err := client.Start(t.Context())
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if backend.pluginCalls != 1 || backend.pluginPaths == nil || len(backend.pluginPaths) != 0 {
					t.Fatalf("trusted plugin set was not explicitly emptied: %#v", backend.pluginPaths)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if backend.startCalls != 1 || (backend.statusCalls > 0) != test.started {
				t.Fatalf("unexpected startup calls: start=%d status=%d", backend.startCalls, backend.statusCalls)
			}
			if test.want != "" && test.name != "plugin reset error" && backend.pluginCalls != 0 {
				t.Fatal("reset plugins after an earlier startup failure")
			}
		})
	}
}

func TestSDKClientOperationsAndSessionConstruction(t *testing.T) {
	session := newFakeSDKSessionBackend()
	backend := &fakeSDKClientBackend{
		status:   &copilot.GetStatusResponse{Version: runtimebundle.Version},
		models:   []copilot.ModelInfo{{ID: "model-a"}},
		sessions: []copilot.SessionMetadata{{SessionID: "session-a"}},
		session:  session,
	}
	client := &sdkClient{client: backend, newTimer: realShutdownTimer}
	if models, err := client.ListModels(t.Context()); err != nil || len(models) != 1 {
		t.Fatalf("models: %#v, %v", models, err)
	}
	if err := client.Health(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sessions, err := client.ListSessions(t.Context()); err != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %#v, %v", sessions, err)
	}
	created, err := client.CreateSession(t.Context(), &copilot.SessionConfig{SessionID: "session-a", Model: "model-a"})
	if err != nil || created.ID() != "session-a" {
		t.Fatalf("create: %#v, %v", created, err)
	}
	resumed, err := client.ResumeSession(t.Context(), "session-a", &copilot.ResumeSessionConfig{Model: "model-a"})
	if err != nil || resumed.ID() != "session-a" || backend.resumeID != "session-a" {
		t.Fatalf("resume: %#v, %v", resumed, err)
	}
	backend.createErr = errors.New("create failed")
	if _, err := client.CreateSession(t.Context(), &copilot.SessionConfig{}); err == nil {
		t.Fatal("create error was swallowed")
	}
	backend.resumeErr = errors.New("resume failed")
	if _, err := client.ResumeSession(t.Context(), "session-a", &copilot.ResumeSessionConfig{}); err == nil {
		t.Fatal("resume error was swallowed")
	}
}

func TestSDKClientStopReturnsErrorsAndForceStopsOnTimeout(t *testing.T) {
	stopErr := errors.New("stop failed")
	normal := &fakeSDKClientBackend{stopErr: stopErr}
	if err := (&sdkClient{client: normal, newTimer: realShutdownTimer}).Stop(); !errors.Is(err, stopErr) {
		t.Fatalf("normal stop error = %v", err)
	}

	release := make(chan struct{})
	timed := &fakeSDKClientBackend{stopErr: stopErr, stopWait: release, forceRelease: release}
	err := (&sdkClient{client: timed, newTimer: immediateShutdownTimer}).Stop()
	if !errors.Is(err, stopErr) || !strings.Contains(err.Error(), "shutdown timed out") || timed.forceStopCalls != 1 {
		t.Fatalf("timeout stop = %v, force calls=%d", err, timed.forceStopCalls)
	}
}

func TestSDKSessionConfigureInstallsEverySafetyBoundary(t *testing.T) {
	backend := newFakeSDKSessionBackend()
	session := &sdkSession{session: backend, selection: ModelSelection{
		ModelID: "model-a", ContextTier: "default",
	}}
	if err := session.Configure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if backend.permissionMode == nil || backend.permissionMode.Mode != rpc.PermissionModeManual {
		t.Fatal("manual permission mode was not required")
	}
	if backend.resetRequest == nil || backend.resetRequest.IncludeLocation == nil || *backend.resetRequest.IncludeLocation {
		t.Fatal("session approvals were not reset without location grants")
	}
	policy := backend.policyRequest
	if policy == nil || policy.ApproveAllReadPermissionRequests == nil || *policy.ApproveAllReadPermissionRequests ||
		policy.ApproveAllToolPermissionRequests == nil || *policy.ApproveAllToolPermissionRequests ||
		policy.Paths == nil || policy.Paths.Unrestricted == nil || *policy.Paths.Unrestricted ||
		policy.Paths.IncludeTempDirectory == nil || *policy.Paths.IncludeTempDirectory ||
		policy.Paths.AdditionalDirectories == nil || len(policy.Paths.AdditionalDirectories) != 0 ||
		policy.URLs == nil || policy.URLs.Unrestricted == nil || *policy.URLs.Unrestricted ||
		policy.URLs.InitialAllowed == nil || len(policy.URLs.InitialAllowed) != 0 {
		t.Fatalf("unsafe permission configuration: %#v", policy)
	}
	if backend.requiredRequest == nil || !backend.requiredRequest.Required {
		t.Fatal("permission event bridge was not required")
	}
}

func TestSDKSessionConfigureFailsClosedAtEveryStage(t *testing.T) {
	testErr := errors.New("stage failed")
	tests := []struct {
		name  string
		want  string
		alter func(*fakeSDKSessionBackend)
	}{
		{name: "read model", want: "read selected model", alter: func(f *fakeSDKSessionBackend) { f.currentErr = testErr }},
		{name: "wrong model", want: "implicit fallback", alter: func(f *fakeSDKSessionBackend) { f.current = nil }},
		{name: "set permission mode", want: "set manual permission mode", alter: func(f *fakeSDKSessionBackend) { f.modeErr = testErr }},
		{name: "permission mode rejected", want: "did not accept manual", alter: func(f *fakeSDKSessionBackend) { f.mode = nil }},
		{name: "reset approvals", want: "reset session permission grants", alter: func(f *fakeSDKSessionBackend) { f.resetErr = testErr }},
		{name: "reset rejected", want: "did not reset", alter: func(f *fakeSDKSessionBackend) { f.reset = nil }},
		{name: "configure boundaries", want: "configure permission boundaries", alter: func(f *fakeSDKSessionBackend) { f.policyErr = testErr }},
		{name: "boundaries rejected", want: "did not accept Sodapop permission boundaries", alter: func(f *fakeSDKSessionBackend) { f.policy = nil }},
		{name: "attach bridge", want: "attach permission event bridge", alter: func(f *fakeSDKSessionBackend) { f.requiredErr = testErr }},
		{name: "bridge rejected", want: "did not attach", alter: func(f *fakeSDKSessionBackend) { f.required = nil }},
		{name: "read interaction", want: "read session interaction mode", alter: func(f *fakeSDKSessionBackend) { f.interactionErr = testErr }},
		{name: "noninteractive", want: "requires an interactive session", alter: func(f *fakeSDKSessionBackend) { f.interaction = nil }},
		{name: "initialize tools", want: "initialize coding tools", alter: func(f *fakeSDKSessionBackend) { f.initializeErr = testErr }},
		{name: "inspect tools", want: "inspect effective coding tools", alter: func(f *fakeSDKSessionBackend) { f.toolsErr = testErr }},
		{name: "missing tools", want: "no effective tool metadata", alter: func(f *fakeSDKSessionBackend) { f.tools = nil }},
		{name: "unsafe tools", want: "unrequested tool", alter: func(f *fakeSDKSessionBackend) {
			f.tools = &rpc.ToolsGetCurrentMetadataResult{Tools: []rpc.CurrentToolMetadata{{Name: "danger"}}}
		}},
		{name: "inspect mcp", want: "inspect MCP isolation", alter: func(f *fakeSDKSessionBackend) { f.mcpErr = testErr }},
		{name: "missing mcp", want: "no MCP isolation metadata", alter: func(f *fakeSDKSessionBackend) { f.mcp = nil }},
		{name: "active mcp", want: "unexpected MCP server", alter: func(f *fakeSDKSessionBackend) {
			f.mcp = &rpc.MCPServerList{Servers: []rpc.MCPServer{{Name: "surprise", Status: rpc.MCPServerStatusConnected}}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeSDKSessionBackend()
			test.alter(backend)
			err := (&sdkSession{session: backend, selection: ModelSelection{
				ModelID: "model-a", ContextTier: "default",
			}}).Configure(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSDKSessionModelTokenAndPermissionOperationsFailClosed(t *testing.T) {
	t.Run("model", func(t *testing.T) {
		backend := newFakeSDKSessionBackend()
		session := &sdkSession{session: backend, selection: ModelSelection{
			ModelID: "model-a", ContextTier: "default",
		}}
		selection := ModelSelection{ModelID: "model-b", ContextTier: "long_context", ReasoningEffort: "high"}
		backend.setModelErr = errors.New("switch failed")
		if err := session.SetModel(t.Context(), selection); err == nil {
			t.Fatal("swallowed model switch failure")
		}
		backend.setModelErr = nil
		backend.currentErr = errors.New("read failed")
		if err := session.SetModel(t.Context(), selection); err == nil || !strings.Contains(err.Error(), "read selected model") {
			t.Fatalf("post-switch read error = %v", err)
		}
		backend.currentErr = nil
		backend.current = nil
		if err := session.SetModel(t.Context(), selection); err == nil {
			t.Fatal("accepted an unverified model switch")
		}
		model, effort, tier := "model-b", "high", rpc.ContextTierLongContext
		backend.current = &rpc.CurrentModel{ModelID: &model, ContextTier: &tier, ReasoningEffort: &effort}
		if err := session.SetModel(t.Context(), selection); err != nil || session.selection != selection ||
			backend.modelOptions == nil || backend.modelOptions.ContextTier == nil ||
			*backend.modelOptions.ContextTier != tier || backend.modelOptions.ReasoningEffort == nil ||
			*backend.modelOptions.ReasoningEffort != effort {
			t.Fatalf("verified model switch failed: %v", err)
		}
	})

	t.Run("token", func(t *testing.T) {
		backend := newFakeSDKSessionBackend()
		session := &sdkSession{session: backend}
		backend.credentialsErr = errors.New("credential RPC failed")
		if err := session.SetToken(t.Context(), "secret"); err == nil {
			t.Fatal("swallowed credential RPC failure")
		}
		backend.credentialsErr = nil
		backend.credentials = nil
		if err := session.SetToken(t.Context(), "secret"); err == nil {
			t.Fatal("accepted missing credential result")
		}
		backend.credentials = &rpc.SessionSetCredentialsResult{Success: true, CopilotUserResolved: copilot.Bool(false)}
		if err := session.SetToken(t.Context(), "secret"); err == nil || !strings.Contains(err.Error(), "account metadata") {
			t.Fatalf("unresolved account error = %v", err)
		}
		backend.credentials.CopilotUserResolved = nil
		if err := session.SetToken(t.Context(), "secret"); err != nil {
			t.Fatal(err)
		}
		credentials, ok := backend.credentialSet.Credentials.(*rpc.SettableTokenAuthInfo)
		if !ok || credentials.Host != "https://github.com" || credentials.Token != "secret" {
			t.Fatalf("wrong explicit credential: %#v", backend.credentialSet)
		}
	})

	t.Run("permission", func(t *testing.T) {
		backend := newFakeSDKSessionBackend()
		session := &sdkSession{session: backend}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := session.DecidePermission(ctx, "request", &rpc.PermissionDecisionReject{}, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled decision = %v", err)
		}
		backend.permissionErr = errors.New("decision failed")
		if err := session.DecidePermission(t.Context(), "request", &rpc.PermissionDecisionReject{}, nil); err == nil {
			t.Fatal("swallowed permission RPC error")
		}
		backend.permissionErr = nil
		backend.permission = nil
		if err := session.DecidePermission(t.Context(), "request", &rpc.PermissionDecisionReject{}, nil); !errors.Is(err, ErrAlreadyResolved) {
			t.Fatalf("missing permission result = %v", err)
		}
		backend.permission = &rpc.PermissionRequestResult{Success: true}
		attribution := &rpc.PermissionDecisionContext{}
		decision := &rpc.PermissionDecisionApproveOnce{}
		if err := session.DecidePermission(t.Context(), "request", decision, attribution); err != nil {
			t.Fatal(err)
		}
		if backend.permissionSet.RequestID != "request" || backend.permissionSet.Result != decision ||
			backend.permissionSet.DecisionContext != attribution {
			t.Fatalf("permission identity or attribution lost: %#v", backend.permissionSet)
		}
	})
}

func TestSDKSessionForwardsOperationsAndDisconnectForceStops(t *testing.T) {
	backend := newFakeSDKSessionBackend()
	clientBackend := &fakeSDKClientBackend{}
	client := &sdkClient{client: clientBackend, newTimer: realShutdownTimer}
	session := &sdkSession{session: backend, owner: client}
	options := copilot.MessageOptions{Prompt: "hello"}
	if id, err := session.Send(t.Context(), options); err != nil || id != "message-a" || backend.sent.Prompt != "hello" {
		t.Fatalf("send = %q, %v", id, err)
	}
	request := &rpc.SessionHistoryCompactRequest{}
	if result, err := session.Compact(t.Context(), request); err != nil || result != backend.compactResult || backend.compacted != request {
		t.Fatalf("compact = %#v, %v", result, err)
	}
	backend.contextResult = &rpc.MetadataContextAttributionResult{}
	if result, err := session.ContextAttribution(t.Context()); err != nil || result != backend.contextResult {
		t.Fatalf("context attribution = %#v, %v", result, err)
	}
	backend.heaviestResult = &rpc.MetadataContextHeaviestMessagesResult{}
	if result, err := session.ContextHeaviestMessages(t.Context(), 5); err != nil || result != backend.heaviestResult ||
		backend.heaviestRequest == nil || backend.heaviestRequest.Limit == nil || *backend.heaviestRequest.Limit != 5 {
		t.Fatalf("heaviest messages = %#v, %v, request=%#v", result, err, backend.heaviestRequest)
	}
	backend.events = []copilot.SessionEvent{{ID: "event-a"}}
	if events, err := session.GetEvents(t.Context()); err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if err := session.Abort(t.Context()); err != nil || backend.abortCalls != 1 {
		t.Fatalf("abort = %v, calls=%d", err, backend.abortCalls)
	}
	backend.disconnectErr = errors.New("detach failed")
	if err := session.Disconnect(); !errors.Is(err, backend.disconnectErr) {
		t.Fatalf("disconnect error = %v", err)
	}

	release := make(chan struct{})
	backend.disconnectWait = release
	clientBackend.forceRelease = release
	client.newTimer = immediateShutdownTimer
	err := session.Disconnect()
	if !errors.Is(err, backend.disconnectErr) || !strings.Contains(err.Error(), "detach timed out") ||
		clientBackend.forceStopCalls != 1 {
		t.Fatalf("timeout disconnect = %v, force calls=%d", err, clientBackend.forceStopCalls)
	}
}

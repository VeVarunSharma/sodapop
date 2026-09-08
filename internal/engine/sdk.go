package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/runtimebundle"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type runtimeClient interface {
	Start(context.Context) error
	Stop() error
	Health(context.Context) error
	ListModels(context.Context) ([]copilot.ModelInfo, error)
	CreateSession(context.Context, *copilot.SessionConfig) (runtimeSession, error)
	ResumeSession(context.Context, string, *copilot.ResumeSessionConfig) (runtimeSession, error)
	ListSessions(context.Context) ([]copilot.SessionMetadata, error)
}

type runtimeSession interface {
	ID() string
	Configure(context.Context) error
	Send(context.Context, copilot.MessageOptions) (string, error)
	Compact(context.Context, *rpc.SessionHistoryCompactRequest) (*rpc.HistoryCompactResult, error)
	ContextAttribution(context.Context) (*rpc.MetadataContextAttributionResult, error)
	ContextHeaviestMessages(context.Context, int64) (*rpc.MetadataContextHeaviestMessagesResult, error)
	GetEvents(context.Context) ([]copilot.SessionEvent, error)
	SetModel(context.Context, ModelSelection) error
	SetToken(context.Context, string) error
	DecidePermission(context.Context, string, rpc.PermissionDecision, *rpc.PermissionDecisionContext) error
	Abort(context.Context) error
	Disconnect() error
}

type sdkClientBackend interface {
	Start(context.Context) error
	Stop() error
	ForceStop()
	Status(context.Context) (*copilot.GetStatusResponse, error)
	SetBuiltinPlugins(context.Context, []string) error
	ListModels(context.Context) ([]copilot.ModelInfo, error)
	ListSessions(context.Context) ([]copilot.SessionMetadata, error)
	CreateSession(context.Context, *copilot.SessionConfig) (sdkSessionBackend, error)
	ResumeSession(context.Context, string, *copilot.ResumeSessionConfig) (sdkSessionBackend, error)
}

type sdkSessionBackend interface {
	ID() string
	CurrentModel(context.Context) (*rpc.CurrentModel, error)
	SetPermissionMode(context.Context, *rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error)
	ResetSessionApprovals(context.Context, *rpc.PermissionsResetSessionApprovalsRequest) (*rpc.PermissionsResetSessionApprovalsResult, error)
	ConfigurePermissions(context.Context, *rpc.PermissionsConfigureParams) (*rpc.PermissionsConfigureResult, error)
	SetPermissionsRequired(context.Context, *rpc.PermissionsSetRequiredRequest) (*rpc.PermissionsSetRequiredResult, error)
	InteractionMode(context.Context) (*rpc.SessionMode, error)
	InitializeTools(context.Context) error
	CurrentTools(context.Context) (*rpc.ToolsGetCurrentMetadataResult, error)
	MCPServers(context.Context) (*rpc.MCPServerList, error)
	Send(context.Context, copilot.MessageOptions) (string, error)
	Compact(context.Context, *rpc.SessionHistoryCompactRequest) (*rpc.HistoryCompactResult, error)
	ContextAttribution(context.Context) (*rpc.MetadataContextAttributionResult, error)
	ContextHeaviestMessages(context.Context, *rpc.MetadataContextHeaviestMessagesRequest) (*rpc.MetadataContextHeaviestMessagesResult, error)
	GetEvents(context.Context) ([]copilot.SessionEvent, error)
	SetModel(context.Context, string, *copilot.SetModelOptions) error
	SetCredentials(context.Context, *rpc.SessionSetCredentialsParams) (*rpc.SessionSetCredentialsResult, error)
	HandlePermission(context.Context, *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error)
	Abort(context.Context) error
	Disconnect() error
}

type shutdownTimer struct {
	done <-chan time.Time
	stop func()
}

type sdkClient struct {
	client   sdkClientBackend
	newTimer func(time.Duration) shutdownTimer
}

type sdkSession struct {
	session   sdkSessionBackend
	owner     *sdkClient
	selection ModelSelection
}

func makeSDKClient(options *copilot.ClientOptions) runtimeClient {
	return &sdkClient{client: &copilotClientBackend{client: copilot.NewClient(options)}, newTimer: realShutdownTimer}
}

type copilotClientBackend struct{ client *copilot.Client }

func (c *copilotClientBackend) Start(ctx context.Context) error { return c.client.Start(ctx) }
func (c *copilotClientBackend) Stop() error                     { return c.client.Stop() }
func (c *copilotClientBackend) ForceStop()                      { c.client.ForceStop() }
func (c *copilotClientBackend) Status(ctx context.Context) (*copilot.GetStatusResponse, error) {
	return c.client.GetStatus(ctx)
}
func (c *copilotClientBackend) SetBuiltinPlugins(ctx context.Context, paths []string) error {
	_, err := c.client.RPC.Plugins.Builtin().Set(ctx, &rpc.PluginsBuiltinSetRequest{Paths: paths})
	return err
}
func (c *copilotClientBackend) ListModels(ctx context.Context) ([]copilot.ModelInfo, error) {
	return c.client.ListModels(ctx)
}
func (c *copilotClientBackend) ListSessions(ctx context.Context) ([]copilot.SessionMetadata, error) {
	return c.client.ListSessions(ctx, nil)
}
func (c *copilotClientBackend) CreateSession(ctx context.Context, config *copilot.SessionConfig) (sdkSessionBackend, error) {
	session, err := c.client.CreateSession(ctx, config)
	if err != nil {
		return nil, err
	}
	return &copilotSessionBackend{session: session}, nil
}
func (c *copilotClientBackend) ResumeSession(ctx context.Context, id string, config *copilot.ResumeSessionConfig) (sdkSessionBackend, error) {
	session, err := c.client.ResumeSession(ctx, id, config)
	if err != nil {
		return nil, err
	}
	return &copilotSessionBackend{session: session}, nil
}

type copilotSessionBackend struct{ session *copilot.Session }

func (s *copilotSessionBackend) ID() string { return s.session.SessionID }
func (s *copilotSessionBackend) CurrentModel(ctx context.Context) (*rpc.CurrentModel, error) {
	return s.session.RPC.Model.GetCurrent(ctx)
}
func (s *copilotSessionBackend) SetPermissionMode(ctx context.Context, request *rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error) {
	return s.session.RPC.Permissions.SetMode(ctx, request)
}
func (s *copilotSessionBackend) ResetSessionApprovals(ctx context.Context, request *rpc.PermissionsResetSessionApprovalsRequest) (*rpc.PermissionsResetSessionApprovalsResult, error) {
	return s.session.RPC.Permissions.ResetSessionApprovals(ctx, request)
}
func (s *copilotSessionBackend) ConfigurePermissions(ctx context.Context, request *rpc.PermissionsConfigureParams) (*rpc.PermissionsConfigureResult, error) {
	return s.session.RPC.Permissions.Configure(ctx, request)
}
func (s *copilotSessionBackend) SetPermissionsRequired(ctx context.Context, request *rpc.PermissionsSetRequiredRequest) (*rpc.PermissionsSetRequiredResult, error) {
	return s.session.RPC.Permissions.SetRequired(ctx, request)
}
func (s *copilotSessionBackend) InteractionMode(ctx context.Context) (*rpc.SessionMode, error) {
	return s.session.RPC.Mode.Get(ctx)
}
func (s *copilotSessionBackend) InitializeTools(ctx context.Context) error {
	_, err := s.session.RPC.Tools.InitializeAndValidate(ctx)
	return err
}
func (s *copilotSessionBackend) CurrentTools(ctx context.Context) (*rpc.ToolsGetCurrentMetadataResult, error) {
	return s.session.RPC.Tools.GetCurrentMetadata(ctx)
}
func (s *copilotSessionBackend) MCPServers(ctx context.Context) (*rpc.MCPServerList, error) {
	return s.session.RPC.MCP.List(ctx)
}
func (s *copilotSessionBackend) Send(ctx context.Context, options copilot.MessageOptions) (string, error) {
	return s.session.Send(ctx, options)
}
func (s *copilotSessionBackend) Compact(ctx context.Context, request *rpc.SessionHistoryCompactRequest) (*rpc.HistoryCompactResult, error) {
	return s.session.RPC.History.Compact(ctx, request)
}
func (s *copilotSessionBackend) ContextAttribution(ctx context.Context) (*rpc.MetadataContextAttributionResult, error) {
	return s.session.RPC.Metadata.GetContextAttribution(ctx)
}
func (s *copilotSessionBackend) ContextHeaviestMessages(ctx context.Context, request *rpc.MetadataContextHeaviestMessagesRequest) (*rpc.MetadataContextHeaviestMessagesResult, error) {
	return s.session.RPC.Metadata.GetContextHeaviestMessages(ctx, request)
}
func (s *copilotSessionBackend) GetEvents(ctx context.Context) ([]copilot.SessionEvent, error) {
	return s.session.GetEvents(ctx)
}
func (s *copilotSessionBackend) SetModel(ctx context.Context, model string, options *copilot.SetModelOptions) error {
	return s.session.SetModel(ctx, model, options)
}
func (s *copilotSessionBackend) SetCredentials(ctx context.Context, request *rpc.SessionSetCredentialsParams) (*rpc.SessionSetCredentialsResult, error) {
	return s.session.RPC.GitHubAuth.SetCredentials(ctx, request)
}
func (s *copilotSessionBackend) HandlePermission(ctx context.Context, request *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error) {
	return s.session.RPC.Permissions.HandlePendingPermissionRequest(ctx, request)
}
func (s *copilotSessionBackend) Abort(ctx context.Context) error { return s.session.Abort(ctx) }
func (s *copilotSessionBackend) Disconnect() error               { return s.session.Disconnect() }

func realShutdownTimer(duration time.Duration) shutdownTimer {
	timer := time.NewTimer(duration)
	return shutdownTimer{done: timer.C, stop: func() { timer.Stop() }}
}

func (c *sdkClient) shutdownTimer() shutdownTimer {
	if c.newTimer == nil {
		return realShutdownTimer(10 * time.Second)
	}
	return c.newTimer(10 * time.Second)
}

func (c *sdkClient) Start(ctx context.Context) error {
	if err := c.client.Start(ctx); err != nil {
		return err
	}
	status, err := c.client.Status(ctx)
	if err != nil {
		return err
	}
	if status == nil {
		return errors.New("bundled runtime returned no status")
	}
	if strings.TrimPrefix(status.Version, "v") != runtimebundle.Version {
		return fmt.Errorf("bundled runtime version mismatch: expected %s, received %s", runtimebundle.Version, status.Version)
	}
	// An empty ClientOptions.BuiltinPluginDirectories slice is omitted by the
	// SDK. Replace the trusted set explicitly, before any session exists.
	return c.client.SetBuiltinPlugins(ctx, []string{})
}

func (c *sdkClient) Stop() error {
	done := make(chan error, 1)
	go func() { done <- c.client.Stop() }()
	timer := c.shutdownTimer()
	defer timer.stop()
	select {
	case err := <-done:
		return err
	case <-timer.done:
		c.client.ForceStop()
		return errors.Join(errors.New("graceful runtime shutdown timed out; stopped the owned child process"), <-done)
	}
}

func (c *sdkClient) ListModels(ctx context.Context) ([]copilot.ModelInfo, error) {
	return c.client.ListModels(ctx)
}

func (c *sdkClient) Health(ctx context.Context) error {
	// GetStatus does not call the SDK's ensureConnected/auto-start path.
	_, err := c.client.Status(ctx)
	return err
}

func (c *sdkClient) ListSessions(ctx context.Context) ([]copilot.SessionMetadata, error) {
	return c.client.ListSessions(ctx)
}

func (c *sdkClient) CreateSession(ctx context.Context, config *copilot.SessionConfig) (runtimeSession, error) {
	session, err := c.client.CreateSession(ctx, config)
	if err != nil {
		return nil, err
	}
	return &sdkSession{session: session, owner: c, selection: ModelSelection{
		ModelID: config.Model, ContextTier: string(config.ContextTier), ReasoningEffort: config.ReasoningEffort,
	}}, nil
}

func (c *sdkClient) ResumeSession(ctx context.Context, id string, config *copilot.ResumeSessionConfig) (runtimeSession, error) {
	session, err := c.client.ResumeSession(ctx, id, config)
	if err != nil {
		return nil, err
	}
	return &sdkSession{session: session, owner: c, selection: ModelSelection{
		ModelID: config.Model, ContextTier: string(config.ContextTier), ReasoningEffort: config.ReasoningEffort,
	}}, nil
}

func (s *sdkSession) ID() string { return s.session.ID() }

func (s *sdkSession) ContextAttribution(ctx context.Context) (*rpc.MetadataContextAttributionResult, error) {
	return s.session.ContextAttribution(ctx)
}

func (s *sdkSession) ContextHeaviestMessages(ctx context.Context, limit int64) (*rpc.MetadataContextHeaviestMessagesResult, error) {
	return s.session.ContextHeaviestMessages(ctx, &rpc.MetadataContextHeaviestMessagesRequest{Limit: &limit})
}

func (s *sdkSession) Configure(ctx context.Context) error {
	selected, err := s.session.CurrentModel(ctx)
	if err != nil {
		return fmt.Errorf("read selected model: %w", err)
	}
	if !currentSelectionMatches(selected, s.selection) {
		return errors.New("runtime selected a different model; refusing to use an implicit fallback")
	}
	mode, err := s.session.SetPermissionMode(ctx, &rpc.PermissionsSetModeRequest{Mode: rpc.PermissionModeManual})
	if err != nil {
		return fmt.Errorf("set manual permission mode: %w", err)
	}
	if mode == nil || !mode.Success || mode.Mode != rpc.PermissionModeManual {
		return errors.New("runtime did not accept manual permission mode")
	}
	reset, err := s.session.ResetSessionApprovals(ctx, &rpc.PermissionsResetSessionApprovalsRequest{IncludeLocation: copilot.Bool(false)})
	if err != nil {
		return fmt.Errorf("reset session permission grants: %w", err)
	}
	if reset == nil || !reset.Success {
		return errors.New("runtime did not reset session permission grants")
	}
	policy, err := s.session.ConfigurePermissions(ctx, &rpc.PermissionsConfigureParams{
		ApproveAllReadPermissionRequests: copilot.Bool(false),
		ApproveAllToolPermissionRequests: copilot.Bool(false),
		Paths: &rpc.PermissionPathsConfig{
			AdditionalDirectories: []string{}, IncludeTempDirectory: copilot.Bool(false),
			Unrestricted: copilot.Bool(false),
		},
		URLs: &rpc.PermissionURLsConfig{InitialAllowed: []string{}, Unrestricted: copilot.Bool(false)},
	})
	if err != nil {
		return fmt.Errorf("configure permission boundaries: %w", err)
	}
	if policy == nil || !policy.Success {
		return errors.New("runtime did not accept Sodapop permission boundaries")
	}
	required, err := s.session.SetPermissionsRequired(ctx, &rpc.PermissionsSetRequiredRequest{Required: true})
	if err != nil {
		return fmt.Errorf("attach permission event bridge: %w", err)
	}
	if required == nil || !required.Success {
		return errors.New("runtime did not attach the permission bridge")
	}
	interaction, err := s.session.InteractionMode(ctx)
	if err != nil {
		return fmt.Errorf("read session interaction mode: %w", err)
	}
	if interaction == nil || *interaction != rpc.SessionModeInteractive {
		return errors.New("Sodapop requires an interactive session, not native plan or autopilot mode")
	}
	if err := s.session.InitializeTools(ctx); err != nil {
		return fmt.Errorf("initialize coding tools: %w", err)
	}
	tools, err := s.session.CurrentTools(ctx)
	if err != nil {
		return fmt.Errorf("inspect effective coding tools: %w", err)
	}
	if tools == nil {
		return errors.New("runtime returned no effective tool metadata")
	}
	if err := validateTools(tools.Tools); err != nil {
		return err
	}
	mcp, err := s.session.MCPServers(ctx)
	if err != nil {
		return fmt.Errorf("inspect MCP isolation: %w", err)
	}
	if mcp == nil {
		return errors.New("runtime returned no MCP isolation metadata")
	}
	for _, server := range mcp.Servers {
		if string(server.Status) != "disabled" && string(server.Status) != "not_configured" {
			return fmt.Errorf("runtime has unexpected MCP server %q (%s); refusing to start a coding turn", server.Name, server.Status)
		}
	}
	return nil
}

func (s *sdkSession) Send(ctx context.Context, options copilot.MessageOptions) (string, error) {
	return s.session.Send(ctx, options)
}

func (s *sdkSession) Compact(ctx context.Context, request *rpc.SessionHistoryCompactRequest) (*rpc.HistoryCompactResult, error) {
	return s.session.Compact(ctx, request)
}

func (s *sdkSession) GetEvents(ctx context.Context) ([]copilot.SessionEvent, error) {
	return s.session.GetEvents(ctx)
}

func (s *sdkSession) SetModel(ctx context.Context, selection ModelSelection) error {
	tier := copilot.ContextTier(selection.ContextTier)
	options := &copilot.SetModelOptions{ContextTier: &tier}
	if selection.ReasoningEffort != "" {
		options.ReasoningEffort = &selection.ReasoningEffort
	}
	if err := s.session.SetModel(ctx, selection.ModelID, options); err != nil {
		return err
	}
	current, err := s.session.CurrentModel(ctx)
	if err != nil {
		return fmt.Errorf("read selected model after switch: %w", err)
	}
	if !currentSelectionMatches(current, selection) {
		return errors.New("runtime did not apply the requested model, context tier, and reasoning effort")
	}
	s.selection = selection
	return nil
}

func currentSelectionMatches(current *rpc.CurrentModel, selection ModelSelection) bool {
	if current == nil || current.ModelID == nil || *current.ModelID != selection.ModelID {
		return false
	}
	tier := "default"
	if current.ContextTier != nil && *current.ContextTier != "" {
		tier = string(*current.ContextTier)
	}
	if tier != selection.ContextTier {
		return false
	}
	return selection.ReasoningEffort == "" ||
		current.ReasoningEffort != nil && *current.ReasoningEffort == selection.ReasoningEffort
}

func (s *sdkSession) SetToken(ctx context.Context, token string) error {
	result, err := s.session.SetCredentials(ctx, &rpc.SessionSetCredentialsParams{
		Credentials: &rpc.SettableTokenAuthInfo{Host: "https://github.com", Token: token},
	})
	if err != nil {
		return err
	}
	if result == nil || !result.Success {
		return errors.New("runtime did not install the refreshed Sodapop credential")
	}
	if result.CopilotUserResolved != nil && !*result.CopilotUserResolved {
		return errors.New("Sodapop credential changed, but Copilot account metadata could not be resolved; reconnect before continuing")
	}
	return nil
}

func (s *sdkSession) DecidePermission(ctx context.Context, id string, decision rpc.PermissionDecision, attribution *rpc.PermissionDecisionContext) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := s.session.HandlePermission(ctx, &rpc.PermissionDecisionRequest{
		RequestID: id, Result: decision, DecisionContext: attribution,
	})
	if err != nil {
		return err
	}
	if result == nil || !result.Success {
		return ErrAlreadyResolved
	}
	return nil
}

func (s *sdkSession) Abort(ctx context.Context) error { return s.session.Abort(ctx) }

func (s *sdkSession) Disconnect() error {
	done := make(chan error, 1)
	go func() { done <- s.session.Disconnect() }()
	timer := s.owner.shutdownTimer()
	defer timer.stop()
	select {
	case err := <-done:
		return err
	case <-timer.done:
		s.owner.client.ForceStop()
		return errors.Join(errors.New("session detach timed out; stopped the owned runtime to prevent continued execution"), <-done)
	}
}

var codingTools = []string{
	"view", "glob", "grep", "rg", "edit", "create", "apply_patch", "str_replace_editor",
	"bash", "read_bash", "write_bash", "stop_bash", "list_bash", "ask_user", "task_complete",
}

func validateTools(tools []rpc.CurrentToolMetadata) error {
	allowed := make(map[string]bool, len(codingTools))
	for _, tool := range codingTools {
		allowed[tool] = true
	}
	found := make(map[string]bool)
	for _, tool := range tools {
		if !allowed[tool.Name] || tool.MCPServerName != nil || tool.MCPToolName != nil {
			return fmt.Errorf("runtime exposed an unrequested tool %q; refusing to send", tool.Name)
		}
		found[tool.Name] = true
	}
	if !found["view"] || !found["bash"] || !found["ask_user"] ||
		!(found["edit"] || found["create"] || found["apply_patch"] || found["str_replace_editor"]) {
		return errors.New("runtime does not expose the required read, write, shell, and question tools")
	}
	return nil
}

func runtimeEnvironment() []string {
	values := runtimebundle.Environment()
	result := make([]string, 0, len(values))
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "COPILOT_") || strings.HasPrefix(key, "GH_") ||
			strings.HasPrefix(key, "GITHUB_") || strings.HasPrefix(key, "SODAPOP_") ||
			strings.HasPrefix(key, "OTEL_") || strings.HasPrefix(key, "OPENAI_") ||
			strings.HasPrefix(key, "AZURE_OPENAI_") || strings.HasPrefix(key, "ANTHROPIC_") ||
			key == "NODE_OPTIONS" || key == "NODE_PATH" || key == "GOOGLE_API_KEY" || key == "GEMINI_API_KEY" {
			continue
		}
		result = append(result, value)
	}
	return result
}

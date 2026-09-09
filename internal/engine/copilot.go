package engine

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/VeVarunSharma/sodapop/internal/runtimebundle"
	"github.com/VeVarunSharma/sodapop/internal/securefs"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// Copilot owns one bundled SDK child process and at most one connected session.
// Config.TokenSource must continue to represent Config.AccountID for its lifetime.
type Copilot struct {
	cfg        Config
	deps       dependencies
	index      *sessionIndex
	stream     *eventStream
	redact     *redactor
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	client     runtimeClient
	active     *liveSession
	started    bool
	closed     bool
	operation  bool
	aborting   bool
	opDone     chan struct{}
	opCancel   context.CancelFunc
	operations sync.WaitGroup
	closeOnce  sync.Once
	closeErr   error
	failure    error
	monitor    sync.WaitGroup
}

type dependencies struct {
	bundlePath  func() (string, error)
	client      func(*copilot.ClientOptions) runtimeClient
	now         func() time.Time
	healthEvery time.Duration
}

var _ Engine = (*Copilot)(nil)

func New(cfg Config) (*Copilot, error) {
	return newCopilot(cfg, dependencies{bundlePath: runtimebundle.Path, client: makeSDKClient, now: time.Now, healthEvery: 5 * time.Second})
}

func newCopilot(cfg Config, deps dependencies) (*Copilot, error) {
	if strings.TrimSpace(cfg.AccountID) == "" {
		return nil, errors.New("Sodapop account identity is required")
	}
	for _, character := range cfg.AccountID {
		if unicode.IsControl(character) {
			return nil, errors.New("Sodapop account identity contains control characters")
		}
	}
	if cfg.TokenSource == nil {
		return nil, ErrNoToken
	}
	if err := validateMCPServers(cfg.MCPServers); err != nil {
		return nil, err
	}
	if err := validateSkills(cfg.Skills, cfg.ActiveSkillDigests); err != nil {
		return nil, err
	}
	project, err := canonicalDirectory(cfg.Project)
	if err != nil {
		return nil, fmt.Errorf("open Sodapop project: %w", err)
	}
	if strings.TrimSpace(cfg.Home) == "" {
		return nil, errors.New("an isolated Sodapop data directory is required")
	}
	home, err := filepath.Abs(cfg.Home)
	if err != nil {
		return nil, fmt.Errorf("resolve Sodapop data directory: %w", err)
	}
	// Never point the runtime at the user's ordinary Copilot state.
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate user home for data isolation: %w", err)
	}
	defaultHome := filepath.Join(userHome, ".copilot")
	if withinDirectory(defaultHome, filepath.Clean(home)) {
		return nil, errors.New("Sodapop data must not be stored inside ~/.copilot")
	}
	if err := securefs.MkdirAllPrivate(home); err != nil {
		return nil, fmt.Errorf("create Sodapop data directory: %w", err)
	}
	home, err = canonicalDirectory(home)
	if err != nil {
		return nil, fmt.Errorf("open Sodapop data directory: %w", err)
	}
	if resolved, resolveErr := canonicalDirectory(defaultHome); resolveErr == nil && withinDirectory(resolved, home) {
		return nil, errors.New("Sodapop data must not resolve inside ~/.copilot")
	}
	cfg.Project, cfg.Home = project, home
	for index := range cfg.Skills {
		directory, err := canonicalDirectory(cfg.Skills[index].Directory)
		if err != nil {
			return nil, fmt.Errorf("open Sodapop skill %q: %w", cfg.Skills[index].Name, err)
		}
		cfg.Skills[index].Directory = directory
	}
	if deps.healthEvery <= 0 {
		deps.healthEvery = 5 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Copilot{
		cfg: cfg, deps: deps, index: &sessionIndex{home: home, project: project, account: cfg.AccountID},
		stream: newEventStream(), redact: &redactor{}, ctx: ctx, cancel: cancel,
	}, nil
}

func validateSkills(skills []Skill, active []string) error {
	names, digests := make(map[string]bool, len(skills)), make(map[string]bool, len(skills))
	for _, skill := range skills {
		if strings.TrimSpace(skill.Name) == "" || skill.Name != strings.TrimSpace(skill.Name) ||
			strings.IndexFunc(skill.Name, unicode.IsControl) >= 0 {
			return errors.New("skill requires a valid name")
		}
		if len(skill.Digest) != 64 || strings.ToLower(skill.Digest) != skill.Digest {
			return fmt.Errorf("skill %q has an invalid digest", skill.Name)
		}
		if _, err := hex.DecodeString(skill.Digest); err != nil {
			return fmt.Errorf("skill %q has an invalid digest", skill.Name)
		}
		if !filepath.IsAbs(skill.Directory) {
			return fmt.Errorf("skill %q requires an absolute directory", skill.Name)
		}
		nameKey := strings.ToLower(skill.Name)
		if names[nameKey] || digests[skill.Digest] {
			return fmt.Errorf("skill %q is duplicated", skill.Name)
		}
		names[nameKey], digests[skill.Digest] = true, true
	}
	enabled := make(map[string]bool, len(active))
	for _, digest := range active {
		if !digests[digest] {
			return fmt.Errorf("active skill digest %q is not installed", digest)
		}
		if enabled[digest] {
			return fmt.Errorf("active skill digest %q is duplicated", digest)
		}
		enabled[digest] = true
	}
	return nil
}

func (c *Copilot) skillDirectories(digests []string) ([]string, error) {
	byDigest := make(map[string]Skill, len(c.cfg.Skills))
	for _, skill := range c.cfg.Skills {
		byDigest[skill.Digest] = skill
	}
	directories := make([]string, 0, len(digests))
	for _, digest := range digests {
		skill, ok := byDigest[digest]
		if !ok {
			return nil, fmt.Errorf("session requires missing skill digest %s; reinstall that exact version before resuming", digest)
		}
		manifest := filepath.Join(skill.Directory, skill.Name, "SKILL.md")
		info, err := os.Lstat(manifest)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("session skill %q is missing or invalid", skill.Name)
		}
		directories = append(directories, skill.Directory)
	}
	return directories, nil
}

// reserve serializes public mutations without holding mu across filesystem,
// SDK, or UI operations. Abort can cancel and wait for the reservation.
func (c *Copilot) reserve(ctx context.Context, requireStart, requireIdle bool) (context.Context, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, ErrClosed
	}
	if c.failure != nil && requireStart {
		return nil, nil, c.failure
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if requireStart && !c.started {
		return nil, nil, ErrNotStarted
	}
	if c.operation || c.aborting || (requireIdle && c.active != nil && c.active.isBusy()) {
		return nil, nil, ErrBusy
	}
	ctx, cancel := linkedContext(ctx, c.ctx)
	c.operation, c.opCancel, c.opDone = true, cancel, make(chan struct{})
	c.operations.Add(1)
	return ctx, func() {
		cancel()
		c.mu.Lock()
		c.operation = false
		c.opCancel = nil
		close(c.opDone)
		c.mu.Unlock()
		c.operations.Done()
	}, nil
}

func (c *Copilot) token(ctx context.Context) (string, error) {
	token, err := c.cfg.TokenSource(ctx)
	c.redact.remember(token)
	if err != nil {
		return "", c.redact.err("obtain Sodapop authentication; reconnect your Sodapop account", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if token == "" {
		return "", ErrNoToken
	}
	for _, character := range token {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return "", errors.New("Sodapop authentication returned an invalid token")
		}
	}
	return token, nil
}

func (c *Copilot) Start(ctx context.Context) error {
	ctx, done, err := c.reserve(ctx, false, false)
	if err != nil {
		return err
	}
	defer done()
	c.mu.Lock()
	failure, started := c.failure, c.started
	c.mu.Unlock()
	if failure != nil {
		return failure
	}
	if started {
		return nil
	}
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	path, err := c.deps.bundlePath()
	if err != nil {
		return c.redact.err("locate bundled Copilot runtime", err)
	}
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("bundled Copilot runtime path is missing or not absolute; no installed CLI fallback is allowed")
	}
	client := c.deps.client(&copilot.ClientOptions{
		Connection: copilot.StdioConnection{Path: path}, WorkingDirectory: c.cfg.Project,
		BaseDirectory: c.cfg.Home, GitHubToken: token, UseLoggedInUser: copilot.Bool(false),
		Mode: copilot.ModeEmpty, Env: runtimeEnvironment(), LogLevel: "none",
		EnableRemoteSessions: false,
		ClientInfo:           &copilot.ClientInfo{ApplicationName: "Sodapop", IntegrationName: "Sodapop Go SDK"},
	})
	if err := client.Start(ctx); err != nil {
		return c.redact.err("start bundled Copilot runtime", errors.Join(err, client.Stop()))
	}
	if err := ctx.Err(); err != nil {
		return c.redact.err("start bundled Copilot runtime", errors.Join(err, client.Stop()))
	}
	c.mu.Lock()
	c.client, c.started = client, true
	c.monitor.Add(1)
	c.mu.Unlock()
	go c.monitorRuntime(client)
	return nil
}

func (c *Copilot) models(ctx context.Context) ([]Model, error) {
	models, err := c.client.ListModels(ctx)
	if err != nil {
		return nil, c.redact.err("list Copilot models", err)
	}
	seen := make(map[string]bool)
	var result []Model
	for _, model := range models {
		if err := validateModelID(model.ID); err != nil || seen[model.ID] {
			return nil, errors.New("runtime returned an invalid or duplicate model ID")
		}
		seen[model.ID] = true
		if model.Policy != nil && strings.EqualFold(model.Policy.State, "disabled") {
			continue
		}
		name := model.Name
		if name == "" {
			name = model.ID
		}
		result = append(result, mapModel(model, c.redact.text(name)))
	}
	if len(result) == 0 {
		return nil, ErrNoModels
	}
	return result, nil
}

func mapModel(model copilot.ModelInfo, name string) Model {
	result := Model{
		ID: model.ID, Name: name, Family: modelFamily(name, model.ID),
		DefaultContextTier: "default", DefaultReasoningEffort: model.DefaultReasoningEffort,
		ContextOptions: []ContextOption{{Tier: "default", Tokens: defaultContextTokens(model)}},
	}
	if model.Billing != nil && model.Billing.TokenPrices != nil && model.Billing.TokenPrices.LongContext != nil {
		long := model.Billing.TokenPrices.LongContext
		result.ContextOptions = append(result.ContextOptions, ContextOption{
			Tier: "long_context", Tokens: firstPositive(long.MaxPromptTokens, long.ContextMax),
		})
	}
	if model.Capabilities.Supports.ReasoningEffort {
		result.ReasoningEfforts = orderedEfforts(model.SupportedReasoningEfforts, model.DefaultReasoningEffort)
	}
	return result
}

func defaultContextTokens(model copilot.ModelInfo) int64 {
	if model.Billing != nil && model.Billing.TokenPrices != nil {
		if tokens := firstPositive(model.Billing.TokenPrices.MaxPromptTokens, model.Billing.TokenPrices.ContextMax); tokens > 0 {
			return tokens
		}
	}
	if model.Capabilities.Limits.MaxContextWindowTokens != nil && *model.Capabilities.Limits.MaxContextWindowTokens > 0 {
		return int64(*model.Capabilities.Limits.MaxContextWindowTokens)
	}
	return 0
}

func firstPositive(values ...*int64) int64 {
	for _, value := range values {
		if value != nil && *value > 0 {
			return *value
		}
	}
	return 0
}

func orderedEfforts(values []string, defaultValue string) []string {
	seen := make(map[string]bool)
	for _, value := range values {
		if value != "" {
			seen[value] = true
		}
	}
	if defaultValue != "" {
		seen[defaultValue] = true
	}
	var result []string
	for _, value := range []string{"none", "low", "medium", "high", "xhigh", "max"} {
		if seen[value] {
			result = append(result, value)
			delete(seen, value)
		}
	}
	for _, value := range values {
		if seen[value] {
			result = append(result, value)
			delete(seen, value)
		}
	}
	return result
}

func modelFamily(name, id string) string {
	value := strings.ToLower(name + " " + id)
	switch {
	case strings.Contains(value, "claude"):
		return "Anthropic"
	case strings.Contains(value, "gemini"):
		return "Google"
	case strings.Contains(value, "grok"):
		return "xAI"
	case strings.Contains(value, "gpt") || strings.Contains(value, "o1") || strings.Contains(value, "o3") || strings.Contains(value, "o4"):
		return "OpenAI"
	default:
		return "Other"
	}
}

func (c *Copilot) Models(ctx context.Context) ([]Model, error) {
	ctx, done, err := c.reserve(ctx, true, false)
	if err != nil {
		return nil, err
	}
	defer done()
	if _, err := c.token(ctx); err != nil {
		return nil, err
	}
	return c.models(ctx)
}

func (c *Copilot) validateSelection(ctx context.Context, selection ModelSelection) error {
	if err := validateModelID(selection.ModelID); err != nil {
		return err
	}
	models, err := c.models(ctx)
	if err != nil {
		return err
	}
	for _, model := range models {
		if model.ID != selection.ModelID {
			continue
		}
		contextOK := false
		for _, option := range model.ContextOptions {
			if option.Tier == selection.ContextTier {
				contextOK = true
				break
			}
		}
		if !contextOK {
			return fmt.Errorf("context tier %q is not available for model %q", selection.ContextTier, c.redact.text(selection.ModelID))
		}
		if selection.ReasoningEffort == "" {
			return nil
		}
		for _, effort := range model.ReasoningEfforts {
			if effort == selection.ReasoningEffort {
				return nil
			}
		}
		return fmt.Errorf("reasoning effort %q is not available for model %q", selection.ReasoningEffort, c.redact.text(selection.ModelID))
	}
	return fmt.Errorf("model %q is not available for this Sodapop account", c.redact.text(selection.ModelID))
}

func (c *Copilot) NewSession(ctx context.Context, selection ModelSelection) (Session, error) {
	if err := validateModelID(selection.ModelID); err != nil {
		return Session{}, err
	}
	ctx, done, err := c.reserve(ctx, true, true)
	if err != nil {
		return Session{}, err
	}
	defer done()
	token, err := c.token(ctx)
	if err != nil {
		return Session{}, err
	}
	if err := c.validateSelection(ctx, selection); err != nil {
		return Session{}, err
	}
	id, err := newSessionID()
	if err != nil {
		return Session{}, err
	}
	meta := Session{
		ID: id, Project: c.cfg.Project, Model: selection.ModelID,
		ContextTier: selection.ContextTier, ReasoningEffort: selection.ReasoningEffort,
		SkillDigests: append([]string(nil), c.cfg.ActiveSkillDigests...),
		Title:        "New conversation", UpdatedAt: c.deps.now().UTC(),
	}
	return c.openSession(ctx, meta, token, false)
}

func (c *Copilot) ResumeSession(ctx context.Context, id string) (Session, error) {
	ctx, done, err := c.reserve(ctx, true, true)
	if err != nil {
		return Session{}, err
	}
	defer done()
	meta, err := c.index.find(ctx, id)
	if err != nil {
		return Session{}, c.redact.err("resume Sodapop session", err)
	}
	token, err := c.token(ctx)
	if err != nil {
		return Session{}, err
	}
	if err := c.validateSelection(ctx, ModelSelection{
		ModelID: meta.Model, ContextTier: meta.ContextTier, ReasoningEffort: meta.ReasoningEffort,
	}); err != nil {
		return Session{}, err
	}
	records, err := c.client.ListSessions(ctx)
	if err != nil {
		return Session{}, c.redact.err("find stored Copilot session", err)
	}
	matching := false
	for _, record := range records {
		if record.SessionID != id {
			continue
		}
		if record.IsRemote || record.Context == nil {
			return Session{}, errors.New("stored Copilot session is remote or has no project identity")
		}
		project, err := canonicalDirectory(record.Context.WorkingDirectory)
		if err != nil || project != c.cfg.Project {
			return Session{}, errors.New("stored Copilot session belongs to a different or missing project")
		}
		matching = true
		break
	}
	if !matching {
		return Session{}, errors.New("Sodapop session history is no longer available in the bundled runtime")
	}
	return c.openSession(ctx, meta, token, true)
}

func (c *Copilot) openSession(ctx context.Context, meta Session, token string, resume bool) (Session, error) {
	instructions, err := projectInstructions(c.cfg.Project)
	if err != nil {
		return Session{}, c.redact.err("load project instructions", err)
	}
	c.mu.Lock()
	old := c.active
	c.active = nil
	c.mu.Unlock()
	if old != nil {
		old.stop()
		if err := old.runtime.Disconnect(); err != nil {
			c.stream.activate(nil)
			return Session{}, c.redact.err("disconnect previous conversation (history was preserved)", err)
		}
	}
	session := newLiveSession(c.ctx, meta, c.stream, c.redact)
	session.resuming = resume
	c.stream.activate(session)
	c.mu.Lock()
	c.active = session
	c.mu.Unlock()
	skillDirectories, err := c.skillDirectories(meta.SkillDigests)
	if err != nil {
		return Session{}, err
	}
	config := sessionConfig(session, c.cfg.Home, token, instructions, c.cfg.MCPServers, skillDirectories)
	ctx, cancel := linkedContext(ctx, session.ctx)
	defer cancel()
	var runtime runtimeSession
	if resume {
		runtime, err = c.client.ResumeSession(ctx, meta.ID, resumeConfig(config))
	} else {
		runtime, err = c.client.CreateSession(ctx, config)
	}
	if err == nil && (runtime == nil || runtime.ID() != meta.ID) {
		err = errors.New("runtime returned a different session identity")
	}
	if err == nil {
		session.setRuntime(runtime, token)
		err = runtime.Configure(ctx)
	}
	var history []copilot.SessionEvent
	if err == nil && resume {
		history, err = runtime.GetEvents(ctx)
	}
	if err == nil {
		session.mu.Lock()
		err = session.bootstrapErr
		session.mu.Unlock()
	}
	meta.UpdatedAt = c.deps.now().UTC()
	if err == nil {
		err = c.index.save(ctx, meta)
	}
	if err == nil {
		session.mu.Lock()
		session.meta = meta
		session.mu.Unlock()
		err = session.activate(history, resume)
	}
	if err != nil {
		session.mu.Lock()
		err = errors.Join(err, session.bootstrapErr)
		session.mu.Unlock()
		session.stop()
		if runtime != nil {
			err = errors.Join(err, runtime.Disconnect())
		}
		c.mu.Lock()
		if c.active == session {
			c.active = nil
		}
		c.mu.Unlock()
		c.stream.activate(nil)
		return Session{}, c.redact.err("open controlled Copilot session", err)
	}
	return meta, nil
}

func (c *Copilot) Sessions(ctx context.Context) ([]Session, error) {
	ctx, done, err := c.reserve(ctx, false, false)
	if err != nil {
		return nil, err
	}
	defer done()
	sessions, err := c.index.list(ctx)
	return sessions, c.redact.err("list Sodapop conversations", err)
}

func (c *Copilot) refreshSessionToken(ctx context.Context, session *liveSession) error {
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	session.mu.Lock()
	previous, runtime := session.token, session.runtime
	session.mu.Unlock()
	if previous == token {
		return nil
	}
	if err := runtime.SetToken(ctx, token); err != nil {
		return c.redact.err("update Copilot session authentication", err)
	}
	session.mu.Lock()
	session.token = token
	session.mu.Unlock()
	return nil
}

func (c *Copilot) Send(ctx context.Context, message Message) error {
	options, err := messageOptions(message)
	if err != nil {
		return err
	}
	ctx, done, err := c.reserve(ctx, true, true)
	if err != nil {
		return err
	}
	defer done()
	c.mu.Lock()
	session := c.active
	c.mu.Unlock()
	if session == nil {
		return ErrNoSession
	}
	turn, err := session.startTurn()
	if err != nil {
		return err
	}
	ctx, cancel := linkedContext(ctx, turn)
	defer cancel()
	submitted := false
	defer func() {
		if err != nil {
			session.sendFailed(submitted)
		}
	}()
	if err = c.refreshSessionToken(ctx, session); err != nil {
		return err
	}
	meta := session.snapshot()
	if meta.Title == "New conversation" {
		title := []rune(strings.Join(strings.Fields(message.Text), " "))
		if len(title) > 80 {
			title = title[:80]
		}
		meta.Title = c.redact.text(string(title))
	}
	meta.UpdatedAt = c.deps.now().UTC()
	if err = c.index.save(ctx, meta); err != nil {
		return c.redact.err("save session metadata before sending", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	submitted = true
	_, err = session.runtime.Send(ctx, options)
	if err != nil {
		return c.redact.err("send prompt; delivery may have occurred, so cancel before trying again", err)
	}
	session.mu.Lock()
	session.meta = meta
	session.mu.Unlock()
	return nil
}

func (c *Copilot) Compact(ctx context.Context, instructions string) (CompactResult, error) {
	if strings.IndexByte(instructions, 0) >= 0 {
		return CompactResult{}, errors.New("compaction instructions must not contain NUL characters")
	}
	ctx, done, err := c.reserve(ctx, true, true)
	if err != nil {
		return CompactResult{}, err
	}
	defer done()
	c.mu.Lock()
	session := c.active
	c.mu.Unlock()
	if session == nil {
		return CompactResult{}, ErrNoSession
	}
	if err := c.refreshSessionToken(ctx, session); err != nil {
		return CompactResult{}, err
	}
	trigger := rpc.SessionHistoryCompactRequestTriggerManual
	request := &rpc.SessionHistoryCompactRequest{Trigger: &trigger}
	if instructions != "" {
		request.CustomInstructions = copilot.String(instructions)
	}
	compacted, err := session.runtime.Compact(ctx, request)
	if err != nil {
		return CompactResult{}, c.redact.err("compact Copilot conversation", err)
	}
	if compacted == nil {
		return CompactResult{}, errors.New("Copilot returned no compaction result")
	}
	result := CompactResult{
		Success:         compacted.Success,
		MessagesRemoved: compacted.MessagesRemoved,
		TokensRemoved:   compacted.TokensRemoved,
	}
	if compacted.ContextWindow != nil {
		result.CurrentTokens = compacted.ContextWindow.CurrentTokens
		result.TokenLimit = compacted.ContextWindow.TokenLimit
		result.MessagesRemaining = compacted.ContextWindow.MessagesLength
	}
	if !result.Success {
		return result, errors.New("Copilot did not compact the conversation")
	}
	meta := session.snapshot()
	meta.UpdatedAt = c.deps.now().UTC()
	session.mu.Lock()
	session.meta = meta
	session.mu.Unlock()
	if err := c.index.save(ctx, meta); err != nil {
		return result, c.redact.err("store compacted session metadata (conversation was already compacted)", err)
	}
	return result, nil
}

func (c *Copilot) Context(ctx context.Context) (ContextUsage, error) {
	ctx, done, err := c.reserve(ctx, true, false)
	if err != nil {
		return ContextUsage{}, err
	}
	defer done()
	c.mu.Lock()
	session := c.active
	c.mu.Unlock()
	if session == nil {
		return ContextUsage{}, ErrNoSession
	}
	meta := session.snapshot()
	if strings.Contains(strings.ToLower(meta.Model), "hydrafusion") {
		return ContextUsage{}, ErrVariableContext
	}
	if err := c.refreshSessionToken(ctx, session); err != nil {
		return ContextUsage{}, err
	}
	result, err := session.runtime.ContextAttribution(ctx)
	if err != nil {
		return ContextUsage{}, c.redact.err("read Copilot context usage", err)
	}
	if result == nil || result.ContextAttribution == nil {
		return ContextUsage{}, ErrContextUnavailable
	}
	attribution := result.ContextAttribution
	if attribution.Limit <= 0 || attribution.TotalTokens < 0 || attribution.BufferTokens < 0 ||
		attribution.PromptTokenLimit < 0 || attribution.CompactionThreshold < 0 {
		return ContextUsage{}, errors.New("Copilot returned invalid context usage totals")
	}
	categories := attribution.Categories
	values := []int64{
		categories.SystemPrompt, categories.CustomInstructions, categories.SystemTools,
		categories.MCPTools, categories.Messages, categories.FreeSpace, categories.Buffer,
	}
	for _, value := range values {
		if value < 0 {
			return ContextUsage{}, errors.New("Copilot returned a negative context usage category")
		}
	}
	usage := ContextUsage{
		Model: attribution.ModelID, ModelSource: attribution.ModelSource,
		TotalTokens: attribution.TotalTokens, PromptTokenLimit: attribution.PromptTokenLimit,
		Limit: attribution.Limit, BufferTokens: attribution.BufferTokens,
		CompactionThreshold: attribution.CompactionThreshold,
		SystemPrompt:        ContextCategory{Tokens: categories.SystemPrompt},
		CustomInstructions:  ContextCategory{Tokens: categories.CustomInstructions},
		SystemTools:         ContextCategory{Tokens: categories.SystemTools},
		MCPTools:            ContextCategory{Tokens: categories.MCPTools},
		Messages:            ContextCategory{Tokens: categories.Messages},
		FreeSpace:           ContextCategory{Tokens: categories.FreeSpace},
		Buffer:              ContextCategory{Tokens: categories.Buffer},
		Compactions:         attribution.Compactions.Count,
	}
	usage.Entries = make([]ContextEntry, 0, len(attribution.Entries))
	for _, entry := range attribution.Entries {
		if entry.Tokens < 0 {
			return ContextUsage{}, errors.New("Copilot returned a negative context attribution entry")
		}
		parent := ""
		if entry.ParentID != nil {
			parent = *entry.ParentID
		}
		usage.Entries = append(usage.Entries, ContextEntry{
			ID: entry.ID, Kind: entry.Kind, Label: c.redact.text(entry.Label),
			ParentID: parent, Tokens: entry.Tokens, Attributes: entry.Attributes,
		})
	}
	heaviest, err := session.runtime.ContextHeaviestMessages(ctx, 5)
	if err != nil {
		usage.Warnings = append(usage.Warnings, c.redact.text("Heaviest messages unavailable: "+err.Error()))
		return usage, nil
	}
	if heaviest == nil {
		usage.Warnings = append(usage.Warnings, "Heaviest messages unavailable: Copilot returned no result")
		return usage, nil
	}
	usage.HeaviestMessages = make([]ContextMessage, 0, len(heaviest.Messages))
	for _, message := range heaviest.Messages {
		if message.Tokens < 0 {
			return ContextUsage{}, errors.New("Copilot returned a negative heaviest-message token count")
		}
		usage.HeaviestMessages = append(usage.HeaviestMessages, ContextMessage{
			ID: message.ID, Label: c.redact.text(message.Label),
			Role: c.redact.text(message.Role), Tokens: message.Tokens,
		})
	}
	return usage, nil
}

func (c *Copilot) SetModel(ctx context.Context, selection ModelSelection) error {
	if err := validateModelID(selection.ModelID); err != nil {
		return err
	}
	ctx, done, err := c.reserve(ctx, true, true)
	if err != nil {
		return err
	}
	defer done()
	c.mu.Lock()
	session := c.active
	c.mu.Unlock()
	if session == nil {
		return ErrNoSession
	}
	if err := c.refreshSessionToken(ctx, session); err != nil {
		return err
	}
	if err := c.validateSelection(ctx, selection); err != nil {
		return err
	}
	if err := session.runtime.SetModel(ctx, selection); err != nil {
		return c.redact.err("switch Copilot model", err)
	}
	meta := session.snapshot()
	meta.Model, meta.ContextTier, meta.ReasoningEffort, meta.UpdatedAt =
		selection.ModelID, selection.ContextTier, selection.ReasoningEffort, c.deps.now().UTC()
	session.mu.Lock()
	session.meta = meta
	session.mu.Unlock()
	if err := session.runtime.Configure(ctx); err != nil {
		session.stop()
		return c.redact.err("check model's effective tool surface; reconnect this conversation before sending", err)
	}
	return c.redact.err("store changed model (runtime model has already changed)", c.index.save(ctx, meta))
}

func (c *Copilot) Abort(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	if c.aborting {
		c.mu.Unlock()
		return ErrBusy
	}
	session := c.active
	if session == nil {
		c.mu.Unlock()
		return ErrNoSession
	}
	c.aborting = true
	c.operations.Add(1)
	opDone, opCancel := c.opDone, c.opCancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.aborting = false
		c.mu.Unlock()
		c.operations.Done()
	}()
	ctx, cancel := linkedContext(ctx, c.ctx)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 15*time.Second)
	defer timeout()
	_, _, _ = session.requestAbort()
	if opCancel != nil {
		opCancel()
		// Let any in-flight send finish writing before issuing abort; otherwise
		// its late RPC frame could start new work after the abort frame.
		select {
		case <-opDone:
		case <-ctx.Done():
			return c.redact.err("wait for the canceled operation", ctx.Err())
		}
	}
	runtime, idle, running := session.requestAbort()
	if runtime == nil {
		return errors.New("session creation was canceled before the runtime attached")
	}
	if err := runtime.Abort(ctx); err != nil {
		return c.redact.err("abort Copilot turn (completed file changes were not reverted)", err)
	}
	if running {
		select {
		case <-idle:
		case <-ctx.Done():
			return c.redact.err("wait for Copilot to become idle after cancellation", ctx.Err())
		}
	}
	if err := session.waitCallbacks(ctx); err != nil {
		return c.redact.err("wait for canceled Copilot callbacks", err)
	}
	c.stream.discardRequests(session)
	return session.finishIdleAbort()
}

func (c *Copilot) Events() <-chan Event { return c.stream.output }

func (c *Copilot) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		session := c.active
		c.mu.Unlock()
		if session != nil {
			session.stop()
		}
		c.operations.Wait()
		c.monitor.Wait()
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client != nil {
			c.closeErr = c.redact.err("stop owned Copilot runtime", client.Stop())
		}
		c.stream.close()
	})
	return c.closeErr
}

func (c *Copilot) monitorRuntime(client runtimeClient) {
	defer c.monitor.Done()
	ticker := time.NewTicker(c.deps.healthEvery)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		err := client.Health(ctx)
		cancel()
		if err == nil {
			continue
		}
		c.mu.Lock()
		if c.closed || c.ctx.Err() != nil {
			c.mu.Unlock()
			return
		}
		failure := c.redact.err("bundled Copilot runtime disconnected; reconnect without replaying the prompt", err)
		c.failure = failure
		session := c.active
		c.cancel()
		c.mu.Unlock()
		sessionID := ""
		if session != nil {
			sessionID = session.id
			session.stop()
		}
		c.stream.fail(Event{Kind: EventError, ID: "sodapop:runtime-disconnected", SessionID: sessionID, Failed: true, Text: failure.Error(), Err: failure})
		return
	}
}

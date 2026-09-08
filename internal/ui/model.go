// Package ui implements Sodapop's keyboard-first terminal application.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
)

var _ tea.Model = (*Model)(nil)

const motionInterval = 50 * time.Millisecond

type dialogKind uint8

const (
	dialogHelp dialogKind = iota + 1
	dialogActions
	dialogAccount
	dialogLogin
	dialogModels
	dialogSessions
	dialogThemes
	dialogDiff
	dialogPermission
	dialogQuestion
	dialogConfirm
)

type menuItem struct {
	id, label, detail string
	group             string
	disabled          bool
}

type dialog struct {
	id          uint64
	kind        dialogKind
	title       string
	body        string
	items       []menuItem
	selected    int
	scroll      int
	view        viewport.Model
	action      string
	value       string
	freeform    bool
	cancel      context.CancelFunc
	cacheBody   string
	cacheWidth  int
	modelDrafts map[string]engine.ModelSelection
}

type notice struct {
	text  string
	error bool
}

type operation struct {
	id     uint64
	kind   string
	cancel context.CancelFunc
	target string
}

type geometry struct {
	compact                                   bool
	header, timeline, input, composer, footer int
	innerWidth, composerY                     int
	mainWidth, sidebarWidth, sidebarX         int
	sidebarVisible                            bool
}

// Model owns all presentation state. External services run in tea.Cmds; only
// the small resources ledger is shared with those commands and Shutdown.
type Model struct {
	opts  Options
	life  *resources
	prefs config.Preferences
	color palette

	width, height int
	layout        geometry
	composer      textarea.Model
	answerInput   textarea.Model
	timeline      viewport.Model
	sidebar       viewport.Model
	follow        bool
	sidebarFocus  bool
	sidebarCache  string
	sidebarWidth  int
	mcpServers    []Capability
	skills        []Capability
	toolFocus     bool
	selectedTool  int
	entries       []*entry
	messages      map[string]*entry
	tools         map[string]*entry
	seenEvents    map[string]bool
	retired       map[string]bool
	entrySequence uint64
	renderer      *glamour.TermRenderer
	renderWidth   int
	appearance    uint64
	renderPending bool
	renderDirty   bool
	renderCount   uint64

	account           auth.Account
	accountOwner      string
	authGeneration    uint64
	identityLoading   bool
	identityCancel    context.CancelFunc
	loggingIn         bool
	loginCancel       context.CancelFunc
	loginContext      context.Context
	deviceCode        auth.DeviceCode
	authError         error
	signOutWarning    string
	engineGeneration  uint64
	lease             *engineLease
	connecting        bool
	connectCancel     context.CancelFunc
	connectionError   error
	models            []engine.Model
	model             string
	contextTier       string
	reasoningEffort   string
	session           engine.Session
	sessionGeneration uint64
	needsResume       bool
	reconnectID       string
	operation         operation
	operationSequence uint64
	sessionBuffer     []eventEnvelope
	turn              bool
	turnSequence      uint64
	lastIdleTurn      uint64
	turnCancel        context.CancelFunc
	turnContext       context.Context
	sendPending       bool
	needsAbort        bool
	canceling         bool
	abortAcknowledged bool
	abortBarrierSeen  bool
	abortSessionID    string
	submitted         string
	pendingPrompt     *engine.Message
	localUser         *entry
	streamKey         string
	planning          bool
	allowAll          bool
	usage             string
	contextSequence   uint64
	contextCancel     context.CancelFunc
	status            workspace.Status
	statusGeneration  uint64

	overlay               *dialog
	behindDecision        *dialog
	dialogSequence        uint64
	requests              []*decision
	seenRequests          map[string]bool
	paletteItems          []menuItem
	paletteIndex          int
	paletteOpen           bool
	paletteHidden         string
	notice                notice
	savingPrefs           bool
	preferenceEpoch       uint64
	frame                 int
	composerAccentApplied string
	motionPending         bool
	motionEpoch           uint64
	startup               startupAnimation
	now                   func() time.Time
	moment                transientMoment
	momentSequence        uint64
	experience            turnExperience
	quitting              bool
	openBrowser           func(context.Context, string) error
}

// New constructs a fresh conversation. It performs no authentication, network,
// Git, preference I/O, or session creation; Init starts asynchronous discovery.
func New(opts Options) *Model {
	prefs := opts.Preferences
	if prefs.Version == 0 {
		prefs.Version = config.DefaultPreferences().Version
	}
	if prefs.Theme == "" {
		prefs.Theme = "arcade"
	}
	if prefs.Personality == "" {
		prefs.Personality = config.DefaultPreferences().Personality
	}
	if prefs.ModelSettings == nil {
		prefs.ModelSettings = make(map[string]config.ModelSettings)
	}
	if os.Getenv("NO_COLOR") != "" {
		prefs.NoColor = true
	}
	if os.Getenv("TERM") == "dumb" {
		prefs.ASCII = true
	}
	m := &Model{
		opts: opts, life: newResources(opts.Context, opts.SavePreferences),
		prefs: prefs, color: colors(prefs), width: 80, height: 24,
		composer: newComposer(), answerInput: newComposer(),
		timeline: viewport.New(), sidebar: viewport.New(), follow: true, selectedTool: -1,
		messages: make(map[string]*entry), tools: make(map[string]*entry),
		seenEvents: make(map[string]bool), retired: make(map[string]bool),
		seenRequests:    make(map[string]bool),
		identityLoading: true, authGeneration: 1,
		openBrowser: openVerificationURL,
		now:         time.Now,
	}
	m.mcpServers = append([]Capability(nil), opts.MCPServers...)
	m.skills = append([]Capability(nil), opts.Skills...)
	m.composer.Placeholder = "What are we building?  / for commands"
	m.answerInput.Placeholder = "Type your answer"
	m.timeline.FillHeight = true
	m.timeline.KeyMap = viewport.KeyMap{}
	m.sidebar.FillHeight = true
	m.sidebar.KeyMap = viewport.KeyMap{}
	m.applyAppearance()
	m.resize(80, 24)
	m.reconcileStartup(false)
	return m
}

func newComposer() textarea.Model {
	t := textarea.New()
	t.Prompt = "> "
	t.ShowLineNumbers = false
	t.CharLimit = 128 * 1024
	t.MaxWidth = 0
	t.MaxHeight = 0
	t.MaxContentHeight = 0
	t.SetVirtualCursor(false)
	t.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "shift+enter", "alt+enter"))
	t.KeyMap.Paste.SetEnabled(false)
	t.KeyMap.CopySelection.SetEnabled(false)
	t.Focus()
	return t
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadIdentity(), m.refreshStatus(), m.ensureMotion())
}

// Shutdown is safe to call more than once, including after a failed Run. It
// cancels owned contexts, releases consent responders, and closes every engine
// adopted by asynchronous startup, even when its result was never displayed.
func (m *Model) Shutdown() error { return m.life.shutdown() }

type identityMsg struct {
	generation uint64
	account    auth.Account
	err        error
	login      bool
}

type deviceCodeMsg struct {
	generation uint64
	code       auth.DeviceCode
}

type connectedMsg struct {
	generation uint64
	lease      *engineLease
	err        error
}

type modelsMsg struct {
	generation uint64
	models     []engine.Model
	err        error
}

type sessionsMsg struct {
	generation uint64
	dialog     uint64
	sessions   []engine.Session
	err        error
}

type sessionMsg struct {
	generation, operation uint64
	session               engine.Session
	err                   error
	kind                  string
}

type modelChangedMsg struct {
	generation, operation uint64
	selection             engine.ModelSelection
	err                   error
}

type compactedMsg struct {
	generation, operation uint64
	result                engine.CompactResult
	err                   error
}

type contextMsg struct {
	generation, session, request uint64
	usage                        engine.ContextUsage
	err                          error
}

type sentMsg struct {
	generation, session, turn uint64
	err                       error
	draft                     string
}

type abortedMsg struct {
	generation uint64
	err        error
}

type signedOutMsg struct {
	generation uint64
	mutated    bool
	err        error
	closeErr   error
}

type eventEnvelope struct {
	event    engine.Event
	decision *decision
}

type eventsMsg struct {
	generation uint64
	events     []eventEnvelope
	closed     bool
}

type decisionMsg struct {
	generation uint64
	turn       uint64
	request    bool
	err        error
}

type statusMsg struct {
	generation uint64
	status     workspace.Status
	err        error
}

type diffMsg struct {
	dialog uint64
	diff   workspace.Diff
	err    error
}

type preferenceMsg struct {
	version uint64
	err     error
}

type motionMsg struct {
	epoch uint64
	at    time.Time
}
type renderMsg struct{}
type shutdownMsg struct{ err error }
type browserMsg struct {
	generation uint64
	err        error
}

func result(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

func (m *Model) loadIdentity() tea.Cmd {
	ctx, cancel := context.WithCancel(m.life.ctx)
	m.identityCancel = cancel
	service, generation, life := m.opts.Auth, m.authGeneration, m.life
	return func() tea.Msg {
		if service == nil {
			return identityMsg{generation: generation, err: errors.New("GitHub authentication is unavailable in this build")}
		}
		account, err := accountRead(life, ctx, func() (auth.Account, error) {
			return service.Current(ctx)
		})
		if err == nil && (account.ID == "" || account.Login == "") {
			err = errors.New("GitHub returned an incomplete account identity; sign in again")
		}
		return identityMsg{generation: generation, account: account, err: err}
	}
}

func (m *Model) refreshStatus() tea.Cmd {
	if m.opts.Workspace == nil {
		// Keep startup discovery batched even when workspace inspection is
		// unavailable, so a blocking identity lookup cannot delay input.
		return func() tea.Msg { return nil }
	}
	m.statusGeneration++
	service, ctx, generation := m.opts.Workspace, m.life.ctx, m.statusGeneration
	return func() tea.Msg {
		status, err := service.Status(ctx)
		return statusMsg{generation: generation, status: status, err: err}
	}
}

func (m *Model) waitEvents() tea.Cmd {
	if m.lease == nil {
		return nil
	}
	l, generation, life := m.lease, m.engineGeneration, m.life
	return func() tea.Msg {
		msg := eventsMsg{generation: generation}
		select {
		case <-l.ctx.Done():
			return nil
		case e, ok := <-l.engine.Events():
			if !ok {
				msg.closed = true
				return msg
			}
			msg.events = append(msg.events, eventEnvelope{event: e, decision: life.track(generation, e)})
		}
		// Bounded batches reduce redraw pressure without starving input or
		// dropping terminal events, errors, questions, or permission requests.
		for len(msg.events) < 32 {
			select {
			case e, ok := <-l.engine.Events():
				if !ok {
					msg.closed = true
					return msg
				}
				msg.events = append(msg.events, eventEnvelope{event: e, decision: life.track(generation, e)})
			default:
				return msg
			}
		}
		return msg
	}
}

func (m *Model) busy() bool {
	return m.turn || m.sendPending || m.needsAbort || m.canceling || m.connecting || m.loggingIn || m.operation.kind != "" || len(m.requests) > 0
}

func (m *Model) ensureMotion() tea.Cmd {
	if m.motionPending || !m.canAnimate() {
		return nil
	}
	m.motionPending = true
	epoch := m.motionEpoch
	return tea.Tick(motionInterval, func(at time.Time) tea.Msg { return motionMsg{epoch: epoch, at: at} })
}

func (m *Model) canAnimate() bool {
	if m.prefs.ReducedMotion || m.quitting || m.notice.error || m.life.ctx.Err() != nil {
		return false
	}
	if m.overlay != nil {
		return m.overlay.kind == dialogLogin && m.loggingIn
	}
	return m.busy() || m.startup.state == startupPlaying || m.moment.id != 0
}

func (m *Model) scheduleRender() tea.Cmd {
	if m.renderPending || !m.renderDirty {
		return nil
	}
	m.renderPending = true
	return tea.Tick(45*time.Millisecond, func(time.Time) tea.Msg { return renderMsg{} })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	sized := false
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		sized = msg.Width > 0 && msg.Height > 0
		if sized {
			m.resize(msg.Width, msg.Height)
		}
	case tea.KeyPressMsg:
		cmd = m.handleKey(msg)
	case tea.MouseWheelMsg:
		cmd = m.handleMouseWheel(msg)
	case tea.PasteMsg:
		cmd = m.handlePaste(safeText(msg.Content))
	case identityMsg:
		cmd = m.identityResult(msg)
	case deviceCodeMsg:
		if msg.generation == m.authGeneration && m.loggingIn {
			m.deviceCode = msg.code
			m.showLogin()
		}
	case connectedMsg:
		cmd = m.connectedResult(msg)
	case modelsMsg:
		cmd = m.modelsResult(msg)
	case sessionsMsg:
		m.sessionsResult(msg)
	case sessionMsg:
		cmd = m.sessionResult(msg)
	case modelChangedMsg:
		if msg.generation == m.engineGeneration && msg.operation == m.operation.id {
			m.finishOperation()
			if msg.err != nil {
				m.report(m.recoveryCopy("Model switch failed: "+msg.err.Error()), true)
				cmd = m.startMoment(reactionRecovery)
			} else {
				m.applySelection(msg.selection)
				m.session.Model = msg.selection.ModelID
				m.session.ContextTier = msg.selection.ContextTier
				m.session.ReasoningEffort = msg.selection.ReasoningEffort
				cmd = m.savePreferences()
				m.report("Model selected: "+m.selectionLabel(msg.selection), false)
			}
		}
	case compactedMsg:
		if msg.generation == m.engineGeneration && msg.operation == m.operation.id {
			m.finishOperation()
			if msg.err != nil {
				m.report(m.recoveryCopy("Compaction failed: "+msg.err.Error()+". The visible transcript was kept."), true)
				cmd = m.startMoment(reactionRecovery)
			} else {
				m.report(compactionNotice(msg.result), false)
			}
		}
	case contextMsg:
		m.contextResult(msg)
	case sentMsg:
		cmd = m.sendResult(msg)
	case abortedMsg:
		if msg.generation == m.engineGeneration && m.canceling {
			if msg.err != nil {
				m.canceling = false
				m.abortAcknowledged, m.abortBarrierSeen = false, false
				m.report(m.recoveryCopy("Cancellation could not be confirmed: "+msg.err.Error()+". Reconnect before sending again."), true)
				m.needsResume = true
				cmd = m.startMoment(reactionRecovery)
			} else {
				m.abortAcknowledged = true
				cmd = m.finishAbort()
			}
		}
	case signedOutMsg:
		cmd = m.signOutResult(msg)
	case eventsMsg:
		cmd = m.receiveEvents(msg)
	case decisionMsg:
		if msg.request && msg.turn != m.turnSequence {
			break
		}
		if msg.generation == m.engineGeneration && msg.err != nil {
			if (m.canceling || m.quitting) && expectedCancellationError(msg.err) {
				break
			}
			m.report("The agent could not receive your response: "+msg.err.Error(), true)
		}
	case statusMsg:
		if msg.generation == m.statusGeneration {
			if msg.err != nil {
				m.report(m.recoveryCopy("Project status unavailable: "+msg.err.Error()), true)
				cmd = m.startMoment(reactionRecovery)
			} else {
				m.status = msg.status
			}
		}
	case diffMsg:
		m.diffResult(msg)
	case preferenceMsg:
		m.savingPrefs = false
		if msg.err != nil {
			m.report(m.recoveryCopy("Appearance changed for this run, but saving preferences failed: "+msg.err.Error()), true)
			cmd = m.startMoment(reactionRecovery)
		}
		if msg.version < m.preferenceEpoch {
			cmd = m.flushPreferences()
		}
	case motionMsg:
		if msg.epoch == m.motionEpoch {
			m.motionPending = false
			if m.canAnimate() {
				m.advanceStartup(msg.at)
				m.frame = (m.frame + 1) % 240
				if m.loadingVisible() || m.moment.id != 0 {
					m.renderDirty = true
				}
			}
		}
	case momentExpiredMsg:
		if msg.id == m.moment.id {
			m.clearMoment()
		}
	case renderMsg:
		m.renderPending = false
		m.flushTimeline()
	case browserMsg:
		if msg.generation != m.authGeneration || !m.loggingIn {
			break
		}
		if msg.err != nil {
			m.report("Could not open the browser: "+msg.err.Error()+". Open the displayed GitHub URL yourself.", true)
		} else {
			m.report("Browser opened. Finish authorization there, then return to Sodapop.", false)
		}
	case shutdownMsg:
		if msg.err != nil {
			m.notice = notice{text: "Shutdown: " + safeText(msg.err.Error()), error: true}
		}
		return m, tea.Quit
	}
	if m.overlay == nil && !m.toolFocus {
		m.updatePalette()
	}
	m.reflowComposer()
	m.reconcileStartup(sized)
	return m, tea.Batch(cmd, m.scheduleRender(), m.ensureMotion())
}

func (m *Model) report(text string, isError bool) {
	if isError {
		m.finishStartup()
	}
	text = safeText(text)
	if isError && text != m.notice.text {
		e := &entry{id: m.nextEntryID(), role: "notice", raw: text, final: true}
		e.touch()
		m.entries = append(m.entries, e)
		m.renderDirty = true
	}
	m.notice = notice{text: text, error: isError}
}

func (m *Model) beginOperation(kind, target string) context.Context {
	m.operationSequence++
	ctx, cancel := context.WithCancel(m.lease.ctx)
	m.operation = operation{id: m.operationSequence, kind: kind, target: target, cancel: cancel}
	return ctx
}

func (m *Model) finishOperation() {
	if m.operation.cancel != nil {
		m.operation.cancel()
	}
	m.operation = operation{}
}

func (m *Model) newDialog(kind dialogKind, title, body string) *dialog {
	m.finishStartup()
	if m.overlay != nil && m.overlay.cancel != nil {
		m.overlay.cancel()
	}
	m.dialogSequence++
	d := &dialog{id: m.dialogSequence, kind: kind, title: title, body: safeText(body), view: viewport.New()}
	d.view.KeyMap = viewport.KeyMap{}
	d.view.FillHeight = true
	m.overlay = d
	m.paletteOpen = false
	return d
}

func (m *Model) closeDialog() {
	if m.overlay != nil && m.overlay.cancel != nil {
		m.overlay.cancel()
	}
	m.overlay = nil
	if len(m.requests) > 0 {
		m.showDecision()
	}
}

func (m *Model) quit() tea.Cmd {
	if m.quitting {
		return nil
	}
	m.quitting = true
	m.cancelContextRequest()
	m.clearMoment()
	m.report("Closing Sodapop; history and completed edits are kept.", false)
	return func() tea.Msg { return shutdownMsg{err: m.Shutdown()} }
}

func (m *Model) savePreferences() tea.Cmd {
	m.preferenceEpoch = m.life.prefs.set(m.prefs)
	if m.savingPrefs {
		return nil
	}
	return m.flushPreferences()
}

func (m *Model) flushPreferences() tea.Cmd {
	m.savingPrefs = true
	writer := &m.life.prefs
	return func() tea.Msg {
		version, err := writer.flush()
		return preferenceMsg{version: version, err: err}
	}
}

func (m *Model) ready() bool {
	if m.account.ID == "" {
		m.report("Sign in to GitHub first with /login. /help and /theme work without an account.", true)
		return false
	}
	if m.lease == nil {
		m.report("Copilot is not connected. Use /login to reconnect; signing in and Copilot access are separate.", true)
		return false
	}
	if m.connecting {
		m.report("Still connecting to Copilot. Your draft is kept.", false)
		return false
	}
	return true
}

func (m *Model) modelName() string {
	for _, model := range m.models {
		if model.ID == m.model && model.Name != "" {
			return singleLine(model.Name)
		}
	}
	if m.model != "" {
		return singleLine(m.model)
	}
	return "choose model"
}

func (m *Model) modelSummary() string {
	if m.model == "" {
		return m.modelName()
	}
	separator := m.color.glyph(" · ", " / ")
	return m.modelName() + "\n" + contextTierLabel(m.contextTier) + separator + effortLabel(m.reasoningEffort)
}

func (m *Model) operationLabel() string {
	switch {
	case m.quitting:
		return "closing"
	case m.loggingIn:
		return "waiting for GitHub"
	case m.connecting:
		return "connecting"
	case m.canceling:
		return "stopping"
	case m.needsAbort:
		return "unresolved turn / Ctrl+C"
	case len(m.requests) > 0:
		return "needs your answer"
	case m.operation.kind != "":
		return m.operation.kind
	case m.turn:
		if m.personality() == "quiet" {
			return "working"
		}
		if m.personality() == "extra" {
			return "bubbling brightly"
		}
		return "bubbling"
	case m.identityLoading:
		return "checking account"
	case m.account.ID == "":
		return "sign in with /login"
	case m.lease == nil:
		return "not connected"
	default:
		return "ready"
	}
}

func (m *Model) nextEntryID() string {
	m.entrySequence++
	return fmt.Sprintf("local:%d", m.entrySequence)
}

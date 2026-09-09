package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/commands"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
)

func (m *Model) submit() tea.Cmd {
	raw := m.composer.Value()
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	input, err := commands.Parse(raw)
	if err != nil {
		m.report(err.Error(), true)
		return nil
	}
	var cmd tea.Cmd
	var accepted bool
	if input.IsCommand {
		cmd, accepted = m.execute(input, true)
	} else {
		cmd, accepted = m.sendPrompt(input.Text, raw)
	}
	if accepted {
		m.composer.Reset()
		m.paletteOpen = false
		m.paletteHidden = ""
		m.reflowComposer()
	}
	return cmd
}

func (m *Model) runLocal(name string) tea.Cmd {
	input, err := commands.Parse("/" + name)
	if err != nil {
		m.report(err.Error(), true)
		return nil
	}
	cmd, _ := m.execute(input, false)
	return cmd
}

func (m *Model) execute(input commands.Input, fromComposer bool) (tea.Cmd, bool) {
	command, ok := commands.Lookup(input.Command)
	if !ok {
		m.report("Unknown command. Type /help to see available commands.", true)
		return nil, false
	}
	if m.busy() && !command.AllowedWhileBusy {
		m.report("/"+command.Name+" is unavailable while work is active. Ctrl+C cancels; your draft is kept. Nothing was queued.", true)
		return nil, false
	}
	switch input.Command {
	case "help":
		help, err := commands.Help(input.Args)
		if err != nil {
			m.report(err.Error(), true)
			return nil, false
		}
		m.newDialog(dialogHelp, "FIELD GUIDE / Sodapop", help)
		return nil, true
	case "login":
		m.showAccount()
		return nil, true
	case "logout":
		if m.account.ID == "" {
			m.report("Already signed out. Use /login to connect GitHub.", false)
			return nil, true
		}
		if m.busy() || len(m.requests) > 0 {
			m.confirm("signout", "", "Stop and sign out?", "Pending permissions will be denied and unanswered questions cancelled. History, your draft, and completed edits are kept.", "Stop and sign out")
			return nil, true
		}
		return m.signOut(), true
	case "model":
		if !m.ready() {
			return nil, false
		}
		if input.Args == "" {
			m.showModels()
			return nil, true
		}
		return m.selectModel(input.Args)
	case "clear":
		if !fromComposer && strings.TrimSpace(m.composer.Value()) != "" {
			m.confirm("clear", "", "Start a fresh conversation?", "Your draft stays in the composer. The previous conversation remains available through /resume.", "Start fresh; keep draft")
		} else {
			m.resetConversation()
			m.overlay = nil
			m.report("Fresh conversation. History is kept; a session starts only when you send.", false)
			return m.captureBaseline(), true
		}
		return nil, true
	case "resume":
		if !m.ready() {
			return nil, false
		}
		if input.Args == "" {
			return m.browseSessions(), true
		}
		if !fromComposer && strings.TrimSpace(m.composer.Value()) != "" {
			m.confirmResume(input.Args)
			return nil, true
		}
		return m.resumeSession(input.Args), true
	case "context":
		return m.showContext(), true
	case "compact":
		return m.compact(input.Args)
	case "plan":
		if input.Args == "" {
			m.setPlanning(true)
			m.report(m.planningNotice(), false)
			return nil, true
		}
		if input.Args == "off" {
			m.setChat()
			m.report(m.planningNotice(), false)
			return nil, true
		}
		m.setPlanning(true)
		return m.sendPrompt(input.Args, m.composer.Value())
	case "fizz":
		return m.runFizz(input.Args, m.composer.Value())
	case "taste-test":
		return m.prepareTasteTest(input.Args, m.composer.Value(), fromComposer)
	case "vending-machine":
		m.showVendingMachine()
		return nil, true
	case "autopilot":
		if input.Args == "off" {
			m.setChat()
		} else {
			m.setAutopilot(true)
		}
		m.report(m.autopilotNotice(), false)
		return nil, true
	case "mcp":
		return m.manageMCP(input.Args)
	case "skill":
		return m.manageSkills(input.Args)
	case "diff":
		mode := input.Args
		if mode == "" {
			mode = "all"
		}
		return m.openDiff(mode), true
	case "theme":
		if input.Args == "" {
			m.showThemes()
			return nil, true
		}
		return m.selectTheme(input.Args)
	case "exit":
		if m.busy() || len(m.requests) > 0 || (!fromComposer && strings.TrimSpace(m.composer.Value()) != "") {
			m.confirmExitWithDraft(!fromComposer)
			return nil, true
		}
		return m.quit(), true
	default:
		m.report("The command registry contains an action this UI does not implement: "+command.Name, true)
		return nil, false
	}
}

func (m *Model) showContext() tea.Cmd {
	if !m.ready() {
		return nil
	}
	if m.session.ID == "" {
		m.appendContextText("Context information is not yet available. Send a message first so Copilot can initialize the agent context.")
		return nil
	}
	m.cancelContextRequest()
	m.contextSequence++
	request := m.contextSequence
	ctx, cancel := context.WithCancel(m.lease.ctx)
	m.contextCancel = cancel
	l, life := m.lease, m.life
	generation, session := m.engineGeneration, m.sessionGeneration
	return func() tea.Msg {
		usage, err := accountRead(life, ctx, func() (engine.ContextUsage, error) {
			return l.engine.Context(ctx)
		})
		return contextMsg{generation: generation, session: session, request: request, usage: usage, err: err}
	}
}

func (m *Model) contextResult(msg contextMsg) {
	if msg.generation != m.engineGeneration || msg.session != m.sessionGeneration ||
		msg.request != m.contextSequence || m.quitting {
		return
	}
	m.cancelContextRequest()
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, engine.ErrNoSession), errors.Is(msg.err, engine.ErrContextUnavailable):
			m.appendContextText("Context information is not yet available. Send a message first so Copilot can initialize the agent context.")
		case errors.Is(msg.err, engine.ErrVariableContext):
			m.appendContextText("Context usage is unavailable for HydraFusion because its context window depends on the model selected for each turn.")
		default:
			m.report(m.recoveryCopy("Context usage could not be loaded: "+m.accessErrorText(msg.err, nil)), true)
		}
		return
	}
	e := &entry{id: m.nextEntryID(), role: "context", final: true, context: &msg.usage}
	e.touch()
	m.entries = append(m.entries, e)
	m.renderDirty = true
	m.flushTimeline()
}

func (m *Model) appendContextText(text string) {
	e := &entry{id: m.nextEntryID(), role: "context", raw: text, final: true}
	e.touch()
	m.entries = append(m.entries, e)
	m.renderDirty = true
	m.flushTimeline()
}

func (m *Model) cancelContextRequest() {
	if m.contextCancel != nil {
		m.contextCancel()
		m.contextCancel = nil
	}
}

func (m *Model) compact(instructions string) (tea.Cmd, bool) {
	if !m.ready() {
		return nil, false
	}
	if m.session.ID == "" {
		m.report("There is no conversation to compact. Send a prompt or resume a saved conversation first.", true)
		return nil, false
	}
	if m.needsResume {
		m.report("Reconnect or explicitly /resume this conversation before compacting it.", true)
		return nil, false
	}
	ctx := m.beginOperation("compacting conversation", "")
	l, generation, op, life := m.lease, m.engineGeneration, m.operation.id, m.life
	m.report("Compacting model context; the visible transcript will be kept.", false)
	return func() tea.Msg {
		compacted, err := accountRead(life, ctx, func() (engine.CompactResult, error) {
			return l.engine.Compact(ctx, instructions)
		})
		return compactedMsg{generation: generation, operation: op, result: compacted, err: err}
	}, true
}

func compactionNotice(result engine.CompactResult) string {
	if result.MessagesRemoved == 0 && result.TokensRemoved == 0 {
		return "Compaction completed, but no context needed to be removed. The visible transcript was kept."
	}
	text := fmt.Sprintf("Compaction completed: removed %d messages and %d tokens", result.MessagesRemoved, result.TokensRemoved)
	if result.TokenLimit > 0 {
		text += fmt.Sprintf("; context is now %d / %d tokens", result.CurrentTokens, result.TokenLimit)
	}
	return text + ". The visible transcript was kept."
}

func (m *Model) planningNotice() string {
	if m.planning {
		return "ADVISORY PLAN enabled: ask for an approach and tradeoffs. This is not read-only; normal tool approvals still apply."
	}
	return "Advisory planning is off. Normal tool approvals still apply."
}

func (m *Model) setPlanning(enabled bool) {
	m.planning = enabled
	if enabled {
		m.autopilotEnabled = false
	}
	m.updateComposerPrompt()
}

func (m *Model) autopilotNotice() string {
	if m.autopilotEnabled {
		return "AUTOPILOT enabled for this conversation. Tool requests will be approved without prompting; commands are not sandboxed. Agent questions still require your answer. Use /autopilot off, F2, or Shift+Tab to return to chat and restore prompts."
	}
	return "Autopilot is off. Tool requests will require individual approval."
}

func (m *Model) setAutopilot(enabled bool) {
	m.autopilotEnabled = enabled
	if enabled {
		m.planning = false
	}
	m.updateComposerPrompt()
	m.renderDirty = true
}

func (m *Model) setChat() {
	m.planning = false
	m.autopilotEnabled = false
	m.updateComposerPrompt()
	m.renderDirty = true
}

func (m *Model) cycleComposerMode() {
	switch {
	case m.autopilotEnabled:
		m.setChat()
	case m.planning:
		m.setAutopilot(true)
	default:
		m.setPlanning(true)
	}
}

func (m *Model) toggleAutopilot() tea.Cmd {
	if m.autopilotEnabled {
		m.setAutopilot(false)
		m.report(m.autopilotNotice(), false)
		return nil
	}
	if len(m.requests) > 0 {
		return m.enableAutopilotForPermissions()
	}
	m.setAutopilot(true)
	m.report(m.autopilotNotice(), false)
	return nil
}

func (m *Model) showModels() {
	d := m.newDialog(dialogModels, "MODEL / Copilot", "Choose a model and adjust its Context and Thinking columns. Changes apply together only when you press Enter.")
	d.modelDrafts = make(map[string]engine.ModelSelection)
	if len(m.models) == 0 {
		d.body = "Loading models from Copilot..."
	}
	models := append([]engine.Model(nil), m.models...)
	sort.SliceStable(models, func(i, j int) bool {
		left, right := familyOrder(models[i].Family), familyOrder(models[j].Family)
		if left != right {
			return left < right
		}
		return strings.ToLower(models[i].Name) < strings.ToLower(models[j].Name)
	})
	for i, model := range models {
		name := model.Name
		if name == "" {
			name = model.ID
		}
		d.items = append(d.items, menuItem{id: model.ID, label: singleLine(name), group: model.Family})
		selection, _ := m.selectionForModel(model.ID)
		d.modelDrafts[model.ID] = selection
		if model.ID == m.model {
			d.selected = i
		}
	}
}

func (m *Model) selectModel(id string) (tea.Cmd, bool) {
	selection, found := m.selectionForModel(id)
	if !found {
		m.report("Model is not available for this account: "+singleLine(id)+". Use /model to choose an available model.", true)
		return nil, false
	}
	return m.selectModelSelection(selection)
}

func (m *Model) selectModelSelection(selection engine.ModelSelection) (tea.Cmd, bool) {
	if m.busy() {
		m.report("Finish or cancel active work before switching models. Nothing was queued.", true)
		return nil, false
	}
	m.overlay = nil
	if m.session.ID == "" || m.needsResume {
		m.applySelection(selection)
		m.report("Model selected: "+m.selectionLabel(selection), false)
		return m.savePreferences(), true
	}
	ctx := m.beginOperation("switching model", selection.ModelID)
	l, generation, op, life := m.lease, m.engineGeneration, m.operation.id, m.life
	return func() tea.Msg {
		err := accountAction(life, ctx, func() error { return l.engine.SetModel(ctx, selection) })
		return modelChangedMsg{generation: generation, operation: op, selection: selection, err: err}
	}, true
}

func (m *Model) selectionForModel(id string) (engine.ModelSelection, bool) {
	for _, model := range m.models {
		if model.ID == id {
			selection := engine.ModelSelection{
				ModelID: id, ContextTier: model.DefaultContextTier,
				ReasoningEffort: model.DefaultReasoningEffort,
			}
			if selection.ContextTier == "" {
				selection.ContextTier = "default"
			}
			if id == m.model && m.contextTier != "" {
				selection.ContextTier = m.contextTier
				selection.ReasoningEffort = m.reasoningEffort
			} else if saved, ok := m.prefs.ModelSettings[id]; ok {
				selection.ContextTier = saved.ContextTier
				selection.ReasoningEffort = saved.ReasoningEffort
			}
			return normalizeSelection(model, selection), true
		}
	}
	return engine.ModelSelection{}, false
}

func familyOrder(family string) int {
	switch family {
	case "OpenAI":
		return 0
	case "Anthropic":
		return 1
	case "Google":
		return 2
	case "xAI":
		return 3
	default:
		return 4
	}
}

func normalizeSelection(model engine.Model, selection engine.ModelSelection) engine.ModelSelection {
	selection.ModelID = model.ID
	contextOK := false
	for _, option := range model.ContextOptions {
		if option.Tier == selection.ContextTier {
			contextOK = true
			break
		}
	}
	if !contextOK {
		selection.ContextTier = model.DefaultContextTier
		if selection.ContextTier == "" {
			selection.ContextTier = "default"
		}
	}
	if len(model.ReasoningEfforts) == 0 {
		selection.ReasoningEffort = ""
		return selection
	}
	for _, effort := range model.ReasoningEfforts {
		if effort == selection.ReasoningEffort {
			return selection
		}
	}
	selection.ReasoningEffort = model.DefaultReasoningEffort
	if selection.ReasoningEffort == "" {
		selection.ReasoningEffort = model.ReasoningEfforts[0]
	}
	return selection
}

func (m *Model) applySelection(selection engine.ModelSelection) {
	m.model = selection.ModelID
	m.contextTier = selection.ContextTier
	m.reasoningEffort = selection.ReasoningEffort
	m.prefs.Model = selection.ModelID
	if m.prefs.ModelSettings == nil {
		m.prefs.ModelSettings = make(map[string]config.ModelSettings)
	}
	m.prefs.ModelSettings[selection.ModelID] = config.ModelSettings{
		ContextTier: selection.ContextTier, ReasoningEffort: selection.ReasoningEffort,
	}
}

func (m *Model) selectionLabel(selection engine.ModelSelection) string {
	name := selection.ModelID
	for _, model := range m.models {
		if model.ID == selection.ModelID && model.Name != "" {
			name = model.Name
			break
		}
	}
	return singleLine(name) + " / " + contextTierLabel(selection.ContextTier) + " / " + effortLabel(selection.ReasoningEffort)
}

func contextTierLabel(tier string) string {
	if tier == "long_context" {
		return "Long"
	}
	return "Default"
}

func effortLabel(effort string) string {
	if effort == "" {
		return "Model default"
	}
	if effort == "xhigh" {
		return "XHigh"
	}
	return strings.ToUpper(effort[:1]) + effort[1:]
}

func (m *Model) browseSessions() tea.Cmd {
	d := m.newDialog(dialogSessions, "RESUME / this project", "Loading Sodapop conversations for this account and project...")
	ctx, cancel := context.WithCancel(m.lease.ctx)
	d.cancel = cancel
	l, generation, id, life := m.lease, m.engineGeneration, d.id, m.life
	return func() tea.Msg {
		sessions, err := accountRead(life, ctx, func() ([]engine.Session, error) {
			return l.engine.Sessions(ctx)
		})
		return sessionsMsg{generation: generation, dialog: id, sessions: sessions, err: err}
	}
}

func (m *Model) sessionsResult(msg sessionsMsg) {
	if msg.generation != m.engineGeneration || m.overlay == nil || m.overlay.id != msg.dialog {
		return
	}
	d := m.overlay
	if msg.err != nil {
		d.body = "Could not list conversations:\n" + safeText(msg.err.Error())
		m.report("Session history could not be loaded: "+msg.err.Error(), true)
		return
	}
	d.body = "Only Sodapop conversations for this account and project are shown. Resuming never replays your last prompt."
	for _, session := range msg.sessions {
		if session.ID == "" || filepath.Clean(session.Project) != filepath.Clean(m.opts.Project) {
			continue
		}
		title := singleLine(session.Title)
		if title == "" {
			title = "Untitled conversation"
		}
		detail := singleLine(session.ID)
		if !session.UpdatedAt.IsZero() {
			detail = session.UpdatedAt.Local().Format("Jan 02 15:04") + " / " + detail
		}
		d.items = append(d.items, menuItem{id: session.ID, label: title, detail: detail})
	}
	if len(d.items) == 0 {
		d.body = "No saved Sodapop conversations for this account and project yet.\n\nA conversation is created only when you send your first prompt."
	}
}

func (m *Model) confirmResume(id string) {
	m.confirm("resume", id, "Resume this conversation?", "Your unsent draft stays in the composer. Nothing is sent automatically.", "Resume; keep draft")
}

func (m *Model) resumeSession(id string) tea.Cmd {
	if m.busy() || !m.ready() {
		if m.busy() {
			m.report("Finish or cancel active work before resuming. Nothing was queued.", true)
		}
		return nil
	}
	m.overlay = nil
	m.sessionBuffer = nil
	ctx := m.beginOperation("resuming", id)
	l, generation, op, life := m.lease, m.engineGeneration, m.operation.id, m.life
	m.report("Restoring conversation history; no prompt is being sent.", false)
	return func() tea.Msg {
		session, err := accountRead(life, ctx, func() (engine.Session, error) {
			return l.engine.ResumeSession(ctx, id)
		})
		return sessionMsg{generation: generation, operation: op, session: session, err: err, kind: "resume"}
	}
}

func (m *Model) sendPrompt(text, draft string) (tea.Cmd, bool) {
	if m.busy() {
		m.report("A turn or operation is already active. Your draft is kept; nothing was queued. Ctrl+C cancels.", true)
		return nil, false
	}
	if !m.ready() {
		return nil, false
	}
	if m.needsResume {
		m.report("Reconnect or explicitly /resume this conversation before sending. /clear starts fresh. No prompt was replayed.", true)
		return nil, false
	}
	if m.model == "" {
		m.showModels()
		m.report("Choose a model, then send your draft. Nothing has been sent.", false)
		return nil, false
	}
	m.turnSequence++
	m.turn = true
	m.beginTurnExperience()
	m.turnContext, m.turnCancel = context.WithCancel(m.lease.ctx)
	m.submitted = draft
	m.streamKey = ""
	m.usage = ""
	m.notice = notice{}
	m.follow = true
	message := engine.Message{Text: text, Planning: m.planning}
	if m.session.ID != "" {
		return m.dispatchSend(message), true
	}
	m.pendingPrompt = &message
	m.turn = false
	m.sessionBuffer = nil
	ctx := m.beginOperation("starting conversation", "")
	l, model, generation, op, life := m.lease, m.model, m.engineGeneration, m.operation.id, m.life
	selection, ok := m.selectionForModel(model)
	if !ok {
		m.finishOperation()
		m.report("The selected model is no longer available. Use /model to choose again.", true)
		return nil, false
	}
	return func() tea.Msg {
		session, err := accountRead(life, ctx, func() (engine.Session, error) {
			return l.engine.NewSession(ctx, selection)
		})
		return sessionMsg{generation: generation, operation: op, session: session, err: err, kind: "new"}
	}, true
}

func (m *Model) dispatchSend(message engine.Message) tea.Cmd {
	m.turn = true
	m.sendPending = true
	m.needsAbort = false
	m.lastIdleTurn = 0
	m.pendingPrompt = nil
	m.localUser = &entry{id: m.nextEntryID(), role: "you", raw: message.Text, final: true}
	m.entries = append(m.entries, m.localUser)
	m.messages[m.localUser.id] = m.localUser
	m.localUser.touch()
	m.renderDirty = true
	m.flushTimeline()
	l, ctx, life := m.lease, m.turnContext, m.life
	generation, session, turn, draft := m.engineGeneration, m.sessionGeneration, m.turnSequence, m.submitted
	return func() tea.Msg {
		err := accountAction(life, ctx, func() error { return l.engine.Send(ctx, message) })
		return sentMsg{generation: generation, session: session, turn: turn, err: err, draft: draft}
	}
}

func (m *Model) sessionResult(msg sessionMsg) tea.Cmd {
	if msg.generation != m.engineGeneration || msg.operation != m.operation.id {
		return nil
	}
	m.finishOperation()
	if msg.err == nil && msg.session.ID == "" {
		msg.err = errors.New("the engine returned an empty session identity")
	}
	if msg.err == nil && msg.session.Project != "" && filepath.Clean(msg.session.Project) != filepath.Clean(m.opts.Project) {
		msg.err = errors.New("the engine returned a session from a different project")
	}
	if msg.err != nil {
		m.turn = false
		m.pendingPrompt = nil
		if m.turnCancel != nil {
			m.turnCancel()
			m.turnCancel = nil
		}
		if msg.kind == "new" && m.composer.Value() == "" {
			m.composer.SetValue(m.submitted)
		}
		if msg.kind == "resume" {
			m.needsResume = m.session.ID != ""
		}
		detail := m.accessErrorText(msg.err, nil)
		for _, envelope := range m.sessionBuffer {
			e := envelope.event
			if e.Kind == engine.EventError && e.SessionID == "" && !e.History && e.Access != nil {
				detail = m.accessErrorText(e.Err, e.Access)
				break
			}
		}
		m.report(m.recoveryCopy("Could not "+msg.kind+" conversation: "+detail+". Your draft and previous history are kept."), true)
		return m.discardBufferedEvents()
	}
	if msg.kind == "resume" {
		m.cancelContextRequest()
		m.clearTimeline()
		m.setPlanning(false)
		m.setAutopilot(false)
	}
	m.session = msg.session
	m.sessionGeneration++
	m.needsResume = false
	m.needsAbort = false
	if msg.session.Model != "" {
		m.model = msg.session.Model
		m.contextTier = msg.session.ContextTier
		if m.contextTier == "" {
			m.contextTier = "default"
		}
		m.reasoningEffort = msg.session.ReasoningEffort
	}
	buffer := m.sessionBuffer
	m.sessionBuffer = nil
	var cmds []tea.Cmd
	for _, envelope := range buffer {
		cmds = append(cmds, m.acceptEvent(envelope))
	}
	m.flushTimeline()
	if msg.kind == "new" && m.pendingPrompt != nil {
		cmds = append(cmds, m.dispatchSend(*m.pendingPrompt))
	} else {
		m.report("Conversation restored. Your draft is kept; nothing was sent.", false)
	}
	cmds = append(cmds, m.captureBaseline())
	if m.model == "" && m.overlay == nil {
		m.showModels()
	}
	return tea.Batch(cmds...)
}

func (m *Model) clearTimeline() {
	m.entries = nil
	m.messages = make(map[string]*entry)
	m.tools = make(map[string]*entry)
	m.seenEvents = make(map[string]bool)
	m.retired = make(map[string]bool)
	m.localUser = nil
	m.streamKey = ""
	m.toolFocus = false
	m.selectedTool = -1
	m.follow = true
	m.experience = turnExperience{}
	m.clearMoment()
	m.renderDirty = true
	m.timeline.SetContent("")
	m.timeline.GotoTop()
}

func (m *Model) resetConversation() {
	m.cancelContextRequest()
	m.sessionGeneration++
	m.baselineGeneration++
	m.session = engine.Session{}
	m.needsResume = false
	m.needsAbort = false
	m.reconnectID = ""
	m.setPlanning(false)
	m.setAutopilot(false)
	m.usage = ""
	m.clearTimeline()
}

func (m *Model) openDiff(mode string) tea.Cmd {
	scope := "WORKING TREE"
	if mode == "session" {
		scope = "CONVERSATION CHANGES"
	}
	d := m.newDialog(dialogDiff, scope+" / "+mode+" / read-only", "Loading Git diff...\n\nChange attribution is observational and does not prove authorship.")
	if m.opts.Workspace == nil {
		d.body = "Working-tree inspection is unavailable in this build."
		m.report(d.body, true)
		return nil
	}
	ctx, cancel := context.WithCancel(m.life.ctx)
	d.cancel = cancel
	service, id := m.opts.Workspace, d.id
	baseline := m.baseline
	return func() tea.Msg {
		var diff workspace.Diff
		var err error
		if mode == "session" {
			if baseline == nil {
				err = errors.New("conversation baseline is not ready")
			} else {
				diff, err = baseline.Diff(ctx)
			}
		} else {
			diff, err = service.Diff(ctx, mode)
		}
		return diffMsg{dialog: id, diff: diff, err: err}
	}
}

func (m *Model) diffResult(msg diffMsg) {
	d := m.overlay
	if d == nil || d.id != msg.dialog {
		d = m.behindDecision
	}
	if d == nil || d.id != msg.dialog {
		return
	}
	if msg.err != nil {
		d.body = "Could not read the working-tree diff:\n\n" + safeText(msg.err.Error())
		m.report("Diff failed: "+msg.err.Error(), true)
		return
	}
	text := "WORKING-TREE STATE / includes changes not made by Sodapop\nRead-only: nothing is staged, discarded, or committed.\n\n"
	if !msg.diff.IsRepository {
		text += "This project is not a Git repository."
	} else if msg.diff.Text == "" {
		text += "No working-tree changes."
	} else {
		text += safeText(msg.diff.Text)
	}
	if msg.diff.Truncated {
		text += "\n\n[Diff truncated by the workspace service; additional content is not shown.]"
	}
	d.body = text
	d.scroll = 0
}

func (m *Model) showThemes() {
	d := m.newDialog(dialogThemes, "APPEARANCE / make it yours", "Changes apply immediately and are saved. Color, motion, personality, and character-set preferences are independent.")
	d.items = []menuItem{
		{id: "arcade", label: "Neon Arcade", detail: "Modern near-black / restrained neon accents"},
		{id: "graphite", label: "Graphite", detail: "Neutral, crisp and GitHub-inspired"},
		{id: "midnight", label: "Midnight", detail: "Deep navy / cool blue / sea glass"},
		{id: "high-contrast", label: "High Contrast", detail: "Maximum separation on true black"},
		{id: "light", label: "Daylight", detail: "Soft neutral canvas / precise dark text"},
		{id: "reduced-motion", label: "Reduced motion", detail: "Decorative animation and cursor blink off"},
		{id: "motion", label: "Enable motion", detail: "Small bubbly activity indicator"},
		{id: "no-color", label: "No color", detail: "No styling or markdown escape codes"},
		{id: "color", label: "Enable color", detail: "Use the selected theme's palette"},
		{id: "ascii", label: "ASCII interface", detail: "Plain borders and symbols; no special font"},
		{id: "unicode", label: "Unicode interface", detail: "Rounded panels and simple symbols"},
		{id: "personality-quiet", label: "Quiet personality", detail: "Direct technical language with minimal decoration"},
		{id: "personality-playful", label: "Playful personality", detail: "Moderate Sodapop chemistry, reactions and success moments"},
		{id: "personality-extra", label: "Extra personality", detail: "Richer deterministic copy and decorative reactions"},
	}
}

func (m *Model) selectTheme(name string) (tea.Cmd, bool) {
	switch name {
	case "arcade", "graphite", "midnight", "high-contrast", "light":
		m.prefs.Theme = name
	case "reduced-motion":
		m.prefs.ReducedMotion = true
	case "motion":
		m.prefs.ReducedMotion = false
	case "no-color":
		m.prefs.NoColor = true
	case "color":
		m.prefs.NoColor = false
	case "ascii":
		m.prefs.ASCII = true
	case "unicode":
		m.prefs.ASCII = false
	case "personality-quiet":
		m.prefs.Personality = "quiet"
	case "personality-playful":
		m.prefs.Personality = "playful"
	case "personality-extra":
		m.prefs.Personality = "extra"
	default:
		m.report("Unknown appearance option: "+singleLine(name)+". Open /theme to choose a theme or accessibility setting.", true)
		return nil, false
	}
	m.applyAppearance()
	m.report("Appearance updated: "+name, false)
	return m.savePreferences(), true
}

func (m *Model) confirm(action, value, title, body, yes string) {
	d := m.newDialog(dialogConfirm, title, body)
	d.action, d.value = action, value
	d.items = []menuItem{{id: "cancel", label: "Keep working"}, {id: "confirm", label: yes}}
}

func (m *Model) confirmExit() {
	m.confirmExitWithDraft(true)
}

func (m *Model) confirmExitWithDraft(includeDraft bool) {
	body := "Conversation history and completed edits will be kept. Exiting does not sign out."
	yes := "Exit Sodapop"
	if m.busy() || len(m.requests) > 0 {
		body = "Stop active work, deny pending permissions, cancel unanswered questions, and exit?\n\nCompleted edits will NOT be undone. Conversation history is kept."
		yes = "Stop and exit"
	}
	if includeDraft && strings.TrimSpace(m.composer.Value()) != "" {
		body += "\n\nThere is an unsent draft. It is not saved after exit."
	}
	body += "\n\n" + m.exitRecap()
	m.confirm("exit", "", "Leave Sodapop?", body, yes)
}

func (m *Model) confirmAction(d *dialog) tea.Cmd {
	switch d.action {
	case "exit":
		return m.quit()
	case "clear":
		m.resetConversation()
		m.overlay = nil
		m.report("Fresh conversation. Your draft and prior history are kept.", false)
		return m.captureBaseline()
	case "resume":
		return m.resumeSession(d.value)
	case "signout":
		return m.signOut()
	default:
		m.report(fmt.Sprintf("Unknown confirmation action %q.", d.action), true)
	}
	return nil
}

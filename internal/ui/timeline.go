package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/charmbracelet/x/ansi"
)

const maxToolBytes = 256 * 1024
const maxMessageBytes = 1024 * 1024
const loadingGlyphs = "0123456789abcdefABCDEFGHIJKLMNOPQRSTUVWXYZ~!@#$%^&*+=_<>"

func (m *Model) loadingVisible() bool {
	return m.turn || m.sendPending || m.operation.kind == "starting conversation"
}

func (m *Model) loadingLabel() string {
	if m.operation.kind == "starting conversation" {
		return "Starting conversation"
	}
	if m.sendPending {
		return "Sending"
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		e := m.entries[i]
		if e.role == "tool" && !e.final {
			return "Running " + singleLine(e.name)
		}
		if e.role == "assistant" && !e.final {
			return "Generating"
		}
	}
	return "Thinking"
}

func (m *Model) loadingView(width int) string {
	label := m.activityCopy(m.loadingLabel())
	dot := m.color.glyph("·", ".")
	if m.prefs.ReducedMotion {
		return clip(m.color.paint(m.color.muted, dot+" "+label), width)
	}

	ellipsis := []string{"", ".", "..", "..."}[(m.frame/8)%4]
	available := max(0, width-ansi.StringWidth(label)-ansi.StringWidth(ellipsis)-1)
	count := min(10, available)
	if count < 4 {
		return clip(m.color.paint(m.color.text, label+ellipsis), width)
	}

	glyphs := []rune(loadingGlyphs)
	ramp := lipgloss.Blend1D(count, lipgloss.Color(m.color.cyan), lipgloss.Color(m.color.magenta))
	var scramble strings.Builder
	for column := range count {
		index := (m.frame*7 + column*13 + column*column*3) % len(glyphs)
		character := string(glyphs[index])
		if m.color.noColor {
			scramble.WriteString(character)
			continue
		}
		scramble.WriteString(lipgloss.NewStyle().Foreground(ramp[column]).Render(character))
	}
	return clip(scramble.String()+" "+m.color.paint(m.color.text, label+ellipsis), width)
}

type entry struct {
	id, role, raw, arguments, name, state string
	final, history, expanded, truncated   bool
	inputTruncated                        bool
	revision, cachedRevision              uint64
	cachedAppearance                      uint64
	cachedWidth                           int
	cached                                string
	context                               *engine.ContextUsage
}

func (e *entry) touch() { e.revision++ }

func (e *entry) append(text string, limit int) {
	remaining := limit - len(e.raw)
	if remaining <= 0 {
		if !e.truncated {
			e.touch()
		}
		e.truncated = true
		return
	}
	if len(text) > remaining {
		text = text[:remaining]
		e.truncated = true
	}
	e.raw += text
	e.touch()
}

func (e *entry) replace(text string, limit int) {
	e.truncated = len(text) > limit
	e.raw = text[:min(len(text), limit)]
	e.touch()
}

func (m *Model) receiveEvents(msg eventsMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.generation != m.engineGeneration || m.quitting {
		for _, e := range msg.events {
			if e.decision != nil {
				cmds = append(cmds, m.resolveCommand(e.decision, answer{cancel: true}))
			}
		}
		return tea.Batch(cmds...)
	}
	for _, envelope := range msg.events {
		if m.operation.kind == "starting conversation" || m.operation.kind == "resuming" {
			m.sessionBuffer = append(m.sessionBuffer, envelope)
			continue
		}
		cmds = append(cmds, m.acceptEvent(envelope))
	}
	if msg.closed {
		m.turn, m.sendPending, m.canceling, m.connecting = false, false, false, false
		m.needsAbort = false
		m.abortAcknowledged, m.abortBarrierSeen = false, false
		if m.turnCancel != nil {
			m.turnCancel()
			m.turnCancel = nil
		}
		m.finishOperation()
		_ = m.finishConnectionProbe(nil)
		if _, blocked := m.currentAccessIssue(); !blocked {
			m.recordAccessFailure(errors.New("the Copilot event stream closed"), &engine.AccessIssue{Reason: engine.AccessNetwork}, true)
		}
		m.needsResume = m.session.ID != ""
		m.reconnectID = m.session.ID
		m.report(m.recoveryCopy("Copilot disconnected. /login reconnects without replaying a prompt."), true)
		cmds = append(cmds, m.cancelDecisions(), m.discardBufferedEvents(), m.startMoment(reactionRecovery))
		l := m.lease
		m.lease = nil
		if l != nil {
			cmds = append(cmds, func() tea.Msg { return decisionMsg{generation: msg.generation, err: l.close()} })
		}
		m.presentAccessRecovery()
	} else {
		cmds = append(cmds, m.waitEvents())
	}
	return tea.Batch(cmds...)
}

func (m *Model) acceptEvent(envelope eventEnvelope) tea.Cmd {
	e := envelope.event
	if e.Kind == engine.EventIdle && e.Name == "aborted" {
		if e.History {
			return nil
		}
		key := string(e.Kind) + "/" + e.ID
		if e.ID != "" && m.seenEvents[key] {
			return nil
		}
		if e.ID != "" {
			m.seenEvents[key] = true
		}
		// The canceled create/resume operation may never have returned a
		// session ID. Only its current engine's outstanding Abort can finish
		// this handshake; a late cancellation barrier never ends a new turn.
		if m.canceling && (m.abortSessionID == "" || e.SessionID == m.abortSessionID) {
			m.abortBarrierSeen = true
			return m.finishAbort()
		}
		return nil
	}
	if e.SessionID != m.session.ID || m.session.ID == "" || m.needsResume {
		if envelope.decision != nil {
			return m.resolveCommand(envelope.decision, answer{cancel: true})
		}
		if e.Kind == engine.EventError && e.SessionID == "" {
			if !e.History && m.recordAccessFailure(e.Err, e.Access, false) {
				m.notifyAccessFailure()
				if m.connecting {
					m.connecting = false
					_ = m.finishConnectionProbe(nil)
					cmd := m.retireConnection()
					m.presentAccessRecovery()
					return cmd
				}
			} else {
				m.report("Copilot: "+eventError(e), true)
			}
		}
		return nil
	}
	if e.Kind == engine.EventError && !m.turn {
		if !e.History && m.recordAccessFailure(e.Err, e.Access, false) {
			m.notifyAccessFailure()
		} else {
			m.report("Copilot: "+eventError(e), true)
		}
		return nil
	}
	if !e.History && !m.turn && e.Kind != engine.EventUsage {
		if envelope.decision != nil {
			return m.resolveCommand(envelope.decision, answer{cancel: true})
		}
		return nil
	}
	if e.ID != "" && (e.Kind != engine.EventDelta || e.ID != e.MessageID) {
		key := string(e.Kind) + "/" + e.ID
		if m.seenEvents[key] {
			return nil
		}
		m.seenEvents[key] = true
	}
	switch e.Kind {
	case engine.EventDelta, engine.EventMessage:
		m.mergeMessage(e)
		if e.Kind == engine.EventMessage {
			m.flushTimeline()
		}
	case engine.EventToolStart, engine.EventToolOutput, engine.EventToolEnd:
		if !e.History {
			if e.Kind == engine.EventToolStart {
				m.recordToolStart(e.ToolID, e.Name, e.Arguments)
			} else if e.Kind == engine.EventToolEnd {
				m.recordToolEnd(e.ToolID, e.Name, e.Arguments, e.Failed)
			}
		}
		m.mergeTool(e)
		if e.Kind != engine.EventToolOutput {
			m.flushTimeline()
		}
	case engine.EventPermission, engine.EventQuestion:
		if envelope.decision == nil {
			m.report("The agent sent an incomplete permission or question event; no action was approved.", true)
			return m.cancelWork()
		}
		if e.History || m.needsAbort {
			return m.resolveCommand(envelope.decision, answer{cancel: true})
		}
		d := envelope.decision
		if d.resolved.Load() {
			return nil
		}
		if e.Kind == engine.EventPermission && m.autopilotEnabled {
			return m.resolveCommand(d, answer{allow: true})
		}
		if !m.seenRequests[d.key] {
			m.seenRequests[d.key] = true
			m.requests = append(m.requests, d)
			if len(m.requests) == 1 {
				m.behindDecision = m.overlay
				m.showDecision()
			}
		}
	case engine.EventUsage:
		m.usage = singleLine(e.Text)
	case engine.EventIdle:
		if e.History {
			return nil
		}
		errored := m.needsAbort
		m.lastIdleTurn = m.turnSequence
		m.turn = false
		m.needsAbort = false
		if !m.sendPending && m.turnCancel != nil {
			m.turnCancel()
			m.turnCancel = nil
		}
		for _, item := range m.entries {
			if item.role == "assistant" && !item.final {
				item.final = true
				item.touch()
			}
		}
		m.renderDirty = true
		m.flushTimeline()
		var moment tea.Cmd
		if errored {
			m.report("Turn ended after an error. Nothing was retried; completed edits remain in your working tree.", false)
		} else {
			m.report("Turn complete. Completed edits remain in your working tree.", false)
			if !m.sendPending {
				moment = m.celebrateValidation()
			}
		}
		return tea.Batch(m.cancelDecisions(), m.refreshStatus(), moment)
	case engine.EventError:
		if e.History {
			m.report("Earlier conversation error: "+eventError(e), true)
			return nil
		}
		m.needsAbort = true
		m.experience.errored = true
		m.stopCards("failed")
		detail := eventError(e)
		if m.recordAccessFailure(e.Err, e.Access, false) {
			issue, _ := m.currentAccessIssue()
			detail = accessTitle(issue) + ". " + m.accessHint(issue)
		}
		m.report(m.recoveryCopy("Copilot: "+detail+". Ctrl+C stops the unresolved turn; /login then reconnects if needed. Nothing was retried."), true)
		m.flushTimeline()
		return tea.Batch(m.cancelDecisions(), m.startMoment(reactionRecovery))
	}
	return nil
}

func eventError(e engine.Event) string {
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Text != "" {
		return e.Text
	}
	return "the Copilot engine reported an unspecified error"
}

func (m *Model) messageKey(e engine.Event, role string) string {
	if role == "tool" && e.ToolID != "" {
		return "tool:" + e.ToolID
	}
	if e.MessageID != "" {
		return "message:" + role + ":" + e.MessageID
	}
	if role == "assistant" && !e.History && m.streamKey != "" {
		return m.streamKey
	}
	if e.ID != "" && (e.History || e.Kind == engine.EventMessage) {
		return "message:" + role + ":" + e.ID
	}
	return "message:" + role + ":" + m.nextEntryID()
}

func (m *Model) mergeMessage(event engine.Event) {
	role := event.Role
	if role == "" {
		role = "assistant"
	}
	if role == "user" {
		role = "you"
	}
	id := m.messageKey(event, role)
	if !event.History && m.retired[id] {
		return
	}
	e := m.messages[id]
	if e == nil && role == "tool" {
		e = m.tools[id]
	}
	if e == nil && role == "you" && !event.History && m.localUser != nil && m.localUser.raw == event.Text {
		e = m.localUser
		delete(m.messages, e.id)
		e.id = id
		m.messages[id] = e
	}
	if e == nil {
		e = &entry{id: id, role: role, history: event.History}
		m.entries = append(m.entries, e)
		m.messages[id] = e
	}
	if role == "tool" {
		m.messages[id] = e
		if event.Name != "" {
			e.name = event.Name
		} else if e.name == "" {
			e.name = "tool result"
		}
		e.state = "done"
		if event.Arguments != "" {
			e.arguments = event.Arguments[:min(len(event.Arguments), maxToolBytes)]
			e.inputTruncated = len(event.Arguments) > maxToolBytes
		}
		if event.History {
			e.state = "history"
		}
		if event.Failed {
			e.state = "failed"
			if event.History {
				e.state = "history / failed"
			}
		}
		m.tools[id] = e
	}
	if event.Kind == engine.EventDelta {
		if e.final {
			return
		}
		e.append(event.Text, maxMessageBytes)
		m.streamKey = id
	} else {
		if event.Text != "" || e.raw == "" {
			e.replace(event.Text, maxMessageBytes)
		}
		e.final = true
		e.touch()
		if m.streamKey == id {
			m.streamKey = ""
		}
	}
	m.renderDirty = true
}

func (m *Model) mergeTool(event engine.Event) {
	id := event.ToolID
	if id == "" {
		id = event.ID
	}
	if id == "" {
		m.report("The agent sent a tool event without an identifier; it cannot be correlated safely.", true)
		return
	}
	id = "tool:" + id
	if !event.History && m.retired[id] {
		return
	}
	e := m.tools[id]
	if e == nil {
		e = &entry{id: id, role: "tool", name: event.Name, state: "running", history: event.History}
		m.entries = append(m.entries, e)
		m.tools[id] = e
	}
	if event.Name != "" {
		e.name = event.Name
	}
	if event.Arguments != "" {
		e.arguments = event.Arguments[:min(len(event.Arguments), maxToolBytes)]
		e.inputTruncated = len(event.Arguments) > maxToolBytes
	}
	switch event.Kind {
	case engine.EventToolStart:
		if event.Text != "" && e.raw == "" {
			e.replace(event.Text, maxToolBytes)
		}
	case engine.EventToolOutput:
		if !e.final {
			e.append(event.Text, maxToolBytes)
		}
	case engine.EventToolEnd:
		if event.Text != "" {
			e.replace(event.Text, maxToolBytes)
		}
		e.final = true
		e.state = "done"
		if event.Failed {
			e.state = "failed"
		}
	}
	e.touch()
	m.renderDirty = true
}

func (m *Model) flushTimeline() {
	if !m.renderDirty {
		return
	}
	width := max(1, m.layout.innerWidth)
	contentWidth := messageContentWidth(width)
	if !m.prefs.NoColor && (m.renderer == nil || m.renderWidth != contentWidth) {
		renderer, err := newMarkdown(contentWidth, m.color)
		if err != nil {
			m.report("Markdown styling is unavailable: "+err.Error()+". Showing safe plain text.", true)
		}
		m.renderer, m.renderWidth = renderer, contentWidth
	}
	parts := make([]string, 0, len(m.entries)+1)
	for index, e := range m.entries {
		if e.cachedRevision != e.revision || e.cachedWidth != width || e.cachedAppearance != m.appearance {
			e.cached = m.renderEntry(e, index, width)
			e.cachedRevision, e.cachedWidth, e.cachedAppearance = e.revision, width, m.appearance
			m.renderCount++
		}
		parts = append(parts, e.cached)
	}
	if m.loadingVisible() {
		parts = append(parts, m.loadingView(width))
	}
	if moment := m.momentView(width); moment != "" {
		parts = append(parts, moment)
	}
	m.timeline.SetContent(strings.Join(parts, "\n\n"))
	if m.follow {
		m.timeline.GotoBottom()
	}
	m.renderDirty = false
}

func (m *Model) renderEntry(e *entry, index, width int) string {
	c := m.color
	if e.role == "tool" {
		return m.renderTool(e, index, width)
	}
	if e.role == "context" {
		text := e.raw
		if e.context != nil {
			text = m.renderContextUsage(*e.context, messageContentWidth(width))
		}
		return m.renderMessageRail(e.role, text, width)
	}
	text := safeText(e.raw)
	if e.role == "assistant" && m.renderer != nil && !m.prefs.NoColor {
		rendered, err := m.renderer.Render(text)
		if err != nil {
			text += "\n[Markdown could not be styled; showing plain text.]"
		} else {
			text = strings.TrimSpace(safeMarkdown(rendered, false))
		}
	}
	if e.role == "notice" {
		text = c.paint(c.amber, "NOTICE") + "\n" + text
	} else if e.role != "you" && e.role != "assistant" {
		text = c.paint(c.muted, strings.ToUpper(singleLine(e.role))) + "\n" + text
	}
	if e.state != "" {
		text += "\n[" + singleLine(e.state) + "]"
	}
	if e.truncated {
		text += "\n[Message truncated in the UI; full history remains with the engine.]"
	}
	return m.renderMessageRail(e.role, ansi.Wrap(text, messageContentWidth(width), ""), width)
}

func messageContentWidth(width int) int {
	if width <= 1 {
		return 1
	}
	return max(1, width-2)
}

func (m *Model) renderMessageRail(role, text string, width int) string {
	if width <= 0 {
		return ""
	}
	glyph, accent := m.color.glyph("│", "|"), m.color.muted
	switch role {
	case "you":
		accent = m.color.cyan
	case "assistant":
		glyph, accent = m.color.glyph("┃", ":"), m.color.magenta
	case "notice":
		glyph, accent = m.color.glyph("┆", "!"), m.color.amber
	}
	prefix := m.color.paint(accent, glyph)
	if width > 1 {
		prefix += " "
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = clip(prefix+line, width)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderTool(e *entry, index, width int) string {
	c := m.color
	marker := c.glyph("▸", ">")
	if e.expanded {
		marker = c.glyph("▾", "v")
	}
	accent := c.muted
	if e.state == "running" {
		accent = c.cyan
	} else if e.state == "done" {
		accent = c.lime
	} else if e.state == "failed" || e.state == "history / failed" {
		accent = c.red
	}
	name := singleLine(e.name)
	if name == "" {
		name = "tool"
	}
	header := marker + " " + name + "  [" + e.state + "]"
	if m.toolFocus && m.selectedTool == index {
		header = c.badge("> "+header, c.cyan)
	} else {
		header = c.paint(accent, header)
	}
	lines := []string{clip(header, width)}
	if e.arguments != "" {
		if e.expanded {
			lines = append(lines, c.paint(c.muted, "Input"), ansi.Wrap(safeText(e.arguments), width, ""))
			if e.inputTruncated {
				lines = append(lines, c.paint(c.amber, "[Input truncated in the UI.]"))
			}
		} else {
			lines = append(lines, c.paint(c.muted, clip(singleLine(e.arguments), width)))
		}
	}
	if e.expanded {
		if e.raw != "" {
			lines = append(lines, c.paint(c.muted, "Output"), ansi.Wrap(safeText(e.raw), width, ""))
		} else {
			lines = append(lines, c.paint(c.muted, "(no output yet)"))
		}
		if e.truncated {
			lines = append(lines, c.paint(c.amber, "[Output truncated in the UI.]"))
		}
	} else {
		lines = append(lines, c.paint(c.muted, "F4 focus tools / Enter expand"))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) showDecision() {
	if len(m.requests) == 0 {
		m.overlay = m.behindDecision
		m.behindDecision = nil
		return
	}
	request := m.requests[0]
	m.overlay = nil
	if p := request.permission; p != nil {
		body := "Allow this action?\n\n"
		if p.Kind != "" {
			body += "Kind: " + safeText(p.Kind) + "\n"
		}
		if p.Path != "" {
			body += "Path: " + visibleDetail(p.Path, false) + "\n"
		}
		if p.Command != "" {
			body += "Command:\n" + visibleDetail(p.Command, true) + "\n"
		}
		if p.URL != "" {
			body += "URL: " + visibleDetail(p.URL, false) + "\n"
		}
		if p.Description != "" {
			body += "\n" + safeText(p.Description) + "\n"
		}
		body += "\nAllow-once and Autopilot are not sandboxes. Completed edits are not undone by cancellation."
		d := m.newDialog(dialogPermission, "PERMISSION / your decision", body)
		d.items = []menuItem{
			{id: "deny", label: "Deny", detail: "D / Esc"},
			{id: "allow", label: "Allow once", detail: "A / this request only"},
			{id: "autopilot", label: "Enable Autopilot", detail: "Shift+A / this conversation"},
		}
	} else if q := request.question; q != nil {
		d := m.newDialog(dialogQuestion, "QUESTION / the agent needs you", q.Prompt)
		for i, choice := range q.Choices {
			d.items = append(d.items, menuItem{id: fmt.Sprintf("choice:%d", i), label: safeText(choice)})
		}
		if q.AllowFreeform {
			d.items = append(d.items, menuItem{id: "freeform", label: "Write an answer", detail: "Free text; Enter sends; Ctrl+J newline"})
		}
		d.items = append(d.items, menuItem{id: "cancel-question", label: "Cancel question", detail: "Esc"})
		m.answerInput.Reset()
	}
}

func (m *Model) resolveCommand(d *decision, a answer) tea.Cmd {
	generation, turn := m.engineGeneration, m.turnSequence
	return func() tea.Msg {
		return decisionMsg{generation: generation, turn: turn, request: true, err: d.resolve(a)}
	}
}

func (m *Model) answerDecision(a answer) tea.Cmd {
	if len(m.requests) == 0 {
		m.report("That request is no longer active.", true)
		m.closeDialog()
		return nil
	}
	d := m.requests[0]
	m.requests = m.requests[1:]
	m.overlay = nil
	m.showDecision()
	return m.resolveCommand(d, a)
}

func (m *Model) enableAutopilotForPermissions() tea.Cmd {
	m.setAutopilot(true)
	requests := m.requests
	m.requests = nil
	var remaining []*decision
	var cmds []tea.Cmd
	for _, d := range requests {
		if d.permission != nil {
			cmds = append(cmds, m.resolveCommand(d, answer{allow: true}))
			continue
		}
		remaining = append(remaining, d)
	}
	m.requests = remaining
	m.overlay = nil
	m.showDecision()
	m.report(m.autopilotNotice(), false)
	return tea.Batch(cmds...)
}

func (m *Model) cancelDecisions() tea.Cmd {
	requests := m.requests
	m.requests = nil
	if m.overlay != nil && (m.overlay.kind == dialogPermission || m.overlay.kind == dialogQuestion) {
		m.overlay = m.behindDecision
	}
	m.behindDecision = nil
	var cmds []tea.Cmd
	for _, d := range requests {
		cmds = append(cmds, m.resolveCommand(d, answer{cancel: true}))
	}
	return tea.Batch(cmds...)
}

func (m *Model) discardBufferedEvents() tea.Cmd {
	buffer := m.sessionBuffer
	m.sessionBuffer = nil
	var cmds []tea.Cmd
	for _, e := range buffer {
		if e.decision != nil {
			cmds = append(cmds, m.resolveCommand(e.decision, answer{cancel: true}))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) stopCards(state string) {
	for _, e := range m.entries {
		if e.role == "tool" || e.role == "assistant" {
			m.retired[e.id] = true
			if !e.final {
				e.final = true
				e.state = state
				e.touch()
			}
		}
	}
	m.renderDirty = true
}

func (m *Model) cancelWork() tea.Cmd {
	if m.loggingIn {
		return m.cancelLogin()
	}
	if m.connecting {
		if m.connectCancel != nil {
			m.connectCancel()
		}
		m.engineGeneration++
		m.connecting = false
		l := m.lease
		m.lease = nil
		m.report("Connection cancelled. /login reconnects; your draft is kept.", false)
		if l != nil {
			return func() tea.Msg { return decisionMsg{err: l.close()} }
		}
		return nil
	}
	if m.canceling {
		m.report("Still waiting for the engine to stop. Ctrl+Q offers stop-and-exit.", false)
		return nil
	}
	if !m.turn && m.operation.kind == "" && !m.sendPending && !m.needsAbort && len(m.requests) == 0 {
		if strings.TrimSpace(m.composer.Value()) != "" {
			m.confirmExit()
			return nil
		}
		return m.quit()
	}
	if m.operation.kind == "signing out" {
		m.report("Sign-out is in progress. Ctrl+Q offers exit.", false)
		return nil
	}
	if m.operation.kind == "preparing taste test" {
		m.finishOperation()
		m.report("Taste test preparation cancelled. Your draft is kept; no prompt was sent.", false)
		return nil
	}
	if m.turnCancel != nil {
		m.turnCancel()
		m.turnCancel = nil
	}
	if m.operation.kind != "" && m.session.ID != "" {
		m.needsResume = true
	}
	m.abortSessionID = m.session.ID
	if m.operation.kind == "starting conversation" || m.operation.kind == "resuming" {
		m.abortSessionID = ""
	}
	m.finishOperation()
	m.turnSequence++
	m.turn, m.sendPending = false, false
	m.needsAbort = false
	if m.pendingPrompt != nil && m.composer.Value() == "" {
		m.composer.SetValue(m.submitted)
	}
	m.pendingPrompt = nil
	m.stopCards("stopped")
	m.flushTimeline()
	m.overlay = nil
	m.report("Stopping the turn. Your draft is kept; completed edits will NOT be undone. Nothing is queued.", false)
	cmds := []tea.Cmd{m.cancelDecisions(), m.discardBufferedEvents()}
	if m.lease != nil {
		m.canceling = true
		m.abortAcknowledged, m.abortBarrierSeen = false, false
		l, generation, life := m.lease, m.engineGeneration, m.life
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(l.ctx, 15*time.Second)
			defer cancel()
			err := accountAction(life, ctx, func() error { return l.engine.Abort(ctx) })
			return abortedMsg{generation: generation, err: err}
		})
	}
	return tea.Batch(cmds...)
}

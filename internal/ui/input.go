package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/VeVarunSharma/sodapop/internal/commands"
	"github.com/VeVarunSharma/sodapop/internal/engine"
)

const wheelScrollLines = 3

func (m *Model) handleMouseWheel(msg tea.MouseWheelMsg) tea.Cmd {
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseWheelUp && mouse.Button != tea.MouseWheelDown {
		return nil
	}
	m.finishStartup()
	if m.overlay != nil {
		if mouse.Button == tea.MouseWheelUp {
			m.overlay.view.ScrollUp(wheelScrollLines)
		} else {
			m.overlay.view.ScrollDown(wheelScrollLines)
		}
		m.overlay.scroll = m.overlay.view.YOffset()
		return nil
	}
	if m.layout.sidebarVisible && mouse.X >= m.layout.sidebarX {
		if mouse.Button == tea.MouseWheelUp {
			m.sidebar.ScrollUp(wheelScrollLines)
		} else {
			m.sidebar.ScrollDown(wheelScrollLines)
		}
		return nil
	}
	if mouse.Y < m.layout.header || mouse.Y >= m.layout.composerY {
		return nil
	}
	if mouse.Button == tea.MouseWheelUp {
		m.timeline.ScrollUp(wheelScrollLines)
	} else {
		m.timeline.ScrollDown(wheelScrollLines)
	}
	m.follow = m.timeline.AtBottom()
	return nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.quitting {
		return nil
	}
	m.finishStartup()
	switch msg.String() {
	case "ctrl+c":
		return m.cancelWork()
	case "ctrl+q":
		m.confirmExit()
		return nil
	case "f1":
		return m.runLocal("help")
	case "f2":
		if m.overlay == nil || m.overlay.kind == dialogPermission {
			return m.toggleAutopilot()
		}
		return nil
	case "ctrl+p":
		m.showActions()
		return nil
	}
	if m.overlay != nil {
		return m.handleDialogKey(msg)
	}
	if msg.String() == "f3" {
		m.focusSidebar(!m.sidebarFocus)
		return nil
	}
	if m.sidebarFocus {
		switch msg.String() {
		case "esc", "f3":
			m.focusSidebar(false)
		case "up", "k":
			m.scrollSidebar(-1)
		case "down", "j":
			m.scrollSidebar(1)
		case "pgup", "ctrl+u":
			m.sidebar.PageUp()
		case "pgdown", "ctrl+d":
			m.sidebar.PageDown()
		case "home", "ctrl+home":
			m.sidebar.GotoTop()
		case "end", "ctrl+end":
			m.sidebar.GotoBottom()
		}
		return nil
	}
	switch msg.String() {
	case "pgup", "alt+up":
		if msg.String() == "pgup" {
			m.timeline.PageUp()
		} else {
			m.timeline.ScrollUp(3)
		}
		m.follow = m.timeline.AtBottom()
		return nil
	case "pgdown", "alt+down":
		if msg.String() == "pgdown" {
			m.timeline.PageDown()
		} else {
			m.timeline.ScrollDown(3)
		}
		m.follow = m.timeline.AtBottom()
		return nil
	case "ctrl+home":
		m.timeline.GotoTop()
		m.follow = false
		return nil
	case "ctrl+end":
		m.timeline.GotoBottom()
		m.follow = true
		return nil
	case "f4":
		m.focusTools(!m.toolFocus)
		return nil
	}
	if m.toolFocus {
		switch msg.String() {
		case "esc":
			m.focusTools(false)
		case "up", "shift+tab":
			m.moveTool(-1)
		case "down", "tab":
			m.moveTool(1)
		case "enter", "space":
			m.expandTool()
		}
		return nil
	}
	m.updatePalette()
	if m.paletteOpen {
		switch msg.String() {
		case "up", "shift+tab":
			m.movePalette(-1)
			return nil
		case "down":
			m.movePalette(1)
			return nil
		case "tab":
			if len(m.paletteItems) > 0 {
				m.composer.SetValue("/" + m.paletteItems[m.paletteIndex].id + " ")
				m.composer.MoveToEnd()
				m.paletteOpen = false
			}
			return nil
		case "enter":
			if len(m.paletteItems) > 0 {
				item := m.paletteItems[m.paletteIndex]
				if item.disabled {
					m.report("That action is unavailable while work is active. Ctrl+C cancels; nothing was queued.", true)
					return nil
				}
				m.composer.SetValue("/" + item.id)
			}
			return m.submit()
		case "esc":
			m.paletteHidden = m.composer.Value()
			m.paletteOpen = false
			return nil
		}
	}
	switch msg.String() {
	case "enter":
		return m.submit()
	case "shift+tab":
		m.cycleComposerMode()
		m.notice = notice{}
		return nil
	case "esc":
		m.notice = notice{}
		return nil
	}
	if msg.Text != "" {
		msg.Text = safeText(msg.Text)
	}
	before := m.composer.Value()
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	if m.composer.Value() != before {
		m.paletteHidden = ""
		m.paletteIndex = 0
	}
	m.reflowComposer()
	m.updatePalette()
	return cmd
}

func (m *Model) handlePaste(text string) tea.Cmd {
	if m.quitting {
		return nil
	}
	m.finishStartup()
	if m.overlay != nil {
		if m.overlay.kind == dialogQuestion && m.overlay.freeform {
			var cmd tea.Cmd
			m.answerInput, cmd = m.answerInput.Update(tea.PasteMsg{Content: text})
			return cmd
		}
		return nil
	}
	if m.toolFocus {
		m.focusTools(false)
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(tea.PasteMsg{Content: text})
	m.paletteHidden = ""
	m.paletteIndex = 0
	m.reflowComposer()
	m.updatePalette()
	return cmd
}

func (m *Model) updatePalette() {
	value := m.composer.Value()
	m.paletteOpen = false
	if m.overlay != nil || value == m.paletteHidden || !strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "//") || strings.ContainsAny(value, " \t\r\n") {
		return
	}
	matches := commands.Match(strings.TrimPrefix(value, "/"))
	m.paletteItems = nil
	for _, command := range matches {
		m.paletteItems = append(m.paletteItems, menuItem{
			id: command.Name, label: "/" + command.Name, detail: command.Summary,
			disabled: m.busy() && !command.AllowedWhileBusy,
		})
	}
	m.paletteIndex = min(m.paletteIndex, max(0, len(m.paletteItems)-1))
	m.paletteOpen = true
}

func (m *Model) movePalette(delta int) {
	if len(m.paletteItems) > 0 {
		m.paletteIndex = (m.paletteIndex + delta + len(m.paletteItems)) % len(m.paletteItems)
	}
}

func (m *Model) showActions() {
	d := m.newDialog(dialogActions, "QUICK ACTIONS / keyboard palette", "Local actions never send a model prompt.")
	for _, command := range commands.All() {
		d.items = append(d.items, menuItem{
			id: "command:" + command.Name, label: command.Usage, detail: command.Summary,
			disabled: m.busy() && !command.AllowedWhileBusy,
		})
	}
}

func (m *Model) showVendingMachine() {
	d := m.newDialog(
		dialogVending,
		"VENDING MACHINE / choose a can",
		"Curated Sodapop workflows and settings. Ctrl+P remains the complete local action palette.",
	)
	for _, command := range commands.Vending() {
		d.items = append(d.items, menuItem{
			id: "command:" + command.Name, label: command.Usage, detail: command.Summary,
			group: command.VendingCategory, disabled: m.busy() && !command.AllowedWhileBusy,
		})
	}
}

func (m *Model) handleDialogKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.overlay
	if msg.String() == "esc" {
		switch d.kind {
		case dialogLogin:
			return m.cancelLogin()
		case dialogPermission, dialogQuestion:
			return m.answerDecision(answer{cancel: true})
		default:
			m.closeDialog()
			return nil
		}
	}
	if d.kind == dialogLogin {
		switch msg.String() {
		case "o":
			return m.openDeviceBrowser()
		case "c":
			return m.copyDeviceCode()
		}
	}
	if d.kind == dialogPermission {
		switch msg.String() {
		case "a":
			return m.answerDecision(answer{allow: true})
		case "A", "shift+a":
			return m.enableAutopilotForPermissions()
		case "d":
			return m.answerDecision(answer{cancel: true})
		}
	}
	if d.kind == dialogQuestion && d.freeform {
		if msg.String() == "enter" {
			text := m.answerInput.Value()
			if strings.TrimSpace(text) == "" {
				m.report("Enter an answer, or press Esc to cancel the question.", true)
				return nil
			}
			return m.answerDecision(answer{text: text})
		}
		if msg.Text != "" {
			msg.Text = safeText(msg.Text)
		}
		var cmd tea.Cmd
		m.answerInput, cmd = m.answerInput.Update(msg)
		return cmd
	}
	if d.kind == dialogModels && len(d.items) > 0 {
		switch msg.String() {
		case "home":
			d.selected = 0
			return nil
		case "end":
			d.selected = len(d.items) - 1
			return nil
		case "up", "k":
			d.selected = (d.selected - 1 + len(d.items)) % len(d.items)
			return nil
		case "down", "j":
			d.selected = (d.selected + 1) % len(d.items)
			return nil
		case "tab":
			m.adjustModelContext(d, 1)
			return nil
		case "shift+tab":
			m.adjustModelContext(d, -1)
			return nil
		case "left":
			m.adjustModelEffort(d, -1)
			return nil
		case "right":
			m.adjustModelEffort(d, 1)
			return nil
		case "enter":
			item := d.items[d.selected]
			cmd, _ := m.selectModelSelection(d.modelDrafts[item.id])
			return cmd
		}
	}
	switch msg.String() {
	case "pgup", "ctrl+u":
		d.view.PageUp()
		d.scroll = d.view.YOffset()
		return nil
	case "pgdown", "ctrl+d":
		d.view.PageDown()
		d.scroll = d.view.YOffset()
		return nil
	case "home":
		if len(d.items) > 0 {
			d.selected = 0
		} else {
			d.view.GotoTop()
			d.scroll = d.view.YOffset()
		}
		return nil
	case "end":
		if len(d.items) > 0 {
			d.selected = len(d.items) - 1
		} else {
			d.view.GotoBottom()
			d.scroll = d.view.YOffset()
		}
		return nil
	case "up", "shift+tab", "k":
		if len(d.items) > 0 {
			d.selected = (d.selected - 1 + len(d.items)) % len(d.items)
		} else {
			d.view.ScrollUp(1)
			d.scroll = d.view.YOffset()
		}
		return nil
	case "down", "tab", "j":
		if len(d.items) > 0 {
			d.selected = (d.selected + 1) % len(d.items)
		} else {
			d.view.ScrollDown(1)
			d.scroll = d.view.YOffset()
		}
		return nil
	case "enter":
		if len(d.items) == 0 {
			return nil
		}
		item := d.items[d.selected]
		if item.disabled {
			m.report("Finish or cancel active work first. Your draft is kept; nothing was queued.", true)
			return nil
		}
		return m.chooseItem(d, item)
	}
	return nil
}

func (m *Model) chooseItem(d *dialog, item menuItem) tea.Cmd {
	switch d.kind {
	case dialogActions, dialogVending:
		m.overlay = nil
		return m.runLocal(strings.TrimPrefix(item.id, "command:"))
	case dialogAccount:
		switch item.id {
		case "login":
			return m.startLogin(false)
		case "session-login":
			return m.startLogin(true)
		case "reconnect":
			return m.connect()
		case "copilot-plans":
			return m.copilotLinkCommand(copilotPlans, false)
		case "copy-copilot-plans":
			return m.copilotLinkCommand(copilotPlans, true)
		case "copilot-settings":
			return m.copilotLinkCommand(copilotSettings, false)
		case "copy-copilot-settings":
			return m.copilotLinkCommand(copilotSettings, true)
		case "signout":
			if m.busy() || len(m.requests) > 0 {
				m.confirm("signout", "", "Stop and sign out?", "Pending permissions will be denied and unanswered questions cancelled. History, your draft, and completed edits are kept.", "Stop and sign out")
				return nil
			}
			return m.signOut()
		case "close":
			m.closeDialog()
		}
	case dialogLogin:
		switch item.id {
		case "open-browser":
			return m.openDeviceBrowser()
		case "copy-code":
			return m.copyDeviceCode()
		case "cancel-login":
			return m.cancelLogin()
		}
	case dialogModels:
		selection := d.modelDrafts[item.id]
		cmd, _ := m.selectModelSelection(selection)
		return cmd
	case dialogSessions:
		if strings.TrimSpace(m.composer.Value()) != "" {
			m.confirmResume(item.id)
			return nil
		}
		return m.resumeSession(item.id)
	case dialogThemes:
		cmd, _ := m.selectTheme(item.id)
		return cmd
	case dialogPermission:
		if item.id == "autopilot" {
			return m.enableAutopilotForPermissions()
		}
		return m.answerDecision(answer{allow: item.id == "allow", cancel: item.id != "allow"})
	case dialogQuestion:
		if item.id == "cancel-question" {
			return m.answerDecision(answer{cancel: true})
		}
		if item.id == "freeform" {
			d.freeform = true
			m.answerInput.Focus()
			return nil
		}
		if len(m.requests) > 0 && m.requests[0].question != nil {
			q := m.requests[0].question
			for i, choice := range q.Choices {
				if item.id == fmt.Sprintf("choice:%d", i) {
					return m.answerDecision(answer{text: choice})
				}
			}
		}
		m.report("That question choice is no longer available.", true)
	case dialogConfirm:
		if item.id == "confirm" {
			return m.confirmAction(d)
		}
		m.closeDialog()
	}
	return nil
}

func (m *Model) adjustModelContext(d *dialog, delta int) {
	item := d.items[d.selected]
	model, ok := m.modelByID(item.id)
	if !ok || len(model.ContextOptions) < 2 {
		return
	}
	selection := d.modelDrafts[item.id]
	index := 0
	for i, option := range model.ContextOptions {
		if option.Tier == selection.ContextTier {
			index = i
			break
		}
	}
	index = (index + delta + len(model.ContextOptions)) % len(model.ContextOptions)
	selection.ContextTier = model.ContextOptions[index].Tier
	d.modelDrafts[item.id] = selection
	d.cacheBody = ""
}

func (m *Model) adjustModelEffort(d *dialog, delta int) {
	item := d.items[d.selected]
	model, ok := m.modelByID(item.id)
	if !ok || len(model.ReasoningEfforts) < 2 {
		return
	}
	selection := d.modelDrafts[item.id]
	index := 0
	for i, effort := range model.ReasoningEfforts {
		if effort == selection.ReasoningEffort {
			index = i
			break
		}
	}
	index = max(0, min(len(model.ReasoningEfforts)-1, index+delta))
	selection.ReasoningEffort = model.ReasoningEfforts[index]
	d.modelDrafts[item.id] = selection
	d.cacheBody = ""
}

func (m *Model) modelByID(id string) (engine.Model, bool) {
	for _, model := range m.models {
		if model.ID == id {
			return model, true
		}
	}
	return engine.Model{}, false
}

func (m *Model) focusTools(focus bool) {
	if m.selectedTool >= 0 && m.selectedTool < len(m.entries) {
		m.entries[m.selectedTool].touch()
	}
	m.toolFocus = focus
	if focus {
		if m.selectedTool < 0 || m.selectedTool >= len(m.entries) || m.entries[m.selectedTool].role != "tool" {
			m.selectedTool = -1
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].role == "tool" {
					m.selectedTool = i
					break
				}
			}
		}
		if m.selectedTool < 0 {
			m.toolFocus = false
			m.report("No tool cards yet. They will appear as the agent works.", false)
			return
		}
		m.entries[m.selectedTool].touch()
	}
	m.renderDirty = true
	m.flushTimeline()
	if focus {
		m.revealTool()
	}
}

func (m *Model) moveTool(delta int) {
	indices := make([]int, 0, len(m.tools))
	selected := 0
	for i, e := range m.entries {
		if e.role == "tool" {
			if i == m.selectedTool {
				selected = len(indices)
			}
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return
	}
	m.entries[m.selectedTool].touch()
	selected = (selected + delta + len(indices)) % len(indices)
	m.selectedTool = indices[selected]
	m.entries[m.selectedTool].touch()
	m.renderDirty = true
	m.follow = false
	m.flushTimeline()
	m.revealTool()
}

func (m *Model) expandTool() {
	if m.selectedTool < 0 || m.selectedTool >= len(m.entries) {
		return
	}
	e := m.entries[m.selectedTool]
	e.expanded = !e.expanded
	e.touch()
	m.follow = false
	m.renderDirty = true
	m.flushTimeline()
	m.revealTool()
}

func (m *Model) revealTool() {
	line := 0
	for i, e := range m.entries {
		if i == m.selectedTool {
			m.timeline.SetYOffset(line)
			m.follow = m.timeline.AtBottom()
			return
		}
		line += strings.Count(e.cached, "\n") + 2
	}
}

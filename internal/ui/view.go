package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/charmbracelet/x/ansi"
)

func (m *Model) applyAppearance() {
	m.color = colors(m.prefs)
	m.appearance++
	m.renderer = nil
	m.renderWidth = 0
	m.renderDirty = true
	m.composerAccentApplied = m.composerAccent()
	m.composer.SetStyles(m.color.textareaStyles(m.prefs.ReducedMotion, m.composerAccentApplied))
	m.answerInput.SetStyles(m.color.textareaStyles(m.prefs.ReducedMotion, m.color.cyan))
	m.motionEpoch++
	m.motionPending = false
	if m.prefs.ReducedMotion {
		m.frame = 0
		m.finishStartup()
	}
	if m.layout.innerWidth > 0 {
		m.flushTimeline()
	}
}

func (m *Model) resize(width, height int) {
	m.width, m.height = width, height
	g := geometry{compact: width < 50 || height < 14, mainWidth: width}
	if !g.compact && width >= sidebarBreakpoint {
		sidebarWidth := min(sidebarMaxWidth, max(sidebarMinWidth, width/4))
		if width-sidebarWidth-sidebarGap >= minimumConversationWidth {
			g.sidebarVisible = true
			g.sidebarWidth = sidebarWidth
			g.mainWidth = width - sidebarWidth - sidebarGap
			g.sidebarX = g.mainWidth + sidebarGap
		}
	}
	if !g.sidebarVisible {
		m.sidebarFocus = false
	}
	if g.compact {
		g.input = m.composerHeight(g.mainWidth, min(3, max(1, height/5)))
		if height >= 3 {
			g.header = 1
		}
		if height >= 2 {
			g.footer = 1
		}
		g.input = min(g.input, max(1, height-g.header-g.footer))
		g.composer = g.input
		g.timeline = max(0, height-g.header-g.composer-g.footer)
		g.innerWidth = g.mainWidth
		g.composerY = g.header + g.timeline
	} else {
		g.header, g.footer = 2, 1
		g.input = m.composerHeight(g.mainWidth-4, min(5, max(1, height-8)))
		g.composer = g.input + 2
		g.timeline = max(1, height-g.header-g.composer-g.footer-2)
		g.innerWidth = g.mainWidth - 4
		g.composerY = g.header + g.timeline + 2
	}
	m.layout = g
	m.updateComposerPrompt()
	m.composer.SetWidth(max(1, g.innerWidth))
	m.composer.SetHeight(g.input)
	m.timeline.SetWidth(max(1, g.innerWidth))
	m.timeline.SetHeight(max(1, g.timeline))
	if g.sidebarVisible {
		m.sidebar.SetWidth(max(1, g.sidebarWidth-4))
		m.sidebar.SetHeight(max(1, height-2))
	}
	m.renderDirty = true
	m.flushTimeline()
	if m.follow {
		m.timeline.GotoBottom()
	}
}

// composerAccent keeps the composer border, prompt and cursor color tied to the
// active mode so the current mode is obvious at a glance.
func (m *Model) composerAccent() string {
	if m.autopilotEnabled {
		return m.autopilotAccent()
	}
	if m.planning {
		return m.color.amber
	}
	return m.color.cyan
}

func (m *Model) autopilotAccent() string {
	return m.color.magenta
}

// composerBubble pulses while Sodapop is working and rests as a dim bubble otherwise.
func (m *Model) composerBubble() string {
	if !m.loadingVisible() {
		if m.moment.id != 0 {
			return m.reactionGlyph()
		}
		return m.color.glyph("·", ".")
	}
	bubbles := []string{"·", "•", "●", "•"}
	if m.prefs.ASCII {
		bubbles = []string{".", "o", "O", "o"}
	}
	return bubbles[m.frame%len(bubbles)]
}

func (m *Model) composerPrompt() string {
	label := "chat> "
	if m.autopilotEnabled {
		label = "auto> "
	} else if m.planning {
		label = "plan> "
	}
	switch {
	case m.layout.innerWidth < 7:
		return ""
	case m.layout.innerWidth < 12:
		return label
	}
	return m.composerBubble() + " " + label
}

func (m *Model) updateComposerPrompt() {
	prompt := m.composerPrompt()
	accent := m.composerAccent()
	if m.composer.Prompt != prompt {
		m.composer.Prompt = prompt
		m.renderDirty = true
	}
	if m.composerAccentApplied != accent {
		m.composerAccentApplied = accent
		m.composer.SetStyles(m.color.textareaStyles(m.prefs.ReducedMotion, accent))
		m.renderDirty = true
	}
}

func (m *Model) composerHeight(width, limit int) int {
	if limit <= 1 {
		return 1
	}
	promptWidth := 8
	switch {
	case width < 7:
		promptWidth = 0
	case width < 12:
		promptWidth = 6
	}
	lineWidth := max(1, width-promptWidth)
	rows := 0
	for _, line := range strings.Split(m.composer.Value(), "\n") {
		rows += max(1, (ansi.StringWidth(line)+lineWidth-1)/lineWidth)
	}
	return min(limit, max(1, rows))
}

func (m *Model) reflowComposer() {
	if m.width < 1 || m.height < 1 {
		return
	}
	m.updateComposerPrompt()
	compact := m.layout.compact
	width, limit := m.layout.mainWidth-4, min(5, max(1, m.height-8))
	if compact {
		width, limit = m.layout.mainWidth, min(3, max(1, m.height/5))
	}
	if m.composerHeight(width, limit) != m.layout.input {
		m.resize(m.width, m.height)
	}
}

func (m *Model) View() tea.View {
	c, g := m.color, m.layout
	parts := make([]string, 0, 4)
	if g.header > 0 {
		parts = append(parts, m.headerView())
	}
	if g.timeline > 0 {
		content := m.timeline.View()
		if len(m.entries) == 0 {
			content = m.welcomeView()
		}
		if len(m.entries) == 0 && m.loadingVisible() {
			content = m.welcomeView() + "\n\n" + m.loadingView(g.innerWidth)
		}
		if g.compact {
			parts = append(parts, fitBlock(content, m.width, g.timeline))
		} else {
			title := "CONVERSATION / new"
			if m.session.ID != "" {
				title = "CONVERSATION / " + singleLine(m.session.Title)
				if m.session.Title == "" {
					title = "CONVERSATION / " + singleLine(m.session.ID)
				}
			}
			if !m.follow {
				title = fmt.Sprintf("SCROLLBACK / %d%% / Ctrl+End live", int(m.timeline.ScrollPercent()*100))
			}
			if m.toolFocus {
				title = "TOOLS / arrows select / Enter expand / Esc compose"
			}
			parts = append(parts, c.frame(title, fitBlock(content, g.innerWidth, g.timeline), g.mainWidth, c.raised))
		}
	}
	input := m.composer.View()
	if g.compact {
		parts = append(parts, fitBlock(input, m.width, g.input))
	} else {
		title := "CHAT / Shift+Tab next: plan / Enter send / Ctrl+J newline"
		if m.busy() {
			title = "DRAFT / work active; nothing is queued"
		}
		if m.planning {
			title = "ADVISORY PLAN / Shift+Tab next: Autopilot / normal approvals"
		}
		if m.autopilotEnabled {
			title = "AUTOPILOT / Shift+Tab next: chat / approvals bypassed"
		}
		parts = append(parts, c.frame(title, fitBlock(input, g.innerWidth, g.input), g.mainWidth, m.composerAccent()))
	}
	if g.footer > 0 {
		parts = append(parts, m.footerView())
	}
	main := c.background(c.ink).Render(fitBlock(strings.Join(parts, "\n"), g.mainWidth, m.height))
	base := main
	if g.sidebarVisible {
		gap := c.background(c.ink).Render(fitBlock("", sidebarGap, m.height))
		base = lipgloss.JoinHorizontal(lipgloss.Top, main, gap, m.sidebarView())
	}
	base = c.background(c.ink).Render(fitBlock(base, m.width, m.height))
	view := tea.NewView(base)
	if !m.toolFocus && !m.sidebarFocus && !m.quitting {
		view.Cursor = m.composer.Cursor()
		if view.Cursor != nil {
			view.Cursor.Y += g.composerY
			if !g.compact {
				view.Cursor.X += 2
				view.Cursor.Y++
			}
		}
	}
	if m.overlay != nil {
		panel, x, y, cursor := m.dialogView()
		view.Content = lipgloss.NewCompositor(
			lipgloss.NewLayer(base),
			lipgloss.NewLayer(panel).X(x).Y(y).Z(1),
		).Render()
		view.Cursor = cursor
	} else if m.paletteOpen {
		panel, x, y := m.commandPaletteView()
		view.Content = lipgloss.NewCompositor(
			lipgloss.NewLayer(base),
			lipgloss.NewLayer(panel).X(x).Y(y).Z(1),
		).Render()
	}
	view.Content = fitBlock(view.Content, m.width, m.height)
	if m.prefs.NoColor {
		view.Content = safeText(view.Content)
	} else {
		view.BackgroundColor = lipgloss.Color(c.ink)
		view.ForegroundColor = lipgloss.Color(c.text)
	}
	if view.Cursor != nil && (view.Cursor.X < 0 || view.Cursor.X >= m.width || view.Cursor.Y < 0 || view.Cursor.Y >= m.height) {
		view.Cursor = nil
	}
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "Sodapop / " + m.projectName()
	return view
}

func (m *Model) projectName() string {
	name := filepath.Base(m.opts.Project)
	if name == "." || name == "" {
		name = "project"
	}
	return singleLine(name)
}

func (m *Model) headerView() string {
	c := m.color
	width := m.layout.mainWidth
	if m.layout.compact {
		return c.background(c.ink).Render(fitLine(c.paint(c.magenta, "SODAPOP")+" / "+m.projectName()+" / "+m.operationLabel(), width))
	}
	spark := m.activityGlyph()
	project := m.projectName()
	if m.status.IsRepository {
		branch := singleLine(m.status.Branch)
		if branch == "" {
			branch = "unborn"
		}
		project += " / " + branch
		if len(m.status.Entries) > 0 {
			project += fmt.Sprintf(" [%d changed]", len(m.status.Entries))
		}
	}
	account := c.paint(c.muted, "/LOGIN")
	if m.identityLoading {
		account = c.paint(c.muted, "GitHub: checking")
	} else if m.account.Login != "" {
		label := "@" + singleLine(m.account.Login)
		if m.account.SessionOnly {
			label += " [session]"
		}
		account = c.paint(c.lime, label)
	}
	left := c.badge("SODAPOP", c.magenta) + " " + c.paint(c.cyan, spark) + "  " + c.paint(c.text, project)
	account = clip(account, min(30, width/3))
	first := fitLine(left, width-lipgloss.Width(account)-1) + account + " "
	status := m.operationLabel()
	if m.usage != "" {
		status += " / " + m.usage
	}
	mode := c.paint(c.muted, clip(status, width/2))
	if m.planning {
		mode = c.badge("ADVISORY PLAN / not read-only", c.amber)
	}
	if m.autopilotEnabled {
		mode = c.badge("AUTOPILOT / approvals bypassed", m.autopilotAccent())
	}
	second := c.paint(c.muted, " model: ") + c.paint(c.cyan, m.modelName())
	second = fitLine(second, max(0, width-lipgloss.Width(mode)-1)) + mode + " "
	return c.background(c.ink).Render(fitLine(first, width) + "\n" + fitLine(second, width))
}

func (m *Model) welcomeView() string {
	if issue, blocked := m.currentAccessIssue(); blocked && !m.connecting {
		return m.accessWelcomeView(issue)
	}
	if content, ok := m.mascotWelcome(); ok {
		return content
	}
	c, g := m.color, m.layout
	if g.compact {
		text := "Fresh conversation.\n/login account / F1 help\nType / for commands."
		if m.account.ID != "" {
			text = "Fresh conversation.\nType a prompt or /help."
		}
		return fitBlock(text, g.innerWidth, g.timeline)
	}
	lines := []string{
		c.paint(c.cyan, m.startupTagline()),
		c.paint(c.muted, "A fresh conversation in "+m.projectName()+"."),
		"",
	}
	switch {
	case m.identityLoading:
		lines = append(lines, "Checking your GitHub identity in the background...")
	case m.account.ID == "":
		lines = append(lines, c.paint(c.lime, "/login  Connect GitHub with native device sign-in"))
	case m.connecting:
		lines = append(lines, "Connecting to Copilot; your draft is ready when you are.")
	case m.model == "":
		lines = append(lines, c.paint(c.lime, "/model  Choose a Copilot model before your first prompt"))
	default:
		lines = append(lines, c.paint(c.lime, "Ready when you are. What should we build?"))
	}
	lines = append(lines, "",
		c.paint(c.magenta, "/help")+"    Field guide and keyboard shortcuts",
		c.paint(c.magenta, "/resume")+"  Return to a saved conversation",
		c.paint(c.magenta, "/theme")+"   Color, personality, motion and ASCII",
		"",
		c.paint(c.muted, "Nothing is sent until you press Enter."),
	)
	padding := max(0, (g.timeline-len(lines))/2)
	body := strings.Repeat("\n", padding) + strings.Join(lines, "\n")
	return fitBlock(body, g.innerWidth, g.timeline)
}

func (m *Model) footerView() string {
	c := m.color
	width := m.layout.mainWidth
	text := " Enter send  Shift+Tab mode  Wheel/PgUp/PgDn scroll  / commands  ^C cancel"
	accent := c.muted
	if m.layout.compact {
		text = " Wheel/PgUp/PgDn scroll  /help  ^C cancel"
	}
	if m.toolFocus {
		text = " Tools: arrows select  Enter expand/collapse  PgUp/PgDn scroll  Esc compose"
	}
	if m.sidebarFocus {
		text = " Sidebar: arrows/PgUp/PgDn scroll  Home/End jump  Esc compose"
	}
	if !m.follow {
		text = " Scrollback / PgUp PgDn / Ctrl+End follows live output"
	}
	if issue, blocked := m.currentAccessIssue(); blocked {
		text, accent = " "+m.accessHint(issue), c.amber
	}
	if m.notice.text != "" {
		text = " " + singleLine(m.notice.text)
		if m.notice.error {
			text = " ! " + singleLine(m.notice.text)
			accent = c.amber
		}
	}
	return c.background(c.ink).Render(fitLine(c.paint(accent, text), width))
}

func (m *Model) commandPaletteView() (string, int, int) {
	const (
		maxPaletteWidth = 88
		maxPaletteRows  = 8
		paletteGap      = 2
	)

	mainWidth := max(1, m.layout.mainWidth)
	width := min(maxPaletteWidth, max(6, mainWidth-8))
	width = min(width, mainWidth)
	inner := max(1, width-4)
	available := max(1, m.layout.composerY)
	gap := paletteGap
	if m.layout.compact {
		gap = 0
	}

	// Search, spacer, help, and the frame consume five rows. The command
	// window takes the remainder, capped to keep the palette lightweight.
	rows := min(maxPaletteRows, max(1, available-gap-5))
	start := 0
	if len(m.paletteItems) > rows {
		start = min(max(0, m.paletteIndex-rows+1), len(m.paletteItems)-rows)
	}
	end := min(len(m.paletteItems), start+rows)

	query := strings.TrimPrefix(singleLine(m.composer.Value()), "/")
	search := m.color.paint(m.color.magenta, "/")
	if query == "" {
		search += m.color.paint(m.color.muted, "  Type to search commands")
	} else {
		search += m.color.paint(m.color.text, query)
	}
	lines := []string{fitLine(search, inner), ""}
	if len(m.paletteItems) == 0 {
		lines = append(lines, fitLine(m.color.paint(m.color.amber, "No matching commands"), inner))
	} else {
		for i := start; i < end; i++ {
			lines = append(lines, m.commandPaletteLine(m.paletteItems[i], i == m.paletteIndex, inner))
		}
	}

	hint := "Enter run  ·  Tab complete  ·  Esc close"
	if m.prefs.ASCII {
		hint = "Enter run  |  Tab complete  |  Esc close"
	}
	if len(m.paletteItems) > 0 {
		position := fmt.Sprintf("%d/%d", m.paletteIndex+1, len(m.paletteItems))
		above, below := start, len(m.paletteItems)-end
		if above > 0 {
			position = fmt.Sprintf("↑ %d  %s", above, position)
			if m.prefs.ASCII {
				position = fmt.Sprintf("^ %d  %d/%d", above, m.paletteIndex+1, len(m.paletteItems))
			}
		}
		if below > 0 {
			if m.prefs.ASCII {
				position += fmt.Sprintf("  v %d", below)
			} else {
				position += fmt.Sprintf("  ↓ %d", below)
			}
		}
		hint = fitLine(m.color.paint(m.color.muted, position), min(20, inner)) +
			m.color.paint(m.color.muted, hint)
	}
	lines = append(lines, fitLine(hint, inner))

	title := fmt.Sprintf("COMMANDS / %d results", len(m.paletteItems))
	panel := m.color.frame(title, strings.Join(lines, "\n"), width, m.color.magenta)
	panel = fitBlock(panel, width, min(available, lipgloss.Height(panel)))
	x := max(0, (mainWidth-width)/2)
	y := max(0, m.layout.composerY-lipgloss.Height(panel)-gap)
	return panel, x, y
}

func (m *Model) commandPaletteLine(item menuItem, selected bool, width int) string {
	c := m.color
	marker := "  "
	if selected {
		marker = c.glyph("› ", "> ")
	}
	label := marker + singleLine(item.label)
	detail := singleLine(item.detail)
	if item.disabled {
		detail = "Unavailable while work is active"
	}

	if width <= 44 {
		text := fitLine(label, width)
		if selected {
			style := c.background(c.raised)
			if !c.noColor {
				style = style.Foreground(lipgloss.Color(c.cyan)).Bold(true)
			}
			return style.Render(text)
		}
		if item.disabled {
			return c.paint(c.muted, text)
		}
		return c.paint(c.text, text)
	}

	commandWidth := min(28, max(18, width/3))
	detailWidth := max(0, width-commandWidth-1)
	command := fitLine(label, commandWidth)
	description := fitLine(detail, detailWidth)
	if selected {
		commandStyle := c.background(c.raised)
		detailStyle := c.background(c.raised)
		if !c.noColor {
			commandStyle = commandStyle.Foreground(lipgloss.Color(c.cyan)).Bold(true)
			detailStyle = detailStyle.Foreground(lipgloss.Color(c.text))
		}
		return commandStyle.Render(command) + detailStyle.Render(" "+description)
	}
	if item.disabled {
		return c.paint(c.muted, command+" "+description)
	}
	return c.paint(c.text, command) + " " + c.paint(c.muted, description)
}

func (m *Model) menuLine(item menuItem, selected bool, width int) string {
	c := m.color
	marker := "  "
	if selected {
		marker = "> "
	}
	label := singleLine(item.label)
	if item.disabled {
		label += " [busy]"
	}
	text := marker + label
	if item.detail != "" && width > 44 {
		column := min(30, width/2)
		text = fitLine(text, column) + " " + singleLine(item.detail)
	}
	text = fitLine(text, width)
	if selected {
		s := c.background(c.raised)
		if !c.noColor {
			s = s.Foreground(lipgloss.Color(c.cyan)).Bold(true)
		}
		return s.Render(text)
	}
	if item.disabled {
		return c.paint(c.muted, text)
	}
	return c.paint(c.text, text)
}

func (m *Model) dialogView() (string, int, int, *tea.Cursor) {
	d, c := m.overlay, m.color
	width := min(88, max(6, m.width-6))
	if m.width < 30 {
		width = m.width
	}
	width = min(width, m.width)
	framed := width >= 6
	innerWidth := width
	frameHeight := 0
	if framed {
		innerWidth -= 4
		frameHeight = 2
	}
	available := max(1, m.layout.composerY)
	outerHeight := min(available, max(5, m.height-7))
	innerHeight := max(1, outerHeight-frameHeight)
	body := d.body
	if len(d.items) > 0 && (d.kind == dialogSessions || d.kind == dialogQuestion) {
		item := d.items[d.selected]
		body += "\n\nSelected: " + item.label
		if item.detail != "" {
			body += "\n" + item.detail
		}
	}
	if d.kind == dialogThemes {
		body += fmt.Sprintf("\n\nNow: %s / personality: %s / reduced motion: %t / no color: %t / ASCII: %t", m.prefs.Theme, m.personality(), m.prefs.ReducedMotion, m.prefs.NoColor, m.prefs.ASCII)
	}
	menuHeight := 0
	if len(d.items) > 0 && !d.freeform {
		rows := len(d.items)
		if d.kind == dialogModels {
			rows = m.modelMenuRowCount(d, innerWidth)
			menuHeight = min(rows, max(1, innerHeight-3))
		} else if d.kind == dialogVending {
			rows = m.groupedMenuRowCount(d)
			menuHeight = min(rows, max(1, innerHeight/2))
		} else {
			menuHeight = min(rows, max(1, innerHeight/2))
		}
	}
	answerHeight := 0
	if d.freeform {
		answerHeight = min(3, max(1, innerHeight/3))
	}
	separator := 0
	if menuHeight > 0 || answerHeight > 0 {
		separator = 1
	}
	hintHeight := 1
	if innerHeight <= 2 {
		hintHeight, separator = 0, 0
	}
	bodyHeight := max(0, innerHeight-menuHeight-answerHeight-separator-hintHeight)
	if d.cacheBody != body || d.cacheWidth != innerWidth {
		d.view.SetWidth(max(1, innerWidth))
		d.view.SetContent(m.dialogBody(body, max(1, innerWidth), d.kind))
		d.cacheBody, d.cacheWidth = body, innerWidth
	}
	d.view.SetHeight(max(1, bodyHeight))
	d.view.SetYOffset(d.scroll)
	lines := make([]string, 0, innerHeight)
	if bodyHeight > 0 {
		lines = append(lines, fitBlock(d.view.View(), innerWidth, bodyHeight))
	}
	if separator > 0 {
		lines = append(lines, "")
	}
	var cursor *tea.Cursor
	answerY := bodyHeight + separator
	if d.freeform {
		m.answerInput.SetWidth(max(1, innerWidth))
		m.answerInput.SetHeight(max(1, answerHeight))
		lines = append(lines, fitBlock(m.answerInput.View(), innerWidth, answerHeight))
		cursor = m.answerInput.Cursor()
	} else if menuHeight > 0 {
		if d.kind == dialogModels {
			lines = append(lines, m.modelMenuLines(d, innerWidth, menuHeight)...)
		} else if d.kind == dialogVending {
			lines = append(lines, m.groupedMenuLines(d, innerWidth, menuHeight)...)
		} else {
			start := max(0, d.selected-menuHeight+1)
			for i := start; i < min(len(d.items), start+menuHeight); i++ {
				lines = append(lines, m.menuLine(d.items[i], i == d.selected, innerWidth))
			}
		}
	}
	if hintHeight > 0 {
		hint := "Arrows/Tab select  Wheel/PgUp/PgDn details  Enter  Esc"
		if len(d.items) == 0 {
			hint = "Wheel / PgUp / PgDn scroll   Esc back"
		}
		if d.kind == dialogPermission {
			hint = "A once / Shift+A all / D deny / Wheel details"
		}
		if d.kind == dialogQuestion {
			hint = "Enter answer / Wheel/PgUp/PgDn details / Esc cancel"
		}
		if d.freeform {
			hint = "Enter answer / Ctrl+J newline / Esc cancel"
		}
		if d.kind == dialogLogin {
			hint = "O browser / C copy code / Esc cancel"
		}
		if d.kind == dialogModels && len(d.items) > 0 {
			hint = "Up/Down model  Tab context  Left/Right effort  Enter apply  Esc"
		}
		lines = append(lines, c.paint(c.muted, clip(hint, innerWidth)))
	}

	content := fitBlock(strings.Join(lines, "\n"), innerWidth, innerHeight)
	accent := c.magenta
	if d.kind == dialogPermission || d.kind == dialogConfirm {
		accent = c.amber
	}
	if framed {
		title := d.title
		if d.kind == dialogLogin && m.loggingIn {
			title = "CONNECT / GitHub / " + m.activityGlyph() + " waiting"
		}
		content = c.frame(title, content, width, accent)
	}
	content = fitBlock(content, width, min(available, lipgloss.Height(content)))
	x, y := (m.width-width)/2, max(0, (m.layout.composerY-lipgloss.Height(content))/2)
	if cursor != nil {
		cursor.X += x
		cursor.Y += y + answerY
		if framed {
			cursor.X += 2
			cursor.Y++
		}
	}
	return content, x, y, cursor
}

func (m *Model) groupedMenuRowCount(d *dialog) int {
	rows := len(d.items)
	previous := ""
	for _, item := range d.items {
		if item.group != previous {
			rows++
			previous = item.group
		}
	}
	return rows
}

func (m *Model) groupedMenuLines(d *dialog, width, height int) []string {
	rows := make([]string, 0, m.groupedMenuRowCount(d))
	selectedRow := 0
	previous := ""
	for i, item := range d.items {
		if item.group != previous {
			group := item.group
			if group == "" {
				group = "Other"
			}
			rows = append(rows, m.color.paint(m.color.magenta, fitLine(strings.ToUpper(group), width)))
			previous = item.group
		}
		if i == d.selected {
			selectedRow = len(rows)
		}
		rows = append(rows, m.menuLine(item, i == d.selected, width))
	}
	start := max(0, selectedRow-height+1)
	end := min(len(rows), start+height)
	return rows[start:end]
}

func contextOptionLabel(option engine.ContextOption) string {
	label := contextTierLabel(option.Tier)
	if option.Tokens > 0 {
		label += " · " + compactTokenCount(option.Tokens)
	}
	return label
}

func compactTokenCount(tokens int64) string {
	switch {
	case tokens >= 1_000_000 && tokens%1_000_000 == 0:
		return fmt.Sprintf("%dM", tokens/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%dK", tokens/1_000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}

func (m *Model) modelMenuLines(d *dialog, width, height int) []string {
	header, rows, selectedStart, selectedEnd := m.modelMenuRows(d, width)
	if height <= 1 {
		return []string{header}
	}
	bodyHeight := height - 1
	start := max(0, selectedEnd-bodyHeight+1)
	if selectedEnd-selectedStart+1 > bodyHeight {
		start = selectedStart
	}
	end := min(len(rows), start+bodyHeight)
	return append([]string{header}, rows[start:end]...)
}

func (m *Model) modelMenuRowCount(d *dialog, width int) int {
	_, rows, _, _ := m.modelMenuRows(d, width)
	return 1 + len(rows)
}

func (m *Model) modelMenuRows(d *dialog, width int) (string, []string, int, int) {
	wide := width >= 56
	header := m.modelTableHeader(width, wide)
	rows := make([]string, 0, len(d.items)*2+2)
	selectedStart, selectedEnd, previous := 0, 0, ""
	for i, item := range d.items {
		if item.group != previous {
			group := item.group
			if group == "" {
				group = "Other"
			}
			rows = append(rows, m.color.paint(m.color.magenta, fitLine(strings.ToUpper(group), width)))
			previous = item.group
		}
		model, ok := m.modelByID(item.id)
		if !ok {
			continue
		}
		selection := d.modelDrafts[item.id]
		selected := i == d.selected
		if i == d.selected {
			selectedStart = len(rows)
		}
		if wide {
			rows = append(rows, m.modelTableRow(item, model, selection, selected, width))
		} else {
			rows = append(rows, m.modelTableLine(modelRowName(item, item.id == m.model), selected, width))
			if selected {
				context := modelContextValue(model, selection, true)
				effort := modelEffortValue(model, selection, true)
				rows = append(rows,
					m.modelTableLine("  Context   "+context, true, width),
					m.modelTableLine("  Thinking  "+effort, true, width),
				)
			}
		}
		if selected {
			selectedEnd = len(rows) - 1
		}
	}
	return header, rows, selectedStart, selectedEnd
}

func (m *Model) modelTableHeader(width int, wide bool) string {
	if !wide {
		return m.color.paint(m.color.muted, fitLine("  MODEL / CONTEXT / THINKING", width))
	}
	modelWidth, contextWidth, effortWidth := modelColumnWidths(width)
	line := "  " + fitLine("MODEL", modelWidth) + "  " +
		fitLine("CONTEXT", contextWidth) + "  " + fitLine("THINKING", effortWidth)
	return m.color.paint(m.color.muted, fitLine(line, width))
}

func (m *Model) modelTableRow(item menuItem, model engine.Model, selection engine.ModelSelection, selected bool, width int) string {
	modelWidth, contextWidth, effortWidth := modelColumnWidths(width)
	marker := "  "
	if selected {
		marker = "> "
	}
	line := marker + fitLine(modelRowName(item, item.id == m.model), modelWidth) + "  " +
		fitLine(modelContextValue(model, selection, selected), contextWidth) + "  " +
		fitLine(modelEffortValue(model, selection, selected), effortWidth)
	return m.modelTableLine(line, selected, width)
}

func modelColumnWidths(width int) (int, int, int) {
	contextWidth := min(22, max(16, width/4))
	effortWidth := min(18, max(12, width/5))
	modelWidth := max(12, width-6-contextWidth-effortWidth)
	return modelWidth, contextWidth, effortWidth
}

func modelRowName(item menuItem, current bool) string {
	name := singleLine(item.label)
	if current {
		name += " [current]"
	}
	return name
}

func modelContextValue(model engine.Model, selection engine.ModelSelection, selected bool) string {
	label := contextTierLabel(selection.ContextTier)
	for _, option := range model.ContextOptions {
		if option.Tier == selection.ContextTier {
			label = contextOptionLabel(option)
			break
		}
	}
	return adjustableModelValue(label, len(model.ContextOptions), selected)
}

func modelEffortValue(model engine.Model, selection engine.ModelSelection, selected bool) string {
	if len(model.ReasoningEfforts) == 0 {
		return "Default (fixed)"
	}
	return adjustableModelValue(effortLabel(selection.ReasoningEffort), len(model.ReasoningEfforts), selected)
}

func adjustableModelValue(label string, options int, selected bool) string {
	if options <= 1 {
		return label + " fixed"
	}
	if selected {
		return "< " + label + " >"
	}
	return label
}

func (m *Model) modelTableLine(text string, selected bool, width int) string {
	text = fitLine(text, width)
	if selected {
		style := m.color.background(m.color.raised)
		if !m.color.noColor {
			style = style.Foreground(lipgloss.Color(m.color.cyan)).Bold(true)
		}
		return style.Render(text)
	}
	return m.color.paint(m.color.text, text)
}

func (m *Model) dialogBody(body string, width int, kind dialogKind) string {
	body = safeText(body)
	if kind != dialogDiff || m.color.noColor {
		return ansi.Wrap(body, width, "")
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "+"):
			lines[i] = m.color.paint(m.color.lime, line)
		case strings.HasPrefix(line, "-"):
			lines[i] = m.color.paint(m.color.red, line)
		case strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "diff --git "):
			lines[i] = m.color.paint(m.color.cyan, line)
		case strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "new file mode "),
			strings.HasPrefix(line, "deleted file mode "),
			strings.HasPrefix(line, "similarity index "),
			strings.HasPrefix(line, "rename from "),
			strings.HasPrefix(line, "rename to "):
			lines[i] = m.color.paint(m.color.muted, line)
		}
	}
	return ansi.Wrap(strings.Join(lines, "\n"), width, "")
}

func (m *Model) activityGlyph() string {
	if !m.canAnimate() {
		return "o"
	}
	sparks := []string{"· o", "o °", "° ·", "o ·"}
	if m.prefs.ASCII {
		sparks = []string{". o", "o .", ". .", "o o"}
	}
	return sparks[(m.frame/3)%len(sparks)]
}

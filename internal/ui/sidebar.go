package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/VeVarunSharma/sodapop/internal/ui/logo"
)

const (
	sidebarBreakpoint        = 120
	minimumConversationWidth = 82
	sidebarMinWidth          = 28
	sidebarMaxWidth          = 36
	sidebarGap               = 1
)

func (m *Model) sidebarView() string {
	g, c := m.layout, m.color
	if !g.sidebarVisible {
		return ""
	}
	content := m.sidebarContent()
	width := max(1, g.sidebarWidth-4)
	masthead := strings.Join(m.sidebarMasthead(width), "\n")
	innerHeight := max(1, m.height-2)
	mastheadHeight := min(innerHeight-1, lipgloss.Height(masthead)+1)
	scrollHeight := max(1, innerHeight-mastheadHeight)
	if content != m.sidebarCache || width != m.sidebarWidth {
		offset := m.sidebar.YOffset()
		m.sidebar.SetWidth(width)
		m.sidebar.SetContent(content)
		m.sidebar.SetYOffset(offset)
		m.sidebarCache = content
		m.sidebarWidth = width
	}
	m.sidebar.SetHeight(scrollHeight)
	title := "STATUS"
	if m.sidebarFocus {
		title = fmt.Sprintf("SIDEBAR / %d%%", int(m.sidebar.ScrollPercent()*100))
	}
	accent := c.raised
	if m.sidebarFocus {
		accent = c.magenta
	}
	body := fitBlock(masthead, width, mastheadHeight-1) + "\n" +
		fitBlock(m.sidebar.View(), width, scrollHeight)
	return c.frame(title, fitBlock(body, width, innerHeight), g.sidebarWidth, accent)
}

func (m *Model) sidebarContent() string {
	c := m.color
	lines := m.sidebarSection("WORKSPACE", m.workspaceRows())
	lines = append(lines, "")
	modelRows := strings.Split(m.modelSummary(), "\n")
	for i := range modelRows {
		modelRows[i] = c.paint(c.cyan, modelRows[i])
	}
	lines = append(lines, m.sidebarSection("MODEL", modelRows)...)
	lines = append(lines, "")
	lines = append(lines, m.sidebarSection("MCP SERVERS", m.capabilityRows(m.mcpServers))...)
	lines = append(lines, "")
	lines = append(lines, m.sidebarSection("SKILLS", m.capabilityRows(m.skills))...)
	lines = append(lines, "", c.paint(c.muted, "F3 focus  "+c.glyph("·", "/")+"  wheel scroll"))
	return strings.Join(lines, "\n")
}

func (m *Model) sidebarMasthead(width int) []string {
	rendered := logo.Render(logo.Options{
		Width: width, Version: m.sidebarVersion(),
		From: m.color.red, To: m.color.magenta, Muted: m.color.muted,
		NoColor: m.prefs.NoColor, ASCII: m.prefs.ASCII, Compact: m.height < 20,
	})
	return strings.Split(rendered, "\n")
}

func (m *Model) sidebarSection(title string, rows []string) []string {
	return append([]string{m.color.paint(m.color.magenta, title)}, rows...)
}

func (m *Model) workspaceRows() []string {
	c := m.color
	project := filepath.Clean(m.opts.Project)
	if project == "." || project == "" {
		project = "project"
	}
	rows := []string{
		c.paint(c.text, m.projectName()),
		c.paint(c.muted, safeText(project)),
	}
	if m.status.IsRepository {
		branch := singleLine(m.status.Branch)
		if branch == "" {
			branch = "unborn"
		}
		detail := branch
		if changed := len(m.status.Entries); changed > 0 {
			detail += fmt.Sprintf("  %s  %d changed", c.glyph("·", "."), changed)
		}
		rows = append(rows, c.paint(c.lime, detail))
	}
	return rows
}

func (m *Model) capabilityRows(capabilities []Capability) []string {
	if len(capabilities) == 0 {
		return []string{m.color.paint(m.color.muted, "None")}
	}
	rows := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		color, state := m.color.red, "inactive"
		if capability.Active {
			color, state = m.color.lime, "active"
		}
		dot := m.color.glyph("●", "o")
		row := m.color.paint(color, dot) + " " + m.color.paint(m.color.text, singleLine(capability.Name))
		row += " " + m.color.paint(m.color.muted, state)
		rows = append(rows, row)
	}
	return rows
}

func (m *Model) sidebarVersion() string {
	version := singleLine(m.opts.Version)
	if version == "" {
		return "dev"
	}
	if version == "dev" || strings.HasPrefix(strings.ToLower(version), "v") {
		return version
	}
	return "v" + version
}

func (m *Model) focusSidebar(focus bool) {
	if !m.layout.sidebarVisible {
		m.sidebarFocus = false
		m.report("The sidebar appears automatically when the terminal is wide enough; chat keeps priority.", false)
		return
	}
	m.sidebarFocus = focus
	if focus {
		m.toolFocus = false
	}
}

func (m *Model) scrollSidebar(lines int) {
	if lines < 0 {
		m.sidebar.ScrollUp(-lines)
	} else {
		m.sidebar.ScrollDown(lines)
	}
}

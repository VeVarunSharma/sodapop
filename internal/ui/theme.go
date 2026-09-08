package ui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	glamansi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/VeVarunSharma/sodapop/internal/config"
)

type palette struct {
	ink, panel, raised, border, text, muted, cyan, magenta, lime, amber, red string
	noColor, ascii                                                           bool
}

func colors(p config.Preferences) palette {
	c := palette{
		ink: "#09090B", panel: "#111116", raised: "#1A1A22", border: "#30303D",
		text: "#F4F4F5", muted: "#9292A3", cyan: "#67E8F9",
		magenta: "#C084FC", lime: "#A3E635", amber: "#FBBF24", red: "#FB7185",
		noColor: p.NoColor, ascii: p.ASCII,
	}
	switch p.Theme {
	case "graphite":
		c.ink, c.panel, c.raised, c.border = "#0B0D10", "#111419", "#1A1E25", "#2A303A"
		c.text, c.muted = "#E8EAED", "#8B949E"
		c.cyan, c.magenta, c.lime = "#79C0FF", "#BC8CFF", "#7EE787"
		c.amber, c.red = "#E3B341", "#FF7B72"
	case "midnight":
		c.ink, c.panel, c.raised, c.border = "#070C14", "#0D1420", "#151F2E", "#263449"
		c.text, c.muted = "#E6EDF7", "#8290A6"
		c.cyan, c.magenta, c.lime = "#7DD3FC", "#A5B4FC", "#6EE7B7"
		c.amber, c.red = "#FCD34D", "#FDA4AF"
	case "high-contrast":
		c.ink, c.panel, c.raised, c.border = "#000000", "#080808", "#1F1F1F", "#737373"
		c.text, c.muted = "#FFFFFF", "#D4D4D4"
		c.cyan, c.magenta, c.lime = "#22D3EE", "#E879F9", "#BEF264"
		c.amber, c.red = "#FDE047", "#FDA4AF"
	case "light":
		c.ink, c.panel, c.raised, c.border = "#F7F7FA", "#FFFFFF", "#ECECF2", "#D6D6E0"
		c.text, c.muted = "#18181B", "#6B7280"
		c.cyan, c.magenta, c.lime = "#087EA4", "#7C3AED", "#3F6212"
		c.amber, c.red = "#92400E", "#BE123C"
	}
	return c
}

func (c palette) style(fg string) lipgloss.Style {
	s := lipgloss.NewStyle()
	if !c.noColor {
		s = s.Foreground(lipgloss.Color(fg))
	}
	return s
}

func (c palette) paint(fg, text string) string { return c.style(fg).Render(text) }

func (c palette) background(bg string) lipgloss.Style {
	s := lipgloss.NewStyle()
	if !c.noColor {
		s = s.Background(lipgloss.Color(bg)).Foreground(lipgloss.Color(c.text))
	}
	return s
}

func (c palette) badge(text, fg string) string {
	s := c.style(fg)
	if !c.noColor {
		s = s.Background(lipgloss.Color(c.raised)).Bold(true)
	}
	return s.Render(" " + text + " ")
}

func (c palette) glyph(unicode, ascii string) string {
	if c.ascii {
		return ascii
	}
	return unicode
}

func (c palette) frame(title, content string, width int, accent string) string {
	if width < 6 {
		return fitBlock(content, width, len(strings.Split(content, "\n")))
	}
	tl, tr, bl, br, h, v := "╭", "╮", "╰", "╯", "─", "│"
	if c.ascii {
		tl, tr, bl, br, h, v = "+", "+", "+", "+", "-", "|"
	}
	label := clip(" "+singleLine(title)+" ", width-4)
	top := c.paint(c.border, tl+h) +
		c.paint(accent, label) +
		c.paint(c.border, strings.Repeat(h, max(0, width-3-lipgloss.Width(label)))+tr)
	bottom := c.paint(c.border, bl+strings.Repeat(h, width-2)+br)
	lines := []string{top}
	for _, line := range strings.Split(content, "\n") {
		lines = append(lines, c.paint(c.border, v)+c.background(c.panel).Render(" "+fitLine(line, width-4)+" ")+c.paint(c.border, v))
	}
	lines = append(lines, bottom)
	return c.background(c.panel).Render(strings.Join(lines, "\n"))
}

func (c palette) textareaStyles(reducedMotion bool, accent string) textarea.Styles {
	if accent == "" {
		accent = c.cyan
	}
	normal := textarea.StyleState{
		Base: c.background(c.panel), Text: c.style(c.text),
		CursorLine: c.style(c.text), Placeholder: c.style(c.muted),
		Prompt: c.style(accent), EndOfBuffer: c.style(c.muted),
		Selection: c.background(c.raised),
	}
	var cursorColor color.Color
	if !c.noColor {
		cursorColor = lipgloss.Color(accent)
	}
	return textarea.Styles{
		Focused: normal, Blurred: normal,
		Cursor: textarea.CursorStyle{Color: cursorColor, Shape: tea.CursorBar, Blink: !reducedMotion},
	}
}

func newMarkdown(width int, c palette) (*glamour.TermRenderer, error) {
	s := styles.ASCIIStyleConfig
	zero := uint(0)
	s.Document.Margin = &zero
	s.CodeBlock.Margin = &zero
	s.Item.BlockPrefix = "- "
	s.ImageText.Format = "Image: {{.text}}"
	s.Link.Format = "({{.text}})"
	s.LinkText.Format = "{{.text}}"
	s.CodeBlock.StylePrimitive = glamansi.StylePrimitive{}
	if !c.noColor {
		s.Document.Color = &c.text
		s.Heading.Color = &c.cyan
		s.Code.Color = &c.lime
		s.Link.Color = &c.cyan
		s.Strong.Color = &c.magenta
		s.BlockQuote.Color = &c.muted
		s.CodeBlock.Color = &c.text
		s.CodeBlock.BackgroundColor = &c.ink
		s.CodeBlock.Theme = "dracula"
		if c.ink == "#F7F7FA" {
			s.CodeBlock.Theme = "github"
		}
	}
	return glamour.NewTermRenderer(
		glamour.WithStyles(s),
		glamour.WithWordWrap(max(10, width)),
		glamour.WithTableWrap(true),
	)
}

// Package logo renders Sodapop's responsive terminal wordmark.
package logo

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type Options struct {
	Width   int
	Version string
	From    string
	To      string
	Muted   string
	NoColor bool
	ASCII   bool
	Compact bool
}

// Render returns a static Crush-style field surrounding Sodapop's custom wordmark.
func Render(opts Options) string {
	width := max(1, opts.Width)
	word := wordmark(opts.ASCII)
	wordWidth := lipgloss.Width(word)
	if opts.Compact || width < wordWidth+2 {
		return renderCompact(opts, width)
	}

	meta := renderVersion(opts, wordWidth)
	diagonal := "╱"
	if opts.ASCII {
		diagonal = "/"
	}
	field := center(gradient(strings.Repeat(diagonal, wordWidth), opts, false), width)
	lines := []string{field, field, center(meta, width)}
	for _, line := range strings.Split(word, "\n") {
		lines = append(lines, gradient(surround(line, wordWidth, width, diagonal), opts, false))
	}
	lines = append(lines, field)
	return strings.Join(lines, "\n")
}

func surround(text string, textWidth, width int, fill string) string {
	text = ansi.Truncate(text, min(textWidth, width), "")
	text += strings.Repeat(" ", max(0, min(textWidth, width)-ansi.StringWidth(text)))
	remaining := max(0, width-ansi.StringWidth(text))
	if remaining < 4 {
		return center(text, width)
	}
	fieldWidth := remaining - 2
	leftWidth := fieldWidth / 2
	rightWidth := fieldWidth - leftWidth
	return strings.Repeat(fill, leftWidth) + " " + text + " " + strings.Repeat(fill, rightWidth)
}

func renderCompact(opts Options, width int) string {
	version := renderVersion(opts, width)
	name := center(gradient("SODAPOP", opts, true), width)
	return version + "\n" + name
}

func renderVersion(opts Options, width int) string {
	tail := "…"
	if opts.ASCII {
		tail = strings.Repeat(".", min(3, width))
	}
	version := ansi.Truncate(opts.Version, width, tail)
	return right(paint(opts.Muted, version, opts.NoColor), width)
}

func gradient(text string, opts Options, bold bool) string {
	if opts.NoColor || opts.From == "" || opts.To == "" {
		return text
	}
	runes := []rune(text)
	ramp := lipgloss.Blend1D(max(1, len(runes)), lipgloss.Color(opts.From), lipgloss.Color(opts.To))
	var result strings.Builder
	for i, r := range runes {
		if r == ' ' {
			result.WriteRune(r)
			continue
		}
		var foreground color.Color = ramp[min(i, len(ramp)-1)]
		style := lipgloss.NewStyle().Foreground(foreground)
		if bold {
			style = style.Bold(true)
		}
		result.WriteString(style.Render(string(r)))
	}
	return result.String()
}

func paint(foreground, text string, noColor bool) string {
	if noColor || foreground == "" {
		return text
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(foreground)).Render(text)
}

func center(text string, width int) string {
	text = ansi.Truncate(text, max(0, width), "")
	padding := max(0, (width-ansi.StringWidth(text))/2)
	return strings.Repeat(" ", padding) + text
}

func right(text string, width int) string {
	text = ansi.Truncate(text, max(0, width), "")
	return strings.Repeat(" ", max(0, width-ansi.StringWidth(text))) + text
}

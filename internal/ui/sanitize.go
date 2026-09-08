package ui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Model, tool, Git, and identity text never gets to write terminal commands.
// Strip before markdown parsing as well as at the renderer boundary. Keeping
// the raw accumulated message until this point also handles split escapes.
func safeText(text string) string {
	text = ansi.Strip(strings.ToValidUTF8(text, "\uFFFD"))
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("    ")
		case unicode.IsControl(r):
		case isDirectionalControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func singleLine(text string) string {
	return strings.Join(strings.Fields(safeText(text)), " ")
}

func isDirectionalControl(r rune) bool {
	return r == '\u061c' || r == '\u200e' || r == '\u200f' ||
		r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069'
}

// Consent details preserve evidence of hidden characters rather than silently
// removing them from the command or path the user is deciding about.
func visibleDetail(text string, multiline bool) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(text, "\uFFFD") {
		switch {
		case r == '\n' && multiline:
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case unicode.IsControl(r) || isDirectionalControl(r):
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Glamour is allowed to generate SGR styling, but not OSC hyperlinks, clipboard
// writes, cursor movement, or any other terminal-control protocol.
func safeMarkdown(text string, noColor bool) string {
	if noColor {
		return safeText(text)
	}
	var b strings.Builder
	for len(text) > 0 {
		i := strings.IndexByte(text, '\x1b')
		if i < 0 {
			b.WriteString(safeText(text))
			break
		}
		b.WriteString(safeText(text[:i]))
		text = text[i:]
		if len(text) >= 3 && text[1] == '[' {
			j := 2
			for j < len(text) && (text[j] >= '0' && text[j] <= '9' || text[j] == ';' || text[j] == ':') {
				j++
			}
			if j < len(text) && text[j] == 'm' {
				b.WriteString(text[:j+1])
				text = text[j+1:]
				continue
			}
		}
		// Strip a whole non-SGR sequence rather than displaying its payload.
		_, width, n, _ := ansi.DecodeSequence(text, 0, nil)
		_ = width
		if n <= 0 {
			_, n = utf8.DecodeRuneInString(text)
		}
		text = text[n:]
	}
	return b.String()
}

func clip(text string, width int) string {
	return ansi.Truncate(text, max(0, width), "")
}

func fitLine(text string, width int) string {
	text = clip(text, width)
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func fitBlock(text string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	result := make([]string, height)
	for i := range result {
		if i < len(lines) {
			result[i] = fitLine(lines[i], width)
		} else {
			result[i] = strings.Repeat(" ", width)
		}
	}
	return strings.Join(result, "\n")
}

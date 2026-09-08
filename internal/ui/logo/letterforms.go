package logo

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type letterform func(bool) string

func renderWord(spacing int, letters ...letterform) string {
	rendered := make([]string, len(letters))
	for i, letter := range letters {
		rendered[i] = letter(false)
	}
	if spacing > 0 {
		rendered = intersperse(rendered, strings.Repeat(" ", spacing))
	}
	word := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
	lines := strings.Split(word, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func intersperse(values []string, separator string) []string {
	if len(values) < 2 {
		return values
	}
	result := make([]string, 0, len(values)*2-1)
	for i, value := range values {
		if i > 0 {
			result = append(result, separator)
		}
		result = append(result, value)
	}
	return result
}

func letterS(bool) string {
	return strings.Join([]string{
		"█▀▀",
		"▀▀█",
		"▄▄█",
	}, "\n")
}

func letterO(bool) string {
	return strings.Join([]string{
		"█▀█",
		"█ █",
		"█▄█",
	}, "\n")
}

func letterD(bool) string {
	return strings.Join([]string{
		"█▀▄",
		"█ █",
		"█▄▀",
	}, "\n")
}

func letterA(bool) string {
	return strings.Join([]string{
		"▄▀▄",
		"█▄█",
		"█ █",
	}, "\n")
}

func letterP(bool) string {
	return strings.Join([]string{
		"█▀█",
		"█▄█",
		"█  ",
	}, "\n")
}

func asciiS(bool) string {
	return strings.Join([]string{
		" __",
		"(_ ",
		"__)",
	}, "\n")
}

func asciiO(bool) string {
	return strings.Join([]string{
		" _ ",
		"/ \\",
		"\\_/",
	}, "\n")
}

func asciiD(bool) string {
	return strings.Join([]string{
		"__ ",
		"| \\",
		"|_/",
	}, "\n")
}

func asciiA(bool) string {
	return strings.Join([]string{
		" _ ",
		"/_\\",
		"| |",
	}, "\n")
}

func asciiP(bool) string {
	return strings.Join([]string{
		"__ ",
		"|_)",
		"|  ",
	}, "\n")
}

func wordmark(ascii bool) string {
	if ascii {
		return renderWord(1, asciiS, asciiO, asciiD, asciiA, asciiP, asciiO, asciiP)
	}
	return renderWord(1, letterS, letterO, letterD, letterA, letterP, letterO, letterP)
}

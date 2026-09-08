package logo

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestBannerUsesStaticFieldsThreeRowWordmarkAndRightAlignedVersion(t *testing.T) {
	const width = 32
	rendered := Render(Options{
		Width: width, Version: "v1.2.3",
		From: "#FB7185", To: "#C084FC", Muted: "#9292A3",
	})
	lines := strings.Split(rendered, "\n")
	if len(lines) != 7 {
		t.Fatalf("banner has %d rows; want 7", len(lines))
	}
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = ansi.Strip(line)
		if lipgloss.Width(line) > width {
			t.Fatalf("line %d exceeds width: %q", i, plain[i])
		}
	}
	if !strings.Contains(plain[0], "╱╱╱") || plain[0] != plain[1] || plain[6] != plain[0] {
		t.Fatalf("static diagonal fields are missing:\n%s", strings.Join(plain, "\n"))
	}
	assertBannerWordmark(t, rendered, width, false)
	versionEnd := ansi.StringWidth(plain[2])
	wordmarkEnd := (width-27)/2 + 27
	if strings.TrimSpace(plain[2]) != "v1.2.3" || versionEnd != wordmarkEnd {
		t.Fatalf("version is not right-aligned above title:\n%s", strings.Join(plain[2:6], "\n"))
	}
	for row, line := range plain[3:6] {
		if !strings.HasPrefix(line, "╱") || !strings.HasSuffix(line, "╱") {
			t.Fatalf("title row %d is not surrounded by diagonal fields: %q", row, line)
		}
	}
	if !strings.ContainsRune(rendered, '\x1b') {
		t.Fatal("color wordmark did not render a gradient")
	}
	if strings.Count(lines[0], "\x1b[") < 4 || strings.Count(lines[3], "\x1b[") < 4 || strings.Count(lines[6], "\x1b[") < 4 {
		t.Fatal("diagonal fields did not render multiple gradient colors")
	}
}

func TestUnicodeAndASCIILetterformsAreLegible(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		t.Run(fmt.Sprintf("ascii=%t", ascii), func(t *testing.T) {
			assertSodapopWordmark(t, wordmark(ascii), ascii)
		})
	}
}

func TestFullBannerSurroundsWordmarkWithinRequestedWidth(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		for _, width := range []int{31, 32, 40, 80} {
			t.Run(fmt.Sprintf("ascii=%t/width=%d", ascii, width), func(t *testing.T) {
				rendered := Render(Options{
					Width: width, Version: "dev", NoColor: true, ASCII: ascii,
				})
				assertBannerWordmark(t, rendered, width, ascii)
				lines := strings.Split(rendered, "\n")
				for row, line := range lines {
					if ansi.StringWidth(line) > width {
						t.Fatalf("row %d exceeded width %d: %q", row, width, line)
					}
				}
				diagonal := "╱"
				if ascii {
					diagonal = "/"
				}
				for row, line := range lines[3:6] {
					if ansi.StringWidth(line) != width {
						t.Fatalf("title row %d used %d cells; want %d: %q", row, ansi.StringWidth(line), width, line)
					}
					if !strings.HasPrefix(line, diagonal) || !strings.HasSuffix(line, diagonal) {
						t.Fatalf("title row %d lacks balanced fields: %q", row, line)
					}
				}
			})
		}
	}
}

func TestASCIIAndNoColorBannerIsPlain(t *testing.T) {
	const width = 32
	rendered := Render(Options{
		Width: width, Version: "dev", From: "#FB7185", To: "#C084FC",
		Muted: "#9292A3", ASCII: true, NoColor: true,
	})
	if strings.ContainsRune(rendered, '\x1b') {
		t.Fatal("no-color banner emitted ANSI styling")
	}
	for _, character := range rendered {
		if character > unicode.MaxASCII {
			t.Fatalf("ASCII banner emitted %q", character)
		}
	}
	assertBannerWordmark(t, rendered, width, true)
	if !strings.Contains(rendered, strings.Repeat("/", 27)) {
		t.Fatalf("ASCII diagonal field is missing:\n%s", rendered)
	}
	for row, line := range strings.Split(rendered, "\n")[3:6] {
		if !strings.HasPrefix(line, "/") || !strings.HasSuffix(line, "/") {
			t.Fatalf("ASCII title row %d is not surrounded by fields: %q", row, line)
		}
	}
}

func TestCompactFallbackFitsNarrowWidthAndTruncatesVersion(t *testing.T) {
	rendered := Render(Options{
		Width: 12, Version: "v123456789012345", Compact: true, NoColor: true,
	})
	lines := strings.Split(rendered, "\n")
	if lipgloss.Width(rendered) > 12 || len(lines) != 2 {
		t.Fatalf("compact lockup exceeded bounds:\n%s", rendered)
	}
	if strings.TrimSpace(lines[1]) != "SODAPOP" || ansi.StringWidth(lines[0]) > 12 {
		t.Fatalf("compact lockup lost title or version:\n%s", rendered)
	}
	if lines[0] != "v1234567890…" || lines[1] != "  SODAPOP" {
		t.Fatalf("compact lockup did not align the longer brand and truncated version:\n%s", rendered)
	}
}

func TestResponsiveBannerPreservesBrandAndAccessibleVersion(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		for _, noColor := range []bool{false, true} {
			for _, compact := range []bool{false, true} {
				for _, requested := range []int{-1, 0, 1, 2, 3, 6, 7, 8, 12, 27, 28, 29, 30, 31, 32} {
					name := fmt.Sprintf("ascii=%t/mono=%t/compact=%t/width=%d", ascii, noColor, compact, requested)
					t.Run(name, func(t *testing.T) {
						const version = "v123456789012345678901234567890123456789"
						opts := Options{
							Width: requested, Version: version, Compact: compact, ASCII: ascii, NoColor: noColor,
							From: "#FB7185", To: "#C084FC", Muted: "#9292A3",
						}
						rendered := Render(opts)
						plain := ansi.Strip(rendered)
						if noColor && rendered != plain {
							t.Fatal("no-color banner emitted styling")
						}
						if !noColor && rendered == plain {
							t.Fatal("color banner lost its styling")
						}
						if ascii {
							for _, r := range plain {
								if r > unicode.MaxASCII {
									t.Fatalf("ASCII banner emitted %q:\n%s", r, plain)
								}
							}
						}
						width := max(1, requested)
						lines := strings.Split(plain, "\n")
						for row, line := range lines {
							if ansi.StringWidth(line) > width {
								t.Fatalf("row %d exceeded width %d: %q", row, width, line)
							}
						}
						metaRow, metaWidth, metaEnd := 0, width, width
						if compact || width < 29 {
							if len(lines) != 2 {
								t.Fatalf("compact banner has %d rows; want 2:\n%s", len(lines), plain)
							}
							want := "SODAPOP"[:min(width, 7)]
							if strings.TrimSpace(lines[1]) != want {
								t.Fatalf("compact title = %q; want %q", lines[1], want)
							}
							if padding := len(lines[1]) - len(strings.TrimLeft(lines[1], " ")); padding != (width-len(want))/2 {
								t.Fatalf("compact title is not centered: %q", lines[1])
							}
						} else {
							assertBannerWordmark(t, rendered, width, ascii)
							metaRow, metaWidth, metaEnd = 2, 27, (width-27)/2+27
						}
						tail := "…"
						if ascii {
							tail = strings.Repeat(".", min(3, metaWidth))
						}
						wantVersion := version[:metaWidth-ansi.StringWidth(tail)] + tail
						if strings.TrimSpace(lines[metaRow]) != wantVersion || ansi.StringWidth(lines[metaRow]) != metaEnd {
							t.Fatalf("version is not bounded and right-aligned: %q; want %q ending at %d", lines[metaRow], wantVersion, metaEnd)
						}
					})
				}
			}
		}
	}
}

func assertBannerWordmark(t *testing.T, rendered string, width int, ascii bool) {
	t.Helper()
	lines := strings.Split(ansi.Strip(rendered), "\n")
	if len(lines) != 7 {
		t.Fatalf("full banner has %d rows; want 7:\n%s", len(lines), rendered)
	}
	left := (width - 27) / 2
	title := make([]string, 3)
	for row, line := range lines[3:6] {
		cells := []rune(line)
		if len(cells) < left+27 {
			t.Fatalf("wordmark row %d is clipped: %q", row, line)
		}
		title[row] = string(cells[left : left+27])
	}
	assertSodapopWordmark(t, strings.Join(title, "\n"), ascii)
}

func assertSodapopWordmark(t *testing.T, rendered string, ascii bool) {
	t.Helper()
	// Decode each authored glyph independently so a correctly renamed caption
	// cannot conceal an old, reordered, clipped, or repeated wordmark.
	alphabet := map[string]rune{
		"█▀▀\n▀▀█\n▄▄█": 'S',
		"█▀█\n█ █\n█▄█": 'O',
		"█▀▄\n█ █\n█▄▀": 'D',
		"▄▀▄\n█▄█\n█ █": 'A',
		"█▀█\n█▄█\n█  ": 'P',
	}
	if ascii {
		alphabet = map[string]rune{
			" __\n(_ \n__)":   'S',
			" _ \n/ \\\n\\_/": 'O',
			"__ \n| \\\n|_/":  'D',
			" _ \n/_\\\n| |":  'A',
			"__ \n|_)\n|  ":   'P',
		}
	}
	if lipgloss.Height(rendered) != 3 || lipgloss.Width(rendered) != 27 {
		t.Fatalf("wordmark must occupy three rows and 27 cells:\n%s", rendered)
	}
	rows := make([][]rune, 3)
	for row, line := range strings.Split(rendered, "\n") {
		rows[row] = []rune(line + strings.Repeat(" ", 27-ansi.StringWidth(line)))
	}
	var decoded strings.Builder
	for column := 0; column < 27; column += 4 {
		glyph := make([]string, 3)
		for row, cells := range rows {
			glyph[row] = string(cells[column : column+3])
			if column+3 < 27 && cells[column+3] != ' ' {
				t.Fatalf("row %d lost spacing after glyph %d:\n%s", row, column/4, rendered)
			}
		}
		letter, ok := alphabet[strings.Join(glyph, "\n")]
		if !ok {
			t.Fatalf("unrecognizable glyph %d:\n%s", column/4, strings.Join(glyph, "\n"))
		}
		decoded.WriteRune(letter)
	}
	if decoded.String() != "SODAPOP" {
		t.Fatalf("wordmark spells %q; want SODAPOP:\n%s", decoded.String(), rendered)
	}
}

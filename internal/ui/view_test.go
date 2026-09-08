package ui

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/VeVarunSharma/sodapop/internal/config"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
	"github.com/charmbracelet/x/ansi"
)

func TestLayoutsAndNoColorKeepComposerAndBoundedView(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {44, 12}, {20, 6}, {8, 3}, {1, 1}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m, _ := readyModel(t)
			m.composer.SetValue("visible draft")
			m.resize(size[0], size[1])
			view := m.View()
			assertBounds(t, view, size[0], size[1])
			if strings.ContainsRune(view.Content, '\x1b') || view.ForegroundColor != nil || view.BackgroundColor != nil {
				t.Fatal("no-color view emitted styling escapes or terminal colors")
			}
			if view.Cursor != nil && view.Cursor.Color != nil {
				t.Fatal("no-color cursor retained a color")
			}
			if size[0] >= 44 && !strings.Contains(view.Content, "visible draft") {
				t.Fatal("composer disappeared in responsive layout")
			}
			for _, r := range view.Content {
				if r > unicode.MaxASCII {
					t.Fatalf("ASCII layout emitted non-ASCII chrome: %q", r)
				}
			}
			if !view.AltScreen {
				t.Fatal("v2 View did not request alternate screen")
			}
			if view.MouseMode != tea.MouseModeCellMotion {
				t.Fatal("v2 View did not enable mouse wheel reporting")
			}
		})
	}
}

func TestMouseWheelScrollsHistoryAndRestoresLiveFollow(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "scroll-session"}
	m.turn = true
	m.resize(80, 16)
	for i := 0; i < 30; i++ {
		event(m, engine.Event{
			Kind: engine.EventMessage, ID: fmt.Sprintf("event-%d", i),
			MessageID: fmt.Sprintf("message-%d", i), Text: fmt.Sprintf("History line %d", i),
		})
	}
	m.flushTimeline()
	if !m.timeline.AtBottom() || !m.follow {
		t.Fatal("timeline did not begin in live-follow mode")
	}
	bottomOffset := m.timeline.YOffset()

	wheelY := m.layout.header
	m.Update(tea.MouseWheelMsg(tea.Mouse{X: 1, Y: wheelY, Button: tea.MouseWheelUp}))
	scrolledOffset := m.timeline.YOffset()
	if scrolledOffset >= bottomOffset || m.follow {
		t.Fatalf("wheel-up did not enter scrollback: offset=%d bottom=%d follow=%t lines=%d", scrolledOffset, bottomOffset, m.follow, strings.Count(m.timeline.GetContent(), "\n")+1)
	}

	event(m, engine.Event{
		Kind: engine.EventMessage, ID: "new-event",
		MessageID: "new-message", Text: "New output while reviewing history",
	})
	m.flushTimeline()
	if m.timeline.YOffset() != scrolledOffset {
		t.Fatal("new output pulled a scrolled-back timeline to the bottom")
	}

	for !m.timeline.AtBottom() {
		m.Update(tea.MouseWheelMsg(tea.Mouse{X: 1, Y: wheelY, Button: tea.MouseWheelDown}))
	}
	if !m.follow {
		t.Fatal("wheel-down at the bottom did not restore live-follow")
	}
}

func TestMouseWheelScrollsDialogInsteadOfTimeline(t *testing.T) {
	m, _ := readyModel(t)
	m.resize(80, 16)
	m.timeline.SetContent(strings.Repeat("timeline\n", 50))
	m.timeline.GotoBottom()
	timelineOffset := m.timeline.YOffset()
	d := m.newDialog(dialogHelp, "LONG DETAILS", strings.Repeat("detail line\n", 50))
	m.View()

	m.Update(tea.MouseWheelMsg(tea.Mouse{X: 10, Y: 5, Button: tea.MouseWheelDown}))
	if d.scroll == 0 {
		t.Fatal("wheel did not scroll open dialog details")
	}
	if m.timeline.YOffset() != timelineOffset {
		t.Fatal("dialog wheel event scrolled the conversation behind it")
	}
}

func TestSidebarIsWideOnlyAndChatKeepsPriority(t *testing.T) {
	m, _ := readyModel(t)
	m.status = workspace.Status{IsRepository: true, Branch: "main"}
	if summary := m.modelSummary(); !strings.Contains(summary, "GPT-5.6 Luna") ||
		!strings.Contains(summary, "Default") || !strings.Contains(summary, "Medium") {
		t.Fatalf("model summary omitted selection details: %q", summary)
	}

	m.resize(sidebarBreakpoint-1, 30)
	if m.layout.sidebarVisible || m.layout.mainWidth != sidebarBreakpoint-1 {
		t.Fatal("sidebar consumed space below its responsive breakpoint")
	}
	if strings.Contains(m.View().Content, "MCP SERVERS") {
		t.Fatal("hidden sidebar was still rendered")
	}

	m.resize(sidebarBreakpoint, 30)
	if !m.layout.sidebarVisible || m.layout.mainWidth < minimumConversationWidth {
		t.Fatalf("wide layout did not preserve chat width: %+v", m.layout)
	}
	view := m.View().Content
	for _, expected := range []string{"SODAPOP", "vtest", "/project", "main", "GPT-5.6 Luna", "Default", "MCP SERVERS", "SKILLS", "None"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("sidebar omitted %q:\n%s", expected, view)
		}
	}

	m.resize(90, 30)
	if m.layout.sidebarVisible || m.layout.mainWidth != 90 {
		t.Fatal("sidebar did not yield the full terminal width back to chat")
	}
}

func TestSidebarCapabilitiesAndIndependentScrolling(t *testing.T) {
	options := testOptions()
	options.MCPServers = []Capability{{Name: "filesystem", Active: true}, {Name: "github", Active: false}}
	for i := 0; i < 35; i++ {
		options.Skills = append(options.Skills, Capability{Name: fmt.Sprintf("skill-%02d", i), Active: i%2 == 0})
	}
	m := testModel(t, options)
	m.models = []engine.Model{{ID: "model-a", Name: "Model A"}}
	m.model = "model-a"
	m.resize(140, 18)
	view := m.View().Content
	for _, expected := range []string{"filesystem", "github", "active", "inactive"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("sidebar capability list omitted %q", expected)
		}
	}

	m.timeline.SetContent(strings.Repeat("conversation\n", 50))
	m.timeline.GotoBottom()
	timelineOffset := m.timeline.YOffset()
	m.Update(tea.MouseWheelMsg(tea.Mouse{X: m.layout.sidebarX, Y: 3, Button: tea.MouseWheelDown}))
	if m.sidebar.YOffset() == 0 {
		t.Fatal("wheel over sidebar did not scroll its viewport")
	}
	if m.timeline.YOffset() != timelineOffset {
		t.Fatal("sidebar wheel event moved conversation history")
	}

	m.sidebar.GotoTop()
	m.handleKey(keyPress("f3"))
	m.handleKey(keyPress("pgdown"))
	if !m.sidebarFocus || m.sidebar.YOffset() == 0 {
		t.Fatal("F3 focus did not route keyboard scrolling to sidebar")
	}
	savedOffset := m.sidebar.YOffset()
	m.resize(90, 18)
	if m.sidebarFocus || m.layout.sidebarVisible {
		t.Fatal("sidebar remained focused after responsive hiding")
	}
	m.resize(140, 18)
	m.View()
	if m.sidebar.YOffset() != savedOffset {
		t.Fatal("sidebar lost its scroll position while responsively hidden")
	}
	m.handleKey(keyPress("f3"))
	m.handleKey(keyPress("esc"))
	if m.sidebarFocus {
		t.Fatal("Esc did not return sidebar focus to the composer")
	}
}

func TestSidebarMastheadCentersVersionTitleAndGradient(t *testing.T) {
	options := testOptions()
	options.Version = "1.2.3"
	options.Preferences.NoColor = false
	options.Preferences.ASCII = false
	m := testModel(t, options)

	for _, width := range []int{sidebarMinWidth - 4, 26, 28, 29, 30, 31, sidebarMaxWidth - 4} {
		lines := m.sidebarMasthead(width)
		rows, metaRow, wordWidth := 7, 2, 27
		if width < 29 {
			rows, metaRow, wordWidth = 2, 0, 7
		}
		if len(lines) != rows {
			t.Fatalf("masthead height at width %d = %d; want %d", width, len(lines), rows)
		}
		version := ansi.Strip(lines[metaRow])
		versionEnd := (width-wordWidth)/2 + wordWidth
		if rows == 2 {
			versionEnd = width
			if strings.TrimSpace(ansi.Strip(lines[1])) != "SODAPOP" {
				t.Fatalf("narrow masthead lost its compact brand at width %d: %q", width, lines)
			}
		} else if title := ansi.Strip(strings.Join(lines[3:6], "\n")); !strings.Contains(title, "█▀▀ █▀█ █▀▄ ▄▀▄ █▀█ █▀█ █▀█") {
			t.Fatalf("full masthead lost the seven-letter wordmark at width %d:\n%s", width, title)
		}
		if strings.TrimSpace(version) != "v1.2.3" || ansi.StringWidth(version) != versionEnd {
			t.Fatalf("version was not right-aligned directly above the title: %q", version)
		}
		for row, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Fatalf("masthead row %d exceeded width %d: %q", row, width, ansi.Strip(line))
			}
			if row == metaRow {
				continue
			}
			plain := ansi.Strip(line)
			content := strings.TrimSpace(plain)
			if rows == 7 && row >= 3 && row <= 5 {
				// The final P leaves blank cells inside the centered glyph grid.
				content = strings.TrimLeft(plain, " ")
			}
			left := len(plain) - len(strings.TrimLeft(plain, " "))
			right := width - left - ansi.StringWidth(content)
			if right < 0 || left-right < -1 || left-right > 1 {
				t.Fatalf("masthead row %d is not centered at width %d: %q", row, width, plain)
			}
		}
		if !strings.ContainsRune(strings.Join(lines, ""), '\x1b') {
			t.Fatal("color-enabled masthead did not render its gradient")
		}
	}
}

func TestSidebarMastheadIsStaticAccessibleAndScrollStable(t *testing.T) {
	options := testOptions()
	options.Preferences.ReducedMotion = false
	options.Preferences.NoColor = true
	options.Preferences.ASCII = false
	for i := 0; i < 35; i++ {
		options.Skills = append(options.Skills, Capability{Name: fmt.Sprintf("skill-%02d", i), Active: true})
	}
	m := testModel(t, options)
	m.resize(140, 24)

	m.turn = true
	m.frame = 0
	first := strings.Join(m.sidebarMasthead(30), "\n")
	m.frame = 1
	second := strings.Join(m.sidebarMasthead(30), "\n")
	if first != second {
		t.Fatal("sidebar banner changed with active animation frames")
	}

	m.turn = false
	m.moment = transientMoment{kind: reactionSuccess, id: 1}
	m.frame = 3
	if first != strings.Join(m.sidebarMasthead(30), "\n") {
		t.Fatal("sidebar banner changed with reaction state")
	}

	m.prefs.ReducedMotion = true
	m.turn = true
	reduced := strings.Join(m.sidebarMasthead(30), "\n")
	m.frame = 2
	if next := strings.Join(m.sidebarMasthead(30), "\n"); reduced != next {
		t.Fatalf("reduced-motion masthead continued animating:\n%q\n%q", reduced, next)
	}
	if strings.ContainsRune(reduced, '\x1b') {
		t.Fatal("no-color masthead emitted ANSI styling")
	}

	m.prefs.ReducedMotion = false
	m.turn = true
	m.frame = 0
	firstView := m.sidebarView()
	m.sidebar.GotoBottom()
	offset := m.sidebar.YOffset()
	m.frame = 1
	secondView := m.sidebarView()
	if m.sidebar.YOffset() != offset {
		t.Fatal("masthead animation reset sidebar scroll position")
	}
	for _, view := range []string{firstView, secondView} {
		if !strings.Contains(view, "█▀▀ █▀█ █▀▄ ▄▀▄ █▀█ █▀█ █▀█") {
			t.Fatal("fixed masthead disappeared while sidebar content scrolled")
		}
	}
}

func TestSidebarMastheadASCIIOnly(t *testing.T) {
	options := testOptions()
	options.Preferences.ASCII = true
	options.Preferences.NoColor = true
	options.Preferences.ReducedMotion = false
	m := testModel(t, options)
	m.turn = true
	for _, line := range m.sidebarMasthead(30) {
		for _, character := range line {
			if character > unicode.MaxASCII {
				t.Fatalf("ASCII masthead emitted %q", character)
			}
		}
	}
}

func TestSidebarMastheadUsesCompactLockupAtShortHeights(t *testing.T) {
	m, _ := readyModel(t)
	m.resize(140, 18)
	lines := m.sidebarMasthead(max(1, m.layout.sidebarWidth-4))
	if len(lines) != 2 || !strings.Contains(strings.Join(lines, "\n"), "SODAPOP") {
		t.Fatalf("short sidebar did not use the compact lockup: %q", lines)
	}
	assertBounds(t, m.View(), 140, 18)
}

func TestSodapopBrandFitsResponsiveHeadersAndSidebars(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	for _, mode := range []struct {
		name           string
		ascii, noColor bool
		theme          string
	}{
		{name: "neon", theme: "arcade"},
		{name: "plain-unicode", noColor: true},
		{name: "ascii", ascii: true},
		{name: "plain-ascii", ascii: true, noColor: true},
		{name: "high-contrast", theme: "high-contrast"},
	} {
		for _, size := range [][2]int{{1, 3}, {6, 6}, {7, 6}, {44, 24}, {50, 24}, {80, 24}, {119, 30}, {120, 30}, {131, 30}, {132, 30}, {144, 30}, {144, 18}} {
			t.Run(fmt.Sprintf("%s/%dx%d", mode.name, size[0], size[1]), func(t *testing.T) {
				options := testOptions()
				options.Project = "/workspace/sodapop"
				options.Preferences.ASCII, options.Preferences.NoColor = mode.ascii, mode.noColor
				options.Preferences.Theme = mode.theme
				m := testModel(t, options)
				m.composer.SetValue("draft")
				m.resize(size[0], size[1])
				view := m.View()
				assertBounds(t, view, size[0], size[1])
				plain := ansi.Strip(view.Content)
				first := strings.Split(plain, "\n")[0]
				if want := "SODAPOP"[:min(7, size[0])]; !strings.Contains(first, want) {
					t.Fatalf("header lost the brand %q: %q", want, first)
				}
				if view.WindowTitle != "Sodapop / sodapop" {
					t.Fatalf("window title lost the product or project: %q", view.WindowTitle)
				}
				if size[0] >= 44 && (!strings.Contains(plain, "draft") || m.layout.input != 1) {
					t.Fatal("longer brand displaced or expanded the single-line composer")
				}
				if mode.noColor && (plain != view.Content || view.ForegroundColor != nil || view.BackgroundColor != nil) {
					t.Fatal("no-color brand layout emitted styling")
				}
				if mode.ascii {
					for _, r := range plain {
						if r > unicode.MaxASCII {
							t.Fatalf("ASCII brand layout emitted %q", r)
						}
					}
				}
				if m.layout.sidebarVisible {
					if m.layout.mainWidth < minimumConversationWidth {
						t.Fatal("wordmark took width away from the conversation")
					}
					rows := 7
					if size[1] < 20 || size[0] < 132 {
						rows = 2
					}
					masthead := m.sidebarMasthead(m.layout.sidebarWidth - 4)
					if len(masthead) != rows {
						t.Fatalf("sidebar used %d brand rows; want %d at %+v", len(masthead), rows, m.layout)
					}
					if rows == 2 && !strings.Contains(ansi.Strip(strings.Join(masthead, "\n")), "SODAPOP") {
						t.Fatal("compact sidebar clipped the brand")
					}
				}
			})
		}
	}
}

func assertBounds(t *testing.T, view tea.View, width, height int) {
	t.Helper()
	if lipgloss.Height(view.Content) != height {
		t.Fatalf("view height %d; want %d\n%s", lipgloss.Height(view.Content), height, view.Content)
	}
	for i, line := range strings.Split(view.Content, "\n") {
		if lipgloss.Width(line) != width {
			t.Fatalf("line %d width %d; want %d: %q", i, lipgloss.Width(line), width, line)
		}
	}
	if view.Cursor != nil && (view.Cursor.X < 0 || view.Cursor.X >= width || view.Cursor.Y < 0 || view.Cursor.Y >= height) {
		t.Fatalf("cursor outside view: %#v", view.Cursor)
	}
}

func TestDialogsAndPaletteFitAtSupportedSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {40, 10}, {20, 6}} {
		m, _ := readyModel(t)
		m.resize(size[0], size[1])
		m.composer.SetValue("kept draft")
		for _, show := range []func(){m.showAccount, m.showModels, m.showThemes, m.showActions} {
			show()
			assertBounds(t, m.View(), size[0], size[1])
			if size[0] >= 40 && !strings.Contains(m.View().Content, "kept draft") {
				t.Fatal("overlay hid persistent composer")
			}
			m.closeDialog()
		}
		m.composer.SetValue("/")
		m.updatePalette()
		assertBounds(t, m.View(), size[0], size[1])
	}
}

func TestSlashPaletteIsElevatedSearchableAndShowsEightRows(t *testing.T) {
	m, _ := readyModel(t)
	m.resize(80, 24)
	m.composer.SetValue("/")
	m.updatePalette()

	panel, x, y := m.commandPaletteView()
	height := lipgloss.Height(panel)
	if x <= 0 || y+height > m.layout.composerY-2 {
		t.Fatalf("palette was not elevated above the composer: x=%d y=%d height=%d composer=%d", x, y, height, m.layout.composerY)
	}
	for _, expected := range []string{"Type to search commands", "/help", "Discover commands and keyboard shortcuts", "/context"} {
		if !strings.Contains(panel, expected) {
			t.Fatalf("palette omitted %q:\n%s", expected, panel)
		}
	}
	if strings.Contains(panel, "/allow-all") {
		t.Fatalf("palette displayed more than eight command rows:\n%s", panel)
	}
	if !strings.Contains(panel, "1/13") || (!strings.Contains(panel, "↓ 5") && !strings.Contains(panel, "v 5")) {
		t.Fatalf("palette omitted result position or overflow cue:\n%s", panel)
	}

	m.paletteIndex = len(m.paletteItems) - 1
	panel, _, _ = m.commandPaletteView()
	if !strings.Contains(panel, "/exit") || strings.Contains(panel, "/help") {
		t.Fatalf("palette did not keep the selected command in its scrolling window:\n%s", panel)
	}

	m.composer.SetValue("/zzzz")
	m.paletteIndex = 0
	m.updatePalette()
	panel, _, _ = m.commandPaletteView()
	if !strings.Contains(panel, "/zzzz") || !strings.Contains(panel, "No matching commands") {
		t.Fatalf("palette did not expose its search query and empty state:\n%s", panel)
	}
}

func TestSlashPaletteStaysInsideMainColumnAndASCIIChrome(t *testing.T) {
	m, _ := readyModel(t)
	m.resize(140, 30)
	if !m.layout.sidebarVisible {
		t.Fatal("test requires the responsive sidebar")
	}
	m.composer.SetValue("/")
	m.updatePalette()
	panel, x, _ := m.commandPaletteView()
	if x+lipgloss.Width(panel) > m.layout.mainWidth {
		t.Fatalf("palette overlapped the sidebar: x=%d width=%d main=%d", x, lipgloss.Width(panel), m.layout.mainWidth)
	}
	for _, r := range panel {
		if r > unicode.MaxASCII {
			t.Fatalf("ASCII palette emitted non-ASCII chrome: %q", r)
		}
	}
}

func TestNoColorEnvironmentAndReducedMotion(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	options := testOptions()
	options.Preferences.NoColor = false
	m := testModel(t, options)
	if !m.prefs.NoColor || strings.ContainsRune(m.View().Content, '\x1b') {
		t.Fatal("NO_COLOR was not honored")
	}
	m.turn = true
	if m.ensureMotion() != nil {
		t.Fatal("reduced-motion scheduled an animation")
	}
	m.frame = 0
	m.Update(motionMsg{epoch: m.motionEpoch, at: m.now()})
	if m.frame != 0 {
		t.Fatal("a late animation tick moved reduced-motion UI")
	}
	if cursor := m.composer.Cursor(); cursor != nil && cursor.Blink {
		t.Fatal("reduced-motion retained blinking cursor")
	}
}

func TestLoadingIndicatorIsCompactAnimatedAndAccessible(t *testing.T) {
	m, _ := readyModel(t)
	m.prefs.ReducedMotion = false
	m.turn = true
	m.frame = 20
	first := m.loadingView(48)
	m.frame++
	second := m.loadingView(48)
	if first == second || strings.Contains(first, "\n") || !strings.Contains(first, "Thinking") {
		t.Fatalf("loading indicator is not a compact animated status: %q / %q", first, second)
	}
	if strings.ContainsAny(ansi.Strip(first), "○●") {
		t.Fatalf("loading indicator retained the bubble-only animation: %q", first)
	}
	prefix := strings.Fields(ansi.Strip(first))[0]
	if len(prefix) != 10 {
		t.Fatalf("loading scramble has %d characters; want 10: %q", len(prefix), first)
	}

	m.prefs.ReducedMotion = true
	static := m.loadingView(48)
	if !strings.Contains(static, "Thinking") || strings.ContainsAny(static, "#$%&*") {
		t.Fatalf("reduced-motion status should be static and readable: %q", static)
	}
}

func TestLoadingIndicatorCollapsesAtNarrowWidths(t *testing.T) {
	m, _ := readyModel(t)
	m.prefs.ReducedMotion = false
	m.turn = true
	for _, width := range []int{1, 8, 16} {
		rendered := m.loadingView(width)
		if strings.Contains(rendered, "\n") || ansi.StringWidth(rendered) > width {
			t.Fatalf("loading indicator exceeded width %d: %q", width, rendered)
		}
	}
}

func TestOnlyDirtyMarkdownIsRenderedAndMotionDoesNotReparseHistory(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	m, _ := readyModel(t)
	m.prefs.NoColor = false
	m.applyAppearance()
	m.session = engine.Session{ID: "stream"}
	m.turn = true
	event(m, engine.Event{Kind: engine.EventMessage, MessageID: "first", Text: "## Finished\n\nA **readable** answer."})
	baseline := m.renderCount
	event(m, engine.Event{Kind: engine.EventDelta, MessageID: "second", ID: "d1", Text: "A streamed "})
	event(m, engine.Event{Kind: engine.EventDelta, MessageID: "second", ID: "d2", Text: "answer."})
	if m.renderCount != baseline {
		t.Fatal("each delta synchronously rerendered markdown")
	}
	m.flushTimeline()
	if m.renderCount != baseline+1 {
		t.Fatalf("historical messages were reparsed: %d -> %d", baseline, m.renderCount)
	}
	m.prefs.ReducedMotion = false
	m.motionEpoch++
	m.motionPending = true
	m.Update(motionMsg{epoch: m.motionEpoch, at: m.now()})
	m.View()
	if m.renderCount != baseline+1 {
		t.Fatal("activity animation reparsed markdown")
	}
	event(m, engine.Event{Kind: engine.EventMessage, MessageID: "second", ID: "f1", Text: "A streamed answer."})
	if len(m.entries) != 2 || m.entries[1].raw != "A streamed answer." {
		t.Fatal("final reconciliation duplicated cached stream")
	}
}

func TestUntrustedTerminalSequencesAreSanitizedIncludingSplitDeltas(t *testing.T) {
	payload := "before\x1b[2J\x1b]52;c;clipboard-payload\x07\x1b]0;malicious-title\x07after\r\b\x00\u202eevil"
	plain := safeText(payload)
	for _, forbidden := range []string{"\x1b", "clipboard-payload", "malicious-title", "\r", "\b", "\x00", "\u202e"} {
		if strings.Contains(plain, forbidden) {
			t.Fatalf("unsafe text survived sanitizer: %q", plain)
		}
	}
	rendered := safeMarkdown("\x1b[31mred\x1b[0m\x1b]8;;https://evil.example\x07link\x1b]8;;\x07\x1b[2J", false)
	if !strings.Contains(rendered, "\x1b[31m") || strings.Contains(rendered, "\x1b]") || strings.Contains(rendered, "\x1b[2J") {
		t.Fatalf("markdown boundary did not limit escapes to SGR: %q", rendered)
	}
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "safe"}
	m.turn = true
	event(m, engine.Event{Kind: engine.EventDelta, MessageID: "split", ID: "d1", Text: "safe\x1b]52;"})
	m.flushTimeline()
	event(m, engine.Event{Kind: engine.EventDelta, MessageID: "split", ID: "d2", Text: "c;clipboard-payload\x07text"})
	m.flushTimeline()
	view := m.View().Content
	if strings.Contains(view, "\x1b") || strings.Contains(view, "clipboard-payload") || !strings.Contains(view, "safetext") {
		t.Fatalf("split control sequence was not sanitized: %q", view)
	}
}

func TestPermissionShowsHiddenCharactersWithoutExecutingThem(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "permission-session"}
	m.turn = true
	event(m, engine.Event{Kind: engine.EventPermission, Permission: &engine.Permission{
		ID: "control-characters", Path: "odd\nname", Command: "echo safe\x1b[2J\rhidden",
		Respond: func(bool) error { return nil },
	}})
	body := m.overlay.body
	if !strings.Contains(body, `odd\nname`) || !strings.Contains(body, `\u001B[2J\rhidden`) || strings.ContainsRune(body, '\x1b') {
		t.Fatalf("permission detail hid control characters or emitted raw escapes: %q", body)
	}
}

func TestToolCardsAreKeyboardExpandableAndOutputIsSafe(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "tool-session"}
	m.turn = true
	event(m, engine.Event{Kind: engine.EventToolStart, ToolID: "tool", Name: "bash", Arguments: "go test ./..."})
	event(m, engine.Event{Kind: engine.EventToolOutput, ToolID: "tool", Text: "PASS\n\x1b]52;c;ignored\x07details"})
	event(m, engine.Event{Kind: engine.EventToolEnd, ToolID: "tool", Text: "PASS\ndetails"})
	if len(m.tools) != 1 || len(m.entries) != 1 || m.entries[0].state != "done" {
		t.Fatal("tool lifecycle did not reconcile one card")
	}
	m.handleKey(keyPress("f4"))
	m.handleKey(keyPress("enter"))
	if !m.toolFocus || !m.entries[0].expanded {
		t.Fatal("tool card not keyboard expandable")
	}
	if !strings.Contains(m.timeline.GetContent(), "details") || strings.Contains(m.View().Content, "\x1b") {
		t.Fatal("expanded output missing or unsafe")
	}
	m.handleKey(keyPress("esc"))
	if m.toolFocus {
		t.Fatal("tool focus did not return to composer")
	}
}

func TestDiffIsReadOnlyAsyncAndErrorsVisible(t *testing.T) {
	m, _ := readyModel(t)
	w := &fakeWorkspace{diff: workspace.Diff{IsRepository: true, Truncated: true, Text: "+ existing user change\n\x1b[2J"}}
	m.opts.Workspace = w
	m.composer.SetValue("kept draft")
	cmd := m.openDiff("staged")
	if w.diffCalls.Load() != 0 {
		t.Fatal("Git diff ran in UI loop")
	}
	runFinite(t, m, cmd)
	if w.mode != "staged" || !strings.Contains(m.overlay.body, "includes changes not made by Sodapop") || !strings.Contains(m.overlay.body, "truncated") {
		t.Fatal("diff mode / ownership / truncation labels missing")
	}
	if strings.ContainsRune(m.overlay.body, '\x1b') || m.composer.Value() != "kept draft" {
		t.Fatal("diff output unsafe or draft discarded")
	}
	w.err = errors.New("Git is unavailable")
	runFinite(t, m, m.openDiff("all"))
	if !strings.Contains(m.overlay.body, "Git is unavailable") || !m.notice.error {
		t.Fatal("workspace error not visible")
	}
	cmd = m.openDiff("unstaged")
	m.closeDialog()
	runFinite(t, m, cmd)
	if m.overlay != nil {
		t.Fatal("late diff reopened a dismissed overlay")
	}
}

func TestPreferenceWritesAreAsyncSerializedAndNewestWins(t *testing.T) {
	options := testOptions()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var saved []config.Preferences
	options.SavePreferences = func(p config.Preferences) error {
		once.Do(func() { close(entered); <-release })
		mu.Lock()
		saved = append(saved, p)
		mu.Unlock()
		return nil
	}
	m := testModel(t, options)
	cmd, ok := m.selectTheme("midnight")
	if !ok || len(saved) != 0 {
		t.Fatal("theme persistence ran in UI loop")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-entered
	next, ok := m.selectTheme("unicode")
	if !ok || next != nil {
		t.Fatal("concurrent preference edit should update queued snapshot, not start another write")
	}
	close(release)
	m.Update(<-done)
	mu.Lock()
	defer mu.Unlock()
	if len(saved) != 2 || saved[1].Theme != "midnight" || saved[1].ASCII {
		t.Fatalf("latest preference was not persisted: %#v", saved)
	}
}

func TestPreferenceSnapshotsIsolatePerModelSettings(t *testing.T) {
	original := config.DefaultPreferences()
	original.ModelSettings["model-a"] = config.ModelSettings{
		ContextTier: "default", ReasoningEffort: "medium",
	}
	snapshot := clonePreferences(original)
	snapshot.ModelSettings["model-a"] = config.ModelSettings{
		ContextTier: "long_context", ReasoningEffort: "high",
	}
	if original.ModelSettings["model-a"].ContextTier != "default" ||
		original.ModelSettings["model-a"].ReasoningEffort != "medium" {
		t.Fatal("preference snapshot shared its per-model settings map")
	}
}

func TestPreferenceFailureIsVisibleAndQuitProtectsDraft(t *testing.T) {
	options := testOptions()
	options.SavePreferences = func(config.Preferences) error { return errors.New("settings are read-only") }
	m := testModel(t, options)
	cmd, _ := m.selectTheme("high-contrast")
	runFinite(t, m, cmd)
	if m.prefs.Theme != "high-contrast" || !m.notice.error || !strings.Contains(m.notice.text, "saving preferences failed") {
		t.Fatal("preference failure was silently treated as persisted success")
	}
	m.composer.SetValue("do not lose")
	m.handleKey(keyPress("ctrl+c"))
	if m.overlay == nil || m.overlay.kind != dialogConfirm || m.overlay.selected != 0 || m.quitting {
		t.Fatal("idle Ctrl+C did not protect a nonempty draft")
	}
	m.handleKey(keyPress("esc"))
	if m.composer.Value() != "do not lose" || m.quitting {
		t.Fatal("cancelled quit discarded draft")
	}
}

package ui

import (
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownLinksCodeAndTablesRemainReadableWithoutOSC(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	m, _ := readyModel(t)
	m.prefs.NoColor = false
	m.prefs.ASCII = false
	m.applyAppearance()
	m.session = engine.Session{ID: "markdown"}
	m.turn = true
	event(m, engine.Event{
		Kind: engine.EventMessage, MessageID: "formatted",
		Text: "# Hello\n\nSee [docs](https://example.com/reference).\n\n```go\nfmt.Println(\"ok\")\n```\n\n| Name | State |\n| --- | --- |\n| build | ready |",
	})
	rendered := m.timeline.GetContent()
	plain := safeText(rendered)
	for _, want := range []string{"Hello", "https://example.com/reference", `fmt.Println("ok")`, "build", "ready"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("markdown lost %q: %q", want, plain)
		}
	}
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(ansi.Strip(line), "Thinking") {
			continue
		}
		if strings.TrimSpace(ansi.Strip(line)) != "" && !strings.HasPrefix(ansi.Strip(line), "┃ ") {
			t.Fatalf("markdown visual line escaped the assistant rail: %q", line)
		}
	}
	if strings.Contains(rendered, "\x1b]") || strings.Contains(plain, "<no value>") {
		t.Fatalf("markdown emitted OSC or a broken template: %q", rendered)
	}
	m.selectTheme("no-color")
	if strings.ContainsRune(m.timeline.GetContent(), '\x1b') || strings.ContainsRune(m.View().Content, '\x1b') {
		t.Fatal("no-color retained cached markdown/style escape codes")
	}
}

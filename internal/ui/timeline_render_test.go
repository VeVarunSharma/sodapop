package ui

import (
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/charmbracelet/x/ansi"
)

func TestConversationEntriesUseRoleRailsWithoutSpeakerHeadings(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "rails"}
	m.turn = true
	m.entries = []*entry{
		{id: "user", role: "you", raw: "user message", final: true, revision: 1},
		{id: "assistant", role: "assistant", raw: "assistant response", final: true, revision: 1},
	}
	m.renderDirty = true
	m.flushTimeline()

	content := m.timeline.GetContent()
	if !strings.Contains(content, "| user message") {
		t.Fatalf("user entry did not use its ASCII rail: %q", content)
	}
	if !strings.Contains(content, ": assistant response") {
		t.Fatalf("assistant entry did not use its distinct ASCII rail: %q", content)
	}
	if strings.Contains(content, "YOU") || strings.Contains(content, "SODAPOP") {
		t.Fatalf("conversation retained redundant speaker headings: %q", content)
	}
}

func TestConversationRailsPrefixWrappedAndBlankLines(t *testing.T) {
	m, _ := readyModel(t)
	m.resize(12, 12)
	e := &entry{
		id: "wrapped", role: "assistant",
		raw: "one two three four\n\nfinal", final: true,
	}

	rendered := m.renderEntry(e, 0, 12)
	lines := strings.Split(rendered, "\n")
	if len(lines) < 4 {
		t.Fatalf("message did not wrap into the expected visual lines: %q", rendered)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, ": ") {
			t.Fatalf("visual line is missing the assistant rail: %q", line)
		}
		if ansi.StringWidth(line) > 12 {
			t.Fatalf("railed line exceeded the available width: %q", line)
		}
	}
}

func TestConversationRailsRemainDistinctAcrossAppearanceModes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ascii     bool
		noColor   bool
		userRail  string
		modelRail string
	}{
		{name: "ascii no color", ascii: true, noColor: true, userRail: "| ", modelRail: ": "},
		{name: "unicode no color", noColor: true, userRail: "│ ", modelRail: "┃ "},
		{name: "unicode color", userRail: "│ ", modelRail: "┃ "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := testOptions()
			options.Preferences.ASCII = tc.ascii
			options.Preferences.NoColor = tc.noColor
			m := testModel(t, options)
			user := m.renderMessageRail("you", "input", 20)
			model := m.renderMessageRail("assistant", "response", 20)
			if !strings.HasPrefix(ansi.Strip(user), tc.userRail) {
				t.Fatalf("user rail mismatch: %q", user)
			}
			if !strings.HasPrefix(ansi.Strip(model), tc.modelRail) {
				t.Fatalf("assistant rail mismatch: %q", model)
			}
			if tc.noColor && (strings.ContainsRune(user, '\x1b') || strings.ContainsRune(model, '\x1b')) {
				t.Fatal("no-color rails emitted ANSI styling")
			}
		})
	}
}

func TestConversationStateAndTruncationStayInsideRail(t *testing.T) {
	m, _ := readyModel(t)
	e := &entry{
		role: "you", raw: "submitted prompt", final: true,
		state: "delivery uncertain", truncated: true,
	}
	rendered := m.renderEntry(e, 0, 40)
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.HasPrefix(line, "| ") {
			t.Fatalf("metadata escaped the message rail: %q", rendered)
		}
	}
	for _, want := range []string{"[delivery uncertain]", "[Message truncated in the UI;"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("message metadata omitted %q: %q", want, rendered)
		}
	}
}

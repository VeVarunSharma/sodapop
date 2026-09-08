package ui

import (
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func TestResumedToolMessagesBecomeExpandableHistoryCards(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "restored"}
	history := engine.Event{
		Kind: engine.EventMessage, MessageID: "saved-tool-result",
		Role: "tool", Name: "view", Text: "saved tool output", History: true,
	}
	event(m, history)
	event(m, history)
	if len(m.entries) != 1 || len(m.tools) != 1 || m.entries[0].state != "history" {
		t.Fatal("resumed tool output was duplicated or lost its historical state")
	}
	if len(f.sent) != 0 || len(f.newModels) != 0 || len(m.requests) != 0 {
		t.Fatal("restoring history performed an action")
	}
	m.handleKey(keyPress("f4"))
	m.handleKey(keyPress("enter"))
	content := m.timeline.GetContent()
	if !strings.Contains(content, "view") || !strings.Contains(content, "[history]") || !strings.Contains(content, "saved tool output") {
		t.Fatalf("historical tool card is not readable or expandable: %q", content)
	}
}

package ui

import (
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func TestToolHistoryPreservesStableIdentityAndFailureMetadata(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "restored"}
	history := engine.Event{
		Kind: engine.EventMessage, ID: "history-source", MessageID: "history-message",
		ToolID: "tool-call", Role: "tool", Name: "bash", Arguments: "go test ./...",
		Text: "no completion was recorded", History: true, Failed: true,
	}
	event(m, history)
	history.ID, history.MessageID = "final-source", "final-message"
	history.Text = "recorded failure"
	event(m, history)
	if len(m.entries) != 1 || len(m.tools) != 1 || m.tools["tool:tool-call"] != m.entries[0] {
		t.Fatal("tool history did not reconcile by stable ToolID")
	}
	card := m.entries[0]
	if card.raw != "recorded failure" || card.arguments != "go test ./..." || card.state != "history / failed" {
		t.Fatalf("authoritative tool history or failure metadata was lost: %#v", card)
	}
	m.handleKey(keyPress("f4"))
	m.handleKey(keyPress("enter"))
	for _, want := range []string{"history / failed", "go test ./...", "recorded failure"} {
		if !strings.Contains(m.timeline.GetContent(), want) {
			t.Fatalf("expanded historical tool card omitted %q", want)
		}
	}
	if len(f.sent) != 0 || len(f.newModels) != 0 {
		t.Fatal("displaying historical metadata replayed an action")
	}
}

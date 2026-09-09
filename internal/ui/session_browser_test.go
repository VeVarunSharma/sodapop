package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/engine"
)

func TestSessionBrowserFiltersScopesAndHandlesFailures(t *testing.T) {
	m, f := readyModel(t)
	f.sessions = []engine.Session{
		{ID: "saved", Project: "/project", UpdatedAt: time.Date(2026, time.September, 8, 12, 30, 0, 0, time.Local)},
		{ID: "foreign", Project: "/other", Title: "Other project"},
		{ID: "", Project: "/project", Title: "Missing identity"},
	}
	cmd := m.browseSessions()
	if cmd == nil || m.overlay == nil || m.overlay.kind != dialogSessions {
		t.Fatal("session browser did not open asynchronously")
	}
	msg := cmd().(sessionsMsg)
	m.sessionsResult(msg)
	if len(m.overlay.items) != 1 || m.overlay.items[0].id != "saved" ||
		m.overlay.items[0].label != "Untitled conversation" ||
		!strings.Contains(m.overlay.items[0].detail, "Sep 08 12:30") {
		t.Fatalf("session items = %#v", m.overlay.items)
	}
	m.confirmResume("saved")
	if m.overlay == nil || m.overlay.kind != dialogConfirm || m.overlay.action != "resume" ||
		m.overlay.value != "saved" {
		t.Fatal("resume confirmation was not bound to the selected session")
	}

	m.browseSessions()
	dialog := m.overlay.id
	m.sessionsResult(sessionsMsg{
		generation: m.engineGeneration, dialog: dialog, err: errors.New("index unavailable"),
	})
	if !m.notice.error || !strings.Contains(m.overlay.body, "index unavailable") {
		t.Fatalf("session failure = %#v, %q", m.notice, m.overlay.body)
	}
	m.sessionsResult(sessionsMsg{generation: m.engineGeneration - 1, dialog: dialog})
	if !strings.Contains(m.overlay.body, "index unavailable") {
		t.Fatal("stale session result replaced the active dialog")
	}
}

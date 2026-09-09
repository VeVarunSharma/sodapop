package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/auth"
	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/skills"
)

func TestManageSkillsUpdatesSidebarAndReconnectsOnActivation(t *testing.T) {
	m, _ := readyModel(t)
	digest := strings.Repeat("a", 64)
	state := skills.State{Installed: []skills.Entry{{
		Name: "test-skill", Description: "Test instructions", Digest: digest,
		Source: skills.Source{Kind: "local", Location: "/trusted/test-skill"},
	}}}
	m.setSkillState(state)
	m.opts.ManageSkills = func(_ context.Context, action skills.Action) (skills.State, error) {
		if action.Operation != "enable" || action.Value != "test-skill" {
			t.Fatalf("skill action = %#v", action)
		}
		state.Enabled = []skills.Reference{{Name: "test-skill", Digest: digest}}
		return state, nil
	}
	m.opts.NewEngine = func(context.Context, auth.Account) (engine.Engine, error) {
		return newFakeEngine(), nil
	}
	m.session = engine.Session{ID: "old-session"}
	cmd, ok := m.manageSkills("enable test-skill")
	if !ok || cmd == nil {
		t.Fatal("enable did not schedule skill persistence")
	}
	_, reconnect := m.Update(cmd())
	if reconnect == nil || m.session.ID != "" || len(m.skills) != 1 || !m.skills[0].Active || !m.connecting {
		t.Fatalf("activation did not reset and reconnect: session=%q skills=%#v connecting=%t", m.session.ID, m.skills, m.connecting)
	}
}

func TestSkillsDialogExplainsIsolation(t *testing.T) {
	m := testModel(t, testOptions())
	m.setSkillState(skills.State{Installed: []skills.Entry{{
		Name: "review", Description: "Review code", Digest: strings.Repeat("b", 64),
		Source: skills.Source{Kind: "git", Location: "https://example.com/review.git", Revision: strings.Repeat("c", 40)},
	}}})
	m.showSkills()
	if m.overlay == nil || m.overlay.kind != dialogSkills ||
		!strings.Contains(m.overlay.body, "Ambient Copilot skills") ||
		!strings.Contains(m.overlay.body, "/skill trust") {
		t.Fatalf("skills dialog omitted isolation guidance: %#v", m.overlay)
	}
}

func TestSkillResultsRejectStaleAndReportFailures(t *testing.T) {
	m := testModel(t, testOptions())
	m.skillGeneration = 3
	m.skillsResult(skillsMsg{
		generation: 2,
		state: skills.State{Installed: []skills.Entry{{
			Name: "stale", Description: "stale", Digest: strings.Repeat("a", 64),
		}}},
	})
	if len(m.skills) != 0 {
		t.Fatal("stale skill result mutated the sidebar")
	}
	m.skillsResult(skillsMsg{generation: 3, action: "install", err: context.Canceled})
	if !m.notice.error || !strings.Contains(m.notice.text, "failed") {
		t.Fatalf("skill failure was not surfaced: %#v", m.notice)
	}
}

func TestManageSkillsValidatesCommandShape(t *testing.T) {
	m := testModel(t, testOptions())
	if cmd, ok := m.manageSkills("enable two names"); ok || cmd != nil {
		t.Fatal("accepted malformed enable command")
	}
	if cmd, ok := m.manageSkills("unknown value"); ok || cmd != nil {
		t.Fatal("accepted unknown skill operation")
	}
	m.opts.ManageSkills = nil
	if cmd, ok := m.manageSkills("add /tmp/skill"); ok || cmd != nil || !m.notice.error {
		t.Fatal("accepted management without a configured skill service")
	}
}

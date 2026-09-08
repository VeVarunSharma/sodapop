package ui

import "testing"

func TestPlanEnableAndDisableAreIdempotentWithoutSending(t *testing.T) {
	m, f := readyModel(t)
	for _, step := range []struct {
		command string
		enabled bool
	}{
		{"/plan", true},
		{"/plan", true},
		{"/plan off", false},
		{"/plan off", false},
	} {
		m.composer.SetValue(step.command)
		runFinite(t, m, m.submit())
		if m.planning != step.enabled {
			t.Fatalf("%s: planning = %t, want %t", step.command, m.planning, step.enabled)
		}
		if len(f.sent) != 0 || len(f.newModels) != 0 || m.session.ID != "" {
			t.Fatalf("%s created a session or sent a prompt", step.command)
		}
	}
	m.composer.SetValue("/plan explain the tradeoffs")
	runFinite(t, m, m.submit())
	if !m.planning || len(f.sent) != 1 || !f.sent[0].Planning || f.sent[0].Text != "explain the tradeoffs" {
		t.Fatalf("/plan with a prompt did not enable and send advisory planning: %#v", f.sent)
	}
}

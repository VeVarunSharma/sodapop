package ui

import (
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
)

func TestPersonalityActivityCopyKeepsTechnicalLabel(t *testing.T) {
	m := testModel(t, testOptions())
	for _, tc := range []struct {
		personality string
		want        string
	}{
		{personality: "quiet", want: "Running bash"},
		{personality: "playful", want: "Running bash / mixing"},
		{personality: "extra", want: "Running bash / mixing carefully"},
	} {
		m.prefs.Personality = tc.personality
		if got := m.activityCopy("Running bash"); got != tc.want {
			t.Errorf("%s activity copy = %q, want %q", tc.personality, got, tc.want)
		}
		if !strings.Contains(m.activityCopy("Generating"), "Generating") {
			t.Errorf("%s activity copy hid the technical state", tc.personality)
		}
	}
}

func TestPersonalityUsesBubblesRatherThanBrandAsAVerb(t *testing.T) {
	m, _ := readyModel(t)
	m.turn = true
	for _, tc := range []struct {
		personality, activity, status string
	}{
		{"quiet", "Synchronizing", "working"},
		{"playful", "Synchronizing / building bubbles", "bubbling"},
		{"extra", "Synchronizing / building extra bubbles", "bubbling brightly"},
	} {
		m.prefs.Personality = tc.personality
		if got := m.activityCopy("Synchronizing"); got != tc.activity {
			t.Errorf("%s activity = %q; want %q", tc.personality, got, tc.activity)
		}
		if got := m.operationLabel(); got != tc.status {
			t.Errorf("%s status = %q; want %q", tc.personality, got, tc.status)
		}
	}
}

func TestPerfectPourRequiresStructuredSuccessfulValidation(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turnSequence, m.turn = 1, true
	m.beginTurnExperience()

	event(m, engine.Event{
		Kind: engine.EventToolStart, ToolID: "validate", Name: "bash",
		Arguments: `{"command":"go test ./internal/ui"}`,
	})
	event(m, engine.Event{
		Kind: engine.EventToolEnd, ToolID: "validate", Name: "bash",
		Arguments: `{"command":"go test ./internal/ui"}`,
	})
	cmd := event(m, engine.Event{Kind: engine.EventIdle, ID: "idle"})
	if cmd == nil || m.moment.kind != reactionSuccess || !strings.Contains(m.moment.text, "Perfect pour") || !m.experience.celebrated {
		t.Fatalf("successful validation did not produce a perfect pour: %#v", m.moment)
	}

	first := m.moment.id
	m.startMoment(reactionRecovery)
	m.Update(momentExpiredMsg{id: first})
	if m.moment.kind != reactionRecovery {
		t.Fatal("stale moment expiry cleared a newer reaction")
	}
}

func TestPerfectPourRejectsFailureAbortAndNonValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		command   string
		failed    bool
		errored   bool
		aborted   bool
		wantValid bool
	}{
		{name: "ordinary command", command: "echo done"},
		{name: "failed validation", command: "go test ./...", failed: true},
		{name: "errored turn", command: "go test ./...", errored: true},
		{name: "aborted turn", command: "go test ./...", aborted: true},
		{name: "successful validation", command: "go test ./...", wantValid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := readyModel(t)
			m.turnSequence = 3
			m.beginTurnExperience()
			args := `{"command":"` + tc.command + `"}`
			m.recordToolStart("tool", "bash", args)
			m.recordToolEnd("tool", "bash", args, tc.failed)
			m.experience.errored = tc.errored
			m.experience.aborted = tc.aborted
			if got := m.perfectPour(); got != tc.wantValid {
				t.Fatalf("perfectPour() = %t, want %t", got, tc.wantValid)
			}
		})
	}
}

func TestPerfectPourWaitsForLateSendAcknowledgement(t *testing.T) {
	m, _ := readyModel(t)
	m.session = engine.Session{ID: "session"}
	m.turnSequence, m.turn, m.sendPending = 7, true, true
	m.beginTurnExperience()
	args := `{"command":"go test ./..."}`
	m.recordToolStart("tool", "bash", args)
	m.recordToolEnd("tool", "bash", args, false)

	event(m, engine.Event{Kind: engine.EventIdle, ID: "idle"})
	if m.moment.id != 0 || m.experience.celebrated {
		t.Fatal("idle celebrated before prompt delivery was confirmed")
	}
	cmd := m.sendResult(sentMsg{
		generation: m.engineGeneration,
		session:    m.sessionGeneration,
		turn:       m.turnSequence,
	})
	if cmd == nil || m.moment.kind != reactionSuccess || !m.experience.celebrated {
		t.Fatal("successful late acknowledgement did not release the celebration")
	}
	if duplicate := m.sendResult(sentMsg{
		generation: m.engineGeneration,
		session:    m.sessionGeneration,
		turn:       m.turnSequence,
	}); duplicate != nil {
		t.Fatal("duplicate acknowledgement retriggered a perfect pour")
	}
}

func TestMomentRenderingRespectsReducedMotionAndASCII(t *testing.T) {
	m := testModel(t, testOptions())
	m.prefs.Personality = "playful"
	m.prefs.ASCII = true
	m.prefs.ReducedMotion = true
	m.startMoment(reactionSuccess)
	first := m.momentView(80)
	m.frame = 99
	second := m.momentView(80)
	if first != second || strings.ContainsAny(first, "✦○●") {
		t.Fatalf("reduced-motion ASCII moment was animated or non-ASCII: %q / %q", first, second)
	}
}

func TestStartupCopyIsDeterministicAndStateAware(t *testing.T) {
	m := testModel(t, testOptions())
	m.prefs.Personality = "playful"
	m.status = workspace.Status{IsRepository: true}
	if got := m.startupTagline(); got != "Fresh branch. Plenty of bubbles." {
		t.Fatalf("clean repository tagline = %q", got)
	}
	m.status.Entries = []workspace.Entry{{Path: "one"}, {Path: "two"}}
	if got := m.startupTagline(); got != "2 changed files. Let's add some bubbles." {
		t.Fatalf("changed repository tagline = %q", got)
	}
	m.prefs.Personality = "quiet"
	if got := m.startupTagline(); got != "Terminal coding companion." {
		t.Fatalf("quiet tagline = %q", got)
	}
}

func TestExitRecapCountsOnlyCurrentNonHistoryEntries(t *testing.T) {
	m := testModel(t, testOptions())
	m.status = workspace.Status{
		IsRepository: true,
		Entries:      []workspace.Entry{{Path: "one.go"}, {Path: "two.go"}},
	}
	m.entries = []*entry{
		{role: "you", history: true},
		{role: "tool", state: "done", history: true},
		{role: "you"},
		{role: "tool", state: "done"},
		{role: "tool", state: "failed"},
		{role: "tool", state: "running"},
	}
	recap := m.exitRecap()
	for _, want := range []string{
		"BOTTLE CAP", "Prompts: 1", "Tools: 3", "1 succeeded", "1 failed",
		"1 running", "Current known changed files: 2", "State: settled",
	} {
		if !strings.Contains(recap, want) {
			t.Fatalf("exit recap missing %q:\n%s", want, recap)
		}
	}
	m.turn = true
	if recap := m.exitRecap(); !strings.Contains(recap, "active or unresolved") {
		t.Fatalf("active recap hid unresolved state:\n%s", recap)
	}
}

func TestExitConfirmationIncludesRecapWithoutWeakeningSafetyCopy(t *testing.T) {
	m, _ := readyModel(t)
	m.turn = true
	m.composer.SetValue("unsent")
	m.confirmExit()
	if m.overlay == nil {
		t.Fatal("exit confirmation was not opened")
	}
	for _, want := range []string{
		"Stop active work", "Completed edits will NOT be undone",
		"There is an unsent draft", "BOTTLE CAP", "active or unresolved",
	} {
		if !strings.Contains(m.overlay.body, want) {
			t.Fatalf("exit confirmation missing %q:\n%s", want, m.overlay.body)
		}
	}
}

func TestThemeSelectsPersonalityWithoutChangingAccessibility(t *testing.T) {
	m := testModel(t, testOptions())
	beforeMotion, beforeColor, beforeASCII := m.prefs.ReducedMotion, m.prefs.NoColor, m.prefs.ASCII
	for _, value := range []string{"personality-quiet", "personality-playful", "personality-extra"} {
		if _, ok := m.selectTheme(value); !ok {
			t.Fatalf("theme rejected %q", value)
		}
	}
	if m.prefs.Personality != "extra" {
		t.Fatalf("personality selection = %q", m.prefs.Personality)
	}
	if m.prefs.ReducedMotion != beforeMotion || m.prefs.NoColor != beforeColor || m.prefs.ASCII != beforeASCII {
		t.Fatal("personality selection changed an accessibility preference")
	}
}

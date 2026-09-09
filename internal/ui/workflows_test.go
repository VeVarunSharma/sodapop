package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/VeVarunSharma/sodapop/internal/workspace"
)

func TestFizzSendsOneShotIdeationWorkflow(t *testing.T) {
	m, f := readyModel(t)
	m.composer.SetValue("/fizz compare queues\nand worker pools")

	runFinite(t, m, m.submit())

	if len(f.sent) != 1 {
		t.Fatalf("fizz sent %d prompts, want 1", len(f.sent))
	}
	prompt := f.sent[0].Text
	for _, want := range []string{
		workflowPrefix + " FIZZ",
		"compare queues\nand worker pools",
		"meaningfully different ideas",
		"Do not edit files",
		"wait for a follow-up request",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("fizz prompt missing %q:\n%s", want, prompt)
		}
	}
	if f.sent[0].Planning || m.planning {
		t.Fatal("fizz changed ordinary chat into planning mode")
	}
	if m.composer.Value() != "" {
		t.Fatalf("accepted fizz command was not cleared: %q", m.composer.Value())
	}
}

func TestFizzPreservesExistingPlanMode(t *testing.T) {
	m, f := readyModel(t)
	m.planning = true
	m.composer.SetValue("/fizz")

	runFinite(t, m, m.submit())

	if len(f.sent) != 1 || !f.sent[0].Planning || !m.planning {
		t.Fatalf("fizz did not preserve plan mode: sent=%#v planning=%t", f.sent, m.planning)
	}
	if !strings.Contains(f.sent[0].Text, "active request and visible conversation") {
		t.Fatal("topic-free fizz prompt omitted its conversation focus")
	}
}

func TestTasteTestUsesSessionDiffAndReportsOnly(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session-a", Project: "/project", Model: "model-a"}
	m.entries = append(m.entries, &entry{role: "you", raw: "Fix cancellation without replaying work.", final: true})
	m.baseline = &fakeBaseline{diff: workspace.Diff{
		IsRepository: true,
		Text:         "OBSERVED SINCE THIS CONVERSATION'S BASELINE\n+cancel safely",
	}}
	m.composer.SetValue("/taste-test focus on stale callbacks\nand cancellation")

	cmd := m.submit()
	if m.operation.kind != "preparing taste test" || len(f.sent) != 0 {
		t.Fatalf("taste test did not prepare asynchronously: operation=%q sent=%d", m.operation.kind, len(f.sent))
	}
	if m.composer.Value() == "" {
		t.Fatal("taste-test command cleared before preparation completed")
	}

	runFinite(t, m, cmd)

	if len(f.sent) != 1 {
		t.Fatalf("taste test sent %d prompts, want 1", len(f.sent))
	}
	prompt := f.sent[0].Text
	for _, want := range []string{
		workflowPrefix + " TASTE TEST",
		"Fix cancellation without replaying work.",
		"focus on stale callbacks\nand cancellation",
		"+cancel safely",
		"report-only validation workflow",
		"Do not edit files",
		"- PASSED:",
		"- FAILED:",
		"- UNVERIFIED:",
		"- EVIDENCE:",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("taste-test prompt missing %q:\n%s", want, prompt)
		}
	}
	if m.operation.kind != "" || m.composer.Value() != "" {
		t.Fatalf("taste-test preparation did not settle: operation=%q draft=%q", m.operation.kind, m.composer.Value())
	}
}

func TestTasteTestContinuesWithExplicitPartialEvidenceWarning(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session-a", Project: "/project", Model: "model-a"}
	m.baseline = &fakeBaseline{diff: workspace.Diff{
		IsRepository: true,
		Truncated:    true,
		Text:         "PARTIAL BASELINE\n+known change",
	}}
	m.composer.SetValue("/taste-test")

	runFinite(t, m, m.submit())

	if len(f.sent) != 1 {
		t.Fatalf("partial taste test sent %d prompts, want 1", len(f.sent))
	}
	for _, want := range []string{"IMPORTANT EVIDENCE LIMITATION", "partial or truncated", "PARTIAL BASELINE"} {
		if !strings.Contains(f.sent[0].Text, want) {
			t.Errorf("partial taste-test prompt missing %q:\n%s", want, f.sent[0].Text)
		}
	}
	if !strings.Contains(m.notice.text, "incomplete evidence") || m.notice.error {
		t.Fatalf("partial evidence warning not visible: %+v", m.notice)
	}
}

func TestTasteTestContinuesWhenBaselineReadFails(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session-a", Project: "/project", Model: "model-a"}
	m.baseline = &fakeBaseline{err: errors.New("snapshot unavailable")}
	m.composer.SetValue("/taste-test")

	runFinite(t, m, m.submit())

	if len(f.sent) != 1 || !strings.Contains(f.sent[0].Text, "snapshot unavailable") ||
		!strings.Contains(f.sent[0].Text, "No conversation-change diff is available") {
		t.Fatalf("failed baseline was not disclosed in continued taste test: %#v", f.sent)
	}
}

func TestTasteTestCancellationDropsLatePreparationWithoutEngineAbort(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "session-a", Project: "/project", Model: "model-a"}
	m.baseline = &fakeBaseline{diff: workspace.Diff{IsRepository: true, Text: "+late"}}
	m.composer.SetValue("/taste-test keep this draft")

	cmd := m.submit()
	msg := cmd()
	if cancel := m.cancelWork(); cancel != nil {
		t.Fatal("local taste-test preparation cancellation invoked the engine")
	}
	if m.operation.kind != "" || m.canceling || m.needsResume {
		t.Fatalf("local cancellation disturbed the session: operation=%q canceling=%t resume=%t", m.operation.kind, m.canceling, m.needsResume)
	}
	m.Update(msg)
	if len(f.sent) != 0 || m.composer.Value() != "/taste-test keep this draft" {
		t.Fatalf("late preparation sent or discarded the draft: sent=%d draft=%q", len(f.sent), m.composer.Value())
	}
}

func TestVendingMachineIsCuratedAndRespectsBusyCommands(t *testing.T) {
	m, _ := readyModel(t)
	m.composer.SetValue("/vending-machine")
	runFinite(t, m, m.submit())

	if m.overlay == nil || m.overlay.kind != dialogVending {
		t.Fatal("vending-machine command did not open its dialog")
	}
	groups := make(map[string]bool)
	for _, item := range m.overlay.items {
		groups[item.group] = true
	}
	for _, want := range []string{"Create", "Inspect & Validate", "Customize", "Connect & Extend"} {
		if !groups[want] {
			t.Errorf("vending machine missing %q group: %#v", want, m.overlay.items)
		}
	}

	m.closeDialog()
	m.turn = true
	m.showVendingMachine()
	var fizzDisabled, themeDisabled bool
	for _, item := range m.overlay.items {
		switch item.id {
		case "command:fizz":
			fizzDisabled = item.disabled
		case "command:theme":
			themeDisabled = item.disabled
		}
	}
	if !fizzDisabled || themeDisabled {
		t.Fatalf("vending busy state wrong: fizz disabled=%t theme disabled=%t", fizzDisabled, themeDisabled)
	}
	view := m.View().Content
	if !strings.Contains(view, "CREATE") || !strings.Contains(view, "/fizz") {
		t.Fatalf("grouped vending menu not rendered:\n%s", view)
	}
}

func TestVendingSelectionUsesNormalCommandExecution(t *testing.T) {
	m, _ := readyModel(t)
	m.composer.SetValue("keep my draft")
	m.showVendingMachine()
	for i, item := range m.overlay.items {
		if item.id == "command:theme" {
			m.overlay.selected = i
			break
		}
	}

	m.handleDialogKey(keyPress("enter"))

	if m.overlay == nil || m.overlay.kind != dialogThemes || m.composer.Value() != "keep my draft" {
		t.Fatalf("vending selection bypassed command routing or lost draft: overlay=%#v draft=%q", m.overlay, m.composer.Value())
	}
}

func TestWorkflowEvidenceIsSanitizedAndBounded(t *testing.T) {
	input := strings.Repeat("é", workflowObjectiveLimit) + "\x1b[2J"
	got, truncated := boundedWorkflowText(input, workflowObjectiveLimit)
	if !truncated || !strings.Contains(got, "Additional content omitted") || strings.Contains(got, "\x1b") {
		t.Fatalf("workflow text was not safely bounded: truncated=%t size=%d", truncated, len(got))
	}
}

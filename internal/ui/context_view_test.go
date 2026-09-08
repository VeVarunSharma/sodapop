package ui

import (
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/engine"
	"github.com/charmbracelet/x/ansi"
)

func sampleContextUsage() engine.ContextUsage {
	return engine.ContextUsage{
		Model: "claude-sonnet-4.5", TotalTokens: 15000, PromptTokenLimit: 128000,
		Limit: 128000, BufferTokens: 6400, CompactionThreshold: 121600,
		SystemPrompt: engine.ContextCategory{Tokens: 7100},
		SystemTools:  engine.ContextCategory{Tokens: 8200},
		Messages:     engine.ContextCategory{Tokens: 71},
		FreeSpace:    engine.ContextCategory{Tokens: 106229},
		Buffer:       engine.ContextCategory{Tokens: 6400},
	}
}

func TestContextViewMatchesCopilotGridAndLegend(t *testing.T) {
	options := testOptions()
	options.Preferences.ASCII = false
	m := testModel(t, options)
	out := ansi.Strip(m.renderContextUsage(sampleContextUsage(), 100))
	for _, want := range []string{
		"Context Usage",
		"claude-sonnet-4.5 · 15k/128k tokens (12%)",
		"○ System Prompt",
		"7.1k",
		"◌ System Tools",
		"8.2k",
		"● MCP Tools",
		"◉ Messages",
		"71",
		"(<1%)",
		"· Free Space",
		"106.2k",
		"◎ Buffer",
		"6.4k",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("context view missing %q:\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 9 {
		t.Fatalf("context grid was not rendered beside its legend:\n%s", out)
	}
	for _, line := range lines[2:9] {
		if !strings.Contains(line, "   ") {
			t.Fatalf("grid and legend lost their separator: %q", line)
		}
	}
}

func TestContextGridPreservesSmallCategoriesAndCapacity(t *testing.T) {
	buckets := []contextBucket{
		{glyph: "a", ascii: "a", label: "a", tokens: 1},
		{glyph: "b", ascii: "b", label: "b", tokens: 1},
		{glyph: "·", ascii: ".", label: "Free Space", tokens: 1},
		{glyph: "z", ascii: "z", label: "z", tokens: 95},
	}
	counts := allocateContextCells(buckets, 100, 100)
	total := 0
	for _, count := range counts {
		total += count
	}
	if total != 100 || counts[0] != 1 || counts[1] != 1 || counts[2] < 1 {
		t.Fatalf("unexpected grid allocation: %#v", counts)
	}
}

func TestContextGridUsesCharacterSetForUnallocatedCells(t *testing.T) {
	buckets := []contextBucket{{glyph: "○", ascii: "S", label: "System Prompt"}}
	if got := contextGrid(buckets, 0, 1, false)[0]; got != "· · · · · · · · · ·" {
		t.Fatalf("unicode grid used the wrong filler: %q", got)
	}
	if got := contextGrid(buckets, 0, 1, true)[0]; got != ". . . . . . . . . ." {
		t.Fatalf("ASCII grid used the wrong filler: %q", got)
	}
}

func TestContextViewIncludesAttributionAndASCIIFallback(t *testing.T) {
	options := testOptions()
	options.Preferences.ASCII = true
	options.Preferences.NoColor = true
	m := testModel(t, options)
	usage := sampleContextUsage()
	usage.CustomInstructions = engine.ContextCategory{Tokens: 500}
	usage.Entries = []engine.ContextEntry{
		{Kind: "skill", Label: "large-skill", Tokens: 12000},
		{Kind: "skill", Label: "small-skill", Tokens: 20},
		{Kind: "tool", Label: "bash", Tokens: 1200},
	}
	usage.HeaviestMessages = []engine.ContextMessage{{Label: "tool: bash", Role: "tool", Tokens: 900}}
	usage.Compactions = 1
	out := m.renderContextUsage(usage, 100)
	for _, want := range []string{
		"I Custom Instructions", "Skills", "large-skill", "small-skill",
		"Tools", "bash", "Heaviest messages", "tool: bash",
		"Compactions: 1 successful compaction", "########",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ASCII context view missing %q:\n%s", want, out)
		}
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Fatal("no-color context view emitted ANSI styling")
	}
}

func TestContextCommandIsLocalBusySafeAndRejectsStaleResults(t *testing.T) {
	m, f := readyModel(t)
	m.session = engine.Session{ID: "context-session"}
	m.sessionGeneration = 7
	f.contextResult = sampleContextUsage()
	m.turn = true
	m.composer.SetValue("/context")
	runFinite(t, m, m.submit())
	if f.contextCalls != 1 || len(f.sent) != 0 || m.composer.Value() != "" {
		t.Fatalf("context was blocked, sent, or kept as a prompt: calls=%d sent=%#v draft=%q", f.contextCalls, f.sent, m.composer.Value())
	}
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].role != "context" ||
		!strings.Contains(ansi.Strip(m.timeline.GetContent()), "Context Usage") {
		t.Fatalf("context result was not added locally: %#v", m.entries)
	}

	before := len(m.entries)
	m.contextSequence = 9
	m.contextResult(contextMsg{
		generation: m.engineGeneration, session: m.sessionGeneration - 1, request: 9,
		usage: sampleContextUsage(),
	})
	if len(m.entries) != before {
		t.Fatal("stale context result mutated the replacement session")
	}
}

func TestResetConversationCancelsContextRequest(t *testing.T) {
	m, _ := readyModel(t)
	canceled := false
	m.contextCancel = func() { canceled = true }
	m.resetConversation()
	if !canceled || m.contextCancel != nil {
		t.Fatal("reset did not release the in-flight context request")
	}
}

func TestContextCommandExplainsUnavailableAndVariableModels(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{engine.ErrContextUnavailable, "Context information is not yet available"},
		{engine.ErrVariableContext, "Context usage is unavailable for HydraFusion"},
	} {
		m, f := readyModel(t)
		m.session = engine.Session{ID: "context-session"}
		f.contextErr = tc.err
		m.composer.SetValue("/context")
		runFinite(t, m, m.submit())
		if !strings.Contains(ansi.Strip(m.timeline.GetContent()), tc.want) {
			t.Fatalf("missing context availability message %q", tc.want)
		}
	}
}

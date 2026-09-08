package commands

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		text, name, args, prompt string
	}{
		{"/model chosen-model", "model", "chosen-model", ""},
		{"/HELP /plan", "help", "/plan", ""},
		{"/plan think first\nthen propose steps", "plan", "think first\nthen propose steps", ""},
		{"/plan off", "plan", "off", ""},
		{"/compact preserve decisions\nand failures", "compact", "preserve decisions\nand failures", ""},
		{"/allow-all", "allow-all", "", ""},
		{"/allow-all OFF", "allow-all", "off", ""},
		{"/theme reduced-motion", "theme", "reduced-motion", ""},
		{"/login", "login", "", ""},
		{"/logout", "logout", "", ""},
		{"/clear", "clear", "", ""},
		{"//usr/local", "", "", "/usr/local"},
		{" //leave whitespace", "", "", " //leave whitespace"},
		{"Here is code:\n/unknown", "", "", "Here is code:\n/unknown"},
	}
	for _, tc := range cases {
		got, err := Parse(tc.text)
		if err != nil || got.Command != tc.name || got.Args != tc.args || got.IsCommand != (tc.name != "") {
			t.Errorf("parse %q: %#v, %v", tc.text, got, err)
		}
		if !got.IsCommand && got.Text != tc.prompt {
			t.Errorf("prompt changed: got %q want %q", got.Text, tc.prompt)
		}
	}
}

func TestInvalidCommandsAreLocalErrors(t *testing.T) {
	for _, text := range []string{"", " \n", "/", "/unknown", "/mo", "/new", "/clear now", "/login now", "/logout now", "/exit now", "/model two ids", "/allow-all on", "/diff other", "/help missing"} {
		if _, err := Parse(text); err == nil {
			t.Errorf("accepted invalid input %q", text)
		}
	}
}

func TestRegistryIsSharedButNotMutable(t *testing.T) {
	all := All()
	if len(all) != 13 {
		t.Fatal("expected thirteen commands")
	}
	if _, ok := Lookup("new"); ok {
		t.Fatal("legacy /new command is still registered")
	}
	all[0].Name = "changed"
	if _, ok := Lookup("help"); !ok {
		t.Fatal("caller mutated registry")
	}
	for _, name := range []string{"help", "logout", "theme", "allow-all", "diff", "context", "exit"} {
		command, ok := Lookup(name)
		if !ok || !command.AllowedWhileBusy {
			t.Errorf("expected local command %s to work while busy", name)
		}
	}
	for _, name := range []string{"login", "model", "clear", "resume", "compact", "plan"} {
		command, ok := Lookup(name)
		if !ok || command.AllowedWhileBusy {
			t.Errorf("unsafe busy command %s", name)
		}
	}
}

func TestCompletionAndHelp(t *testing.T) {
	for query, want := range map[string]string{"/CL": "clear", "/COM": "compact", "/MO": "model", "mdl": "model", "rs": "resume", "model chosen": "model"} {
		matches := Match(query)
		if len(matches) == 0 || matches[0].Name != want {
			t.Errorf("match %q: %#v", query, matches)
		}
	}
}

func TestPlanPreservesFreeformSpacing(t *testing.T) {
	for _, tc := range []struct {
		text string
		args string
	}{
		{"/plan   keep  spaces \n  and indentation\n\n", "  keep  spaces \n  and indentation\n\n"},
		{"/plan\n\n  pasted code\n", "\n  pasted code\n"},
		{"/plan\tprompt\r\n\tmore  ", "prompt\r\n\tmore  "},
		{"/plan \t\n", ""},
		{"/plan OFF \n", "off"},
		{"/plan discuss off  ", "discuss off  "},
	} {
		input, err := Parse(tc.text)
		if err != nil || input.Command != "plan" || input.Args != tc.args {
			t.Errorf("Parse(%q) = %#v, %v; want args %q", tc.text, input, err, tc.args)
		}
	}
}

func TestCompactPreservesFreeformSpacing(t *testing.T) {
	for _, tc := range []struct {
		text string
		args string
	}{
		{"/compact   keep  decisions \n  and errors\n\n", "  keep  decisions \n  and errors\n\n"},
		{"/compact\n\n  preserve code\n", "\n  preserve code\n"},
		{"/compact \t\n", ""},
	} {
		input, err := Parse(tc.text)
		if err != nil || input.Command != "compact" || input.Args != tc.args {
			t.Errorf("Parse(%q) = %#v, %v; want args %q", tc.text, input, err, tc.args)
		}
	}
}

func TestPromptsAndNamesStayExact(t *testing.T) {
	for _, text := range []string{
		"  leading and trailing  \r\n\n",
		"```go\n// code comment\npath := \"/tmp\"\n```\n",
		"\n/exit", " /help", "later /model\n/unknown",
	} {
		input, err := Parse(text)
		if err != nil || input.IsCommand || input.Text != text {
			t.Errorf("prompt changed: %#v, %v", input, err)
		}
	}
	for _, text := range []string{"//", "///literal\n  preserve  \n", "//unknown argument\nmore"} {
		input, err := Parse(text)
		if err != nil || input.IsCommand || input.Text != text[1:] {
			t.Errorf("literal slash changed: %#v, %v", input, err)
		}
	}
	for _, text := range []string{"", " \n\t", "\u2003\n"} {
		if _, err := Parse(text); err == nil {
			t.Errorf("whitespace-only prompt accepted: %q", text)
		}
	}
	for _, tc := range []struct {
		text string
		args string
	}{
		{"/MODEL Provider/Model-v2:Preview", "Provider/Model-v2:Preview"},
		{"/resume Case-Sensitive-session_ID", "Case-Sensitive-session_ID"},
		{"/context", ""},
		{"/theme Future-Custom-Theme", "Future-Custom-Theme"},
		{"/theme no-color", "no-color"},
		{"/theme unicode", "unicode"},
		{"/diff STAGED", "staged"},
	} {
		input, err := Parse(tc.text)
		if err != nil || input.Args != tc.args {
			t.Errorf("handler-owned argument changed: %#v, %v", input, err)
		}
	}
}

func TestSuggestionsAndErrorsCannotBecomePrompts(t *testing.T) {
	for _, text := range []string{"/", "/unknown", "/modle", "/help modle", "/new", "/clear now", "/context now", "/exit\nlater", "/diff all staged"} {
		input, err := Parse(text)
		if err == nil || !input.IsCommand || input.Text != "" {
			t.Errorf("invalid command could become a prompt: %#v, %v", input, err)
		}
	}
	for _, text := range []string{"/modle", "/help modle", "/hepl"} {
		if _, err := Parse(text); err == nil || !strings.Contains(err.Error(), "did you mean /") {
			t.Errorf("missing local suggestion for %q: %v", text, err)
		}
	}
	if matches := Match(strings.Repeat("z", 100000)); len(matches) != 0 {
		t.Fatal("unexpected matches for oversized query")
	}
	matches := Match("help")
	matches[0].Name = "changed"
	if command, _ := Lookup("help"); command.Name != "help" {
		t.Fatal("palette caller mutated the registry")
	}
}

func TestHelpIncludesLocalGuidance(t *testing.T) {
	for _, name := range []string{"", "/PLAN", "diff", "theme", "exit"} {
		text, err := Help(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{"Enter", "Ctrl+J", "Esc", "Ctrl+C", "Ctrl+P", "advisory", "not read-only", "safety boundary", "approvals", "//path", "later"} {
			if !strings.Contains(text, required) {
				t.Errorf("Help(%q) missing %q", name, required)
			}
		}
	}
}

func TestHelpAndPaletteFallback(t *testing.T) {
	if len(Match("")) != 13 || len(Match("xyz")) != 0 {
		t.Fatal("unexpected palette fallback")
	}
	help, err := Help("")
	if err != nil || !strings.Contains(help, "/login") || !strings.Contains(help, "/logout") || !strings.Contains(help, "/clear") || strings.Contains(help, "/new") || !strings.Contains(help, "//") {
		t.Fatalf("missing help: %s, %v", help, err)
	}
	for _, command := range All() {
		if !strings.Contains(help, command.Usage) {
			t.Errorf("help omitted %s", command.Name)
		}
	}
	plan, err := Help("plan")
	if err != nil || !strings.Contains(plan, "not read-only") {
		t.Fatal("planning help implies a safety boundary")
	}
	compact, err := Help("compact")
	if err != nil || !strings.Contains(compact, "visible transcript") || !strings.Contains(compact, "model tokens") {
		t.Fatal("compaction help omits behavior or cost")
	}
}

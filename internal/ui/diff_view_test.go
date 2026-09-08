package ui

import (
	"strings"
	"testing"
)

func TestDiffDialogUsesGitStyleColors(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	options := testOptions()
	options.Preferences.NoColor = false
	m := testModel(t, options)
	m.resize(100, 30)
	d := m.newDialog(dialogDiff, "WORKING TREE / all / read-only", strings.Join([]string{
		"diff --git a/example.go b/example.go",
		"index 1111111..2222222 100644",
		"--- a/example.go",
		"+++ b/example.go",
		"@@ -1 +1 @@",
		"-old value",
		"+new value",
		" unchanged",
	}, "\n"))

	rendered := m.dialogBody(d.body, 80, d.kind)
	for _, want := range []string{
		m.color.paint(m.color.red, "--- a/example.go"),
		m.color.paint(m.color.lime, "+++ b/example.go"),
		m.color.paint(m.color.red, "-old value"),
		m.color.paint(m.color.lime, "+new value"),
		m.color.paint(m.color.cyan, "@@ -1 +1 @@"),
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("styled diff missing %q: %q", want, rendered)
		}
	}
	if plain := safeText(rendered); plain != d.body {
		t.Fatalf("diff styling changed content:\n got: %q\nwant: %q", plain, d.body)
	}
}

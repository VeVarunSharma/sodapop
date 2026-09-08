package ui

import (
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/config"
)

func TestThemePalettesHaveCompleteContrastLayers(t *testing.T) {
	themes := []string{"arcade", "graphite", "midnight", "high-contrast", "light"}
	seen := make(map[string]string)
	for _, theme := range themes {
		c := colors(config.Preferences{Theme: theme})
		values := []string{
			c.ink, c.panel, c.raised, c.border, c.text, c.muted,
			c.cyan, c.magenta, c.lime, c.amber, c.red,
		}
		for i, value := range values {
			if value == "" {
				t.Fatalf("%s palette color %d is empty", theme, i)
			}
		}
		key := c.ink + c.panel + c.raised + c.border
		if other, ok := seen[key]; ok {
			t.Fatalf("%s and %s share the same surface palette", theme, other)
		}
		seen[key] = theme
	}
}

func TestGraphiteThemeCanBeSelected(t *testing.T) {
	m, _ := readyModel(t)
	if _, ok := m.selectTheme("graphite"); !ok || m.prefs.Theme != "graphite" {
		t.Fatal("graphite theme was not selected")
	}
}

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
)

func TestPreferencesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "config.json")
	prefs, err := Load(path)
	if err != nil || !reflect.DeepEqual(prefs, DefaultPreferences()) {
		t.Fatalf("missing config: %#v, %v", prefs, err)
	}
	if prefs.Version != 3 || prefs.Personality != "playful" {
		t.Fatalf("fresh defaults: %#v", prefs)
	}
	prefs.Model = "available-model"
	prefs.ModelSettings[prefs.Model] = ModelSettings{ContextTier: "long_context", ReasoningEffort: "high"}
	prefs.ReducedMotion = true
	prefs.Personality = "extra"
	if err := Save(path, prefs); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, prefs) {
		t.Fatalf("round trip: %#v, %v", got, err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	private, privacyErr := securefs.IsPrivateRegularFile(file)
	closeErr := file.Close()
	if privacyErr != nil || closeErr != nil || !private {
		t.Fatalf("expected private preferences: private=%t, err=%v, close=%v", private, privacyErr, closeErr)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".sodapop-config-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files remain: %v, %v", files, err)
	}
}

func TestVersionOnePreferencesMigrateToPlayful(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{
  "version": 1,
  "theme": "graphite",
  "model": "available-model",
  "reduced_motion": true,
  "no_color": true,
  "ascii": true
}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Preferences{
		Version:       3,
		Theme:         "graphite",
		Model:         "available-model",
		ReducedMotion: true,
		NoColor:       true,
		ASCII:         true,
		Personality:   "playful",
		ModelSettings: make(map[string]ModelSettings),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated preferences: %#v, want %#v", got, want)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != contents {
		t.Fatal("migration modified the existing configuration")
	}
}

func TestVersionTwoPreferencesMigrateToEmptyModelSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{"version":2,"theme":"midnight","model":"model-a","reduced_motion":false,"no_color":false,"ascii":false,"personality":"quiet"}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 3 || got.Model != "model-a" || got.Personality != "quiet" || len(got.ModelSettings) != 0 {
		t.Fatalf("version two migration: %#v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != contents {
		t.Fatal("migration modified the existing configuration")
	}
}

func TestPersonalityValuesAreStrict(t *testing.T) {
	for _, personality := range []string{"quiet", "playful", "extra"} {
		prefs := DefaultPreferences()
		prefs.Personality = personality
		if err := validate(prefs); err != nil {
			t.Errorf("valid personality %q rejected: %v", personality, err)
		}
	}
	for _, personality := range []string{"", "Quiet", "loud", " playful"} {
		prefs := DefaultPreferences()
		prefs.Personality = personality
		if err := validate(prefs); err == nil {
			t.Errorf("invalid personality %q accepted", personality)
		}
	}
}

func TestMalformedPreferencesAreNotSilentlyReplaced(t *testing.T) {
	cases := []string{
		"",
		`{`,
		`{"version":4,"theme":"arcade","personality":"playful"}`,
		`{"version":1,"theme":"arcade","unknown":true}`,
		`{"version":1,"theme":"arcade","personality":"quiet"}`,
		`{"version":2,"theme":"arcade","personality":"playful","unknown":true}`,
		`{"version":2,"theme":"arcade","personality":"playful"} {}`,
		`{"version":2,"theme":"\u001b[31m","personality":"playful"}`,
		`{"version":2,"theme":"arcade"}`,
		`{"version":2,"theme":"arcade","personality":""}`,
		`{"version":2,"theme":"arcade","personality":"loud"}`,
		`{"version":3,"theme":"arcade","personality":"playful","model_settings":{"model-a":{"context_tier":"huge"}}}`,
		`{"version":3,"theme":"arcade","personality":"playful","model_settings":{"":{"context_tier":"default"}}}`,
		strings.Repeat(" ", maxConfigBytes+1),
	}
	for _, contents := range cases {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("expected invalid config to be rejected (%d bytes)", len(contents))
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != contents {
			t.Fatal("invalid configuration was modified")
		}
	}
}

func TestAccountHomeDoesNotAcceptPathTraversal(t *testing.T) {
	paths := Paths{StateDir: t.TempDir()}
	first, err := paths.AccountHome("../../other-account")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(paths.StateDir, first)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("account escaped state root: %q, %v", rel, err)
	}
	second, err := paths.AccountHome("another-account")
	if err != nil || second == first {
		t.Fatal("account state was not isolated")
	}
	if _, err := paths.AccountHome(""); err == nil {
		t.Fatal("missing identity accepted")
	}
}

func TestPlatformPaths(t *testing.T) {
	base, home := filepath.Join(t.TempDir(), "config"), t.TempDir()
	configFile := filepath.Join(base, "sodapop", "config.json")
	mac, err := resolvePaths("darwin", base, home, "", "")
	if err != nil || mac.ConfigFile != configFile || mac.StateDir != filepath.Join(base, "sodapop", "state") {
		t.Fatalf("macOS paths: %#v, %v", mac, err)
	}
	linux, err := resolvePaths("linux", base, home, "", "")
	if err != nil || linux.ConfigFile != configFile || linux.StateDir != filepath.Join(home, ".local", "state", "sodapop") {
		t.Fatalf("Linux paths: %#v, %v", linux, err)
	}
	custom := filepath.Join(t.TempDir(), "state")
	linux, err = resolvePaths("linux", base, home, custom, "")
	if err != nil || linux.ConfigFile != configFile || linux.StateDir != filepath.Join(custom, "sodapop") {
		t.Fatalf("custom Linux paths: %#v, %v", linux, err)
	}
	if _, err := resolvePaths("linux", base, home, "relative", ""); err == nil {
		t.Fatal("relative XDG_STATE_HOME accepted")
	}
	localData := filepath.Join(t.TempDir(), "local")
	windows, err := resolvePaths("windows", base, home, "", localData)
	if err != nil || windows.ConfigFile != configFile || windows.StateDir != filepath.Join(localData, "sodapop", "state") {
		t.Fatalf("Windows paths: %#v, %v", windows, err)
	}
	if _, err := resolvePaths("windows", base, home, "", ""); err == nil {
		t.Fatal("missing Windows local application data accepted")
	}
}

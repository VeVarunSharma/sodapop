package skills

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
)

func writeSkill(t *testing.T, root, name, description string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# Instructions\n"
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalInstallIsImmutableAndProjectScoped(t *testing.T) {
	state, source := t.TempDir(), t.TempDir()
	path := writeSkill(t, source, "test-skill", "A test skill")
	managerA, err := New(state, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := managerA.Install(t.Context(), path); err == nil || !strings.Contains(err.Error(), "trusted roots") {
		t.Fatalf("installed untrusted source: %v", err)
	}
	if _, err := managerA.TrustSource(source); err != nil {
		t.Fatal(err)
	}
	installed, err := managerA.Install(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findEntry(installed.Installed, "test-skill")
	if !ok {
		t.Fatal("installed skill missing")
	}
	if _, err := managerA.SetEnabled(entry.Name, true); err != nil {
		t.Fatal(err)
	}
	resolved, active, err := managerA.Resolve()
	if err != nil || len(active) != 1 {
		t.Fatalf("resolve = %#v, %#v, %v", resolved, active, err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := managerA.Resolve(); err != nil {
		t.Fatalf("source mutation changed installed skill: %v", err)
	}
	managerB, err := New(state, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, active, err := managerB.Resolve()
	if err != nil || len(active) != 0 {
		t.Fatalf("project isolation = %#v, %#v, %v", other, active, err)
	}
	for _, skill := range other {
		if skill.Enabled {
			t.Fatalf("other project inherited activation: %#v", other)
		}
	}
}

func TestInstallRejectsUnsafeSkillContents(t *testing.T) {
	for name, prepare := range map[string]func(*testing.T, string) string{
		"missing manifest": func(t *testing.T, source string) string {
			path := filepath.Join(source, "missing")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			return path
		},
		"invalid name": func(t *testing.T, source string) string {
			return writeSkill(t, source, "Bad_Name", "description")
		},
		"symlink": func(t *testing.T, source string) string {
			path := writeSkill(t, source, "linked", "description")
			if err := os.Symlink(filepath.Join(path, "SKILL.md"), filepath.Join(path, "link")); err != nil {
				t.Fatal(err)
			}
			return path
		},
		"oversized file": func(t *testing.T, source string) string {
			path := writeSkill(t, source, "large", "description")
			if err := os.WriteFile(filepath.Join(path, "large.bin"), make([]byte, maxSkillFile+1), 0600); err != nil {
				t.Fatal(err)
			}
			return path
		},
	} {
		t.Run(name, func(t *testing.T) {
			state, source, project := t.TempDir(), t.TempDir(), t.TempDir()
			path := prepare(t, source)
			manager, err := New(state, project)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.TrustSource(source); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Install(context.Background(), path); err == nil {
				t.Fatal("unsafe skill was installed")
			}
		})
	}
}

func TestGitSourcesRequireTrustAndPinRevision(t *testing.T) {
	manager, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	manager.runGit = func(_ context.Context, directory string, args ...string) error {
		calls = append(calls, append([]string{directory}, args...))
		if len(args) > 0 && args[0] == "clone" {
			destination := args[len(args)-1]
			if err := os.MkdirAll(filepath.Join(destination, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
			manifest := "---\nname: git-skill\ndescription: From Git\n---\n"
			if err := os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	manager.gitRevision = func(context.Context, string) (string, error) {
		return strings.Repeat("a", 40), nil
	}
	source := "https://example.com/owner/skill.git#v1"
	if _, err := manager.Install(t.Context(), source); err == nil {
		t.Fatal("installed untrusted Git source")
	}
	if _, err := manager.TrustSource("https://example.com/owner/skill.git"); err != nil {
		t.Fatal(err)
	}
	installed, err := manager.Install(t.Context(), source)
	entry, ok := findEntry(installed.Installed, "git-skill")
	if err != nil || !ok || entry.Source.Revision != strings.Repeat("a", 40) {
		t.Fatalf("Git install = %#v, %v", installed, err)
	}
	if len(calls) != 3 || calls[0][1] != "clone" || calls[1][1] != "fetch" || calls[2][1] != "checkout" {
		t.Fatalf("Git revision was not fetched and detached: %#v", calls)
	}
}

func TestActiveSkillMustBeDisabledBeforeReplacement(t *testing.T) {
	state, source, project := t.TempDir(), t.TempDir(), t.TempDir()
	path := writeSkill(t, source, "replaceable", "First version")
	manager, err := New(state, project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.TrustSource(source); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetEnabled("replaceable", true); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, source, "replaceable", "Second version")
	if _, err := manager.Install(t.Context(), path); err == nil || !strings.Contains(err.Error(), "disable") {
		t.Fatalf("active replacement was not rejected: %v", err)
	}
}

func TestApplyLifecycleRetainsRemovedVersions(t *testing.T) {
	state, source, project := t.TempDir(), t.TempDir(), t.TempDir()
	path := writeSkill(t, source, "lifecycle", "Lifecycle fixture")
	manager, err := New(state, project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(t.Context(), Action{Operation: "unknown"}); err == nil {
		t.Fatal("unknown action was accepted")
	}
	for _, action := range []Action{
		{Operation: "load"},
		{Operation: "trust", Value: source},
		{Operation: "install", Value: path},
		{Operation: "enable", Value: "lifecycle"},
		{Operation: "disable", Value: "lifecycle"},
		{Operation: "remove", Value: "lifecycle"},
	} {
		if _, err := manager.Apply(t.Context(), action); err != nil {
			t.Fatalf("%s failed: %v", action.Operation, err)
		}
	}
	loaded, err := manager.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findEntry(loaded.Installed, "lifecycle"); ok {
		t.Fatal("removed skill remained in the catalog")
	}
	resolved, active, err := manager.Resolve()
	if err != nil || len(active) != 0 {
		t.Fatalf("resolve after remove = %#v, %#v, %v", resolved, active, err)
	}
	retained := false
	for _, skill := range resolved {
		retained = retained || skill.Name == "lifecycle"
	}
	if !retained {
		t.Fatal("removed skill bytes were not retained for saved sessions")
	}
	if _, err := manager.Remove("missing"); err == nil {
		t.Fatal("unknown skill removal was accepted")
	}
}

func TestSkillStateIsStrictPrivateAndTamperEvident(t *testing.T) {
	state, source, project := t.TempDir(), t.TempDir(), t.TempDir()
	path := writeSkill(t, source, "private-skill", "Private fixture")
	manager, err := New(state, project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.TrustSource(source); err != nil {
		t.Fatal(err)
	}
	installed, err := manager.Install(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findEntry(installed.Installed, "private-skill")
	if !ok {
		t.Fatal("installed skill missing")
	}
	for _, path := range []string{manager.catalogPath(), manager.trustPath()} {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		private, privacyErr := securefs.IsPrivateRegularFile(file)
		closeErr := file.Close()
		if privacyErr != nil || closeErr != nil || !private {
			t.Fatalf("state file %q is not private: %t, %v, %v", path, private, privacyErr, closeErr)
		}
	}
	manifest := filepath.Join(manager.storePath(), entry.Digest, entry.Name, "SKILL.md")
	if err := os.WriteFile(manifest, []byte("---\nname: private-skill\ndescription: tampered\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Resolve(); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered skill was accepted: %v", err)
	}

	corrupt := []byte(`{"version":1,"entries":[],"unknown":true}`)
	if err := os.WriteFile(manager.catalogPath(), corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Load(); err == nil {
		t.Fatal("skill state with unknown fields was accepted")
	}
	got, err := os.ReadFile(manager.catalogPath())
	if err != nil || !reflect.DeepEqual(got, corrupt) {
		t.Fatal("corrupt skill state was modified")
	}
}

func TestGitSourceNormalizationAndManifestParsing(t *testing.T) {
	for _, source := range []string{
		"http://example.com/skill.git",
		"ssh://git@example.com/skill.git",
		"https://user@example.com/skill.git",
		"https://example.com/skill.git?credential=value",
	} {
		if _, ok, err := normalizeGitSource(source); !ok || err == nil {
			t.Errorf("accepted unsafe Git source %q: ok=%t err=%v", source, ok, err)
		}
	}
	normalized, ok, err := normalizeGitSource("HTTPS://Example.COM/owner/skill.git/#main")
	if err != nil || !ok || normalized != "https://example.com/owner/skill.git" {
		t.Fatalf("normalized source = %q, %t, %v", normalized, ok, err)
	}

	name, description, err := parseManifest([]byte("---\nname: quoted-skill\ndescription: \"Quoted description\"\n---\n"))
	if err != nil || name != "quoted-skill" || description != "Quoted description" {
		t.Fatalf("quoted manifest = %q, %q, %v", name, description, err)
	}
	for _, manifest := range []string{
		"name: missing-frontmatter",
		"---\nname: missing-end\ndescription: nope\n",
		"---\nname: bad_name\ndescription: nope\n---\n",
		"---\nname: valid-name\ndescription: \"bad\\nvalue\"\n---\n",
	} {
		if _, _, err := parseManifest([]byte(manifest)); err == nil {
			t.Fatalf("accepted malformed manifest %q", manifest)
		}
	}
}

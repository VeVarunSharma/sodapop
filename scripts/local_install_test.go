package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalInstallKeepsPreviousCommandOnFailure(t *testing.T) {
	for _, failedCommand := range []string{"install", "mv"} {
		t.Run(failedCommand, func(t *testing.T) {
			root := scriptFixture(t)
			writeFixtureFile(t, root, "bin/sodapop", fixtureExecutable, 0755)
			writeFixtureFile(t, root, "home/.local/bin/sodapop", "previous command\n", 0755)
			writeFixtureFile(t, root, "fake-bin/"+failedCommand, "#!/usr/bin/env bash\nexit 23\n", 0755)
			output, err := runScriptFixture(t, root, []string{"bash", "scripts/install.sh"})
			if err == nil || strings.Contains(output, "Installed ") {
				t.Fatalf("failed %s reported success: %s, %v", failedCommand, output, err)
			}
			assertFixtureLines(t, root, "home/.local/bin/sodapop", []string{"previous command"})
			leftovers, err := filepath.Glob(filepath.Join(root, "home", ".local", "bin", ".sodapop-install.*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("temporary installation files remain: %v, %v", leftovers, err)
			}
		})
	}
}

func TestLocalInstallRefusesOtherInstallersLinks(t *testing.T) {
	root := scriptFixture(t)
	writeFixtureFile(t, root, "bin/sodapop", fixtureExecutable, 0755)
	writeFixtureFile(t, root, "another-manager/sodapop", "other installer command\n", 0755)
	directory := filepath.Join(root, "home", ".local", "bin")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "another-manager", "sodapop")
	link := filepath.Join(directory, "sodapop")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	output, err := runScriptFixture(t, root, []string{"bash", "scripts/install.sh"})
	if err == nil || !strings.Contains(output, "Refusing to replace") {
		t.Fatalf("installer accepted another installer's link: %s, %v", output, err)
	}
	got, err := os.Readlink(link)
	if err != nil || got != target {
		t.Fatalf("installer modified the existing link: %s, %v", got, err)
	}
	assertFixtureLines(t, root, "another-manager/sodapop", []string{"other installer command"})
}

func TestLocalInstallRefusesDirectoryAtCommandPath(t *testing.T) {
	root := scriptFixture(t)
	writeFixtureFile(t, root, "bin/sodapop", fixtureExecutable, 0755)
	writeFixtureFile(t, root, "home/.local/bin/sodapop/keep", "unrelated\n", 0600)
	output, err := runScriptFixture(t, root, []string{"bash", "scripts/install.sh"})
	if err == nil || !strings.Contains(output, "Refusing to replace") {
		t.Fatalf("installer accepted a directory: %s, %v", output, err)
	}
	assertFixtureLines(t, root, "home/.local/bin/sodapop/keep", []string{"unrelated"})
}

func TestLocalInstallCanUpgradeAndReinstall(t *testing.T) {
	root := scriptFixture(t)
	writeFixtureFile(t, root, "home/.local/bin/sodapop", "previous command\n", 0755)
	writeFixtureFile(t, root, "bin/sodapop", fixtureExecutable, 0755)
	for i := 0; i < 2; i++ {
		output, err := runScriptFixture(t, root, []string{"bash", "scripts/install.sh"})
		if err != nil || !strings.Contains(output, "Installed ") {
			t.Fatalf("installation %d failed: %s, %v", i, output, err)
		}
		data, err := os.ReadFile(filepath.Join(root, "home", ".local", "bin", "sodapop"))
		if err != nil || string(data) != fixtureExecutable {
			t.Fatalf("installation %d did not replace the command: %q, %v", i, data, err)
		}
	}
}

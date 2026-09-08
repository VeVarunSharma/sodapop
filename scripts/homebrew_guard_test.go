package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomebrewEarlyFailureNeverCleansUnownedInstallations(t *testing.T) {
	for _, reason := range []string{"no consent", "existing installation", "invalid manifest", "existing tap"} {
		t.Run(reason, func(t *testing.T) {
			root := homebrewFixture(t)
			releases, manifest, _ := writeHomebrewReleaseInputs(t, root, "releases", "1.2.3", "1.0.13", "1.0.83")
			prefix := filepath.Join(root, "fake-prefix")
			state := filepath.Join(root, "fake-brew-state")
			log := filepath.Join(root, "brew.log")
			writeFakeHomebrew(t, root, prefix)
			if err := os.MkdirAll(state, 0700); err != nil {
				t.Fatal(err)
			}
			if reason == "existing tap" {
				homebrewWriteFixtureFile(t, root, "fake-brew-state/tapped-repo", filepath.Join(root, "other-tap"), 0600)
				homebrewWriteFixtureFile(t, root, "other-tap/keep", "user tap", 0600)
			} else {
				homebrewWriteFixtureFile(t, root, "fake-brew-state/version", "0.9.0\n", 0600)
				homebrewWriteFixtureFile(t, root, "fake-prefix/bin/sodapop", "user installation", 0755)
			}
			consent := "1"
			if reason == "no consent" {
				consent = "0"
			}
			if reason == "invalid manifest" {
				manifest = filepath.Join(root, "missing-manifest")
			}
			_, err := homebrewRunScriptFixture(t, root,
				[]string{"bash", "scripts/test-homebrew-install.sh", "--release-dir", releases, "--manifest", manifest},
				"FAKE_BREW_PREFIX="+prefix, "FAKE_BREW_STATE="+state, "FAKE_BREW_LOG="+log,
				"FAKE_BREW_PLATFORM=darwin-arm64", "SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX="+consent)
			if err == nil {
				t.Fatal("unsafe precondition did not stop the installation test")
			}
			if data, err := os.ReadFile(log); err == nil {
				for _, operation := range []string{"uninstall ", "untap ", "install --", "tap --"} {
					if strings.Contains(string(data), operation) {
						t.Fatalf("early failure mutated unowned Homebrew state: %s", data)
					}
				}
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			expectedPath, expected := filepath.Join(prefix, "bin", "sodapop"), "user installation"
			if reason == "existing tap" {
				expectedPath, expected = filepath.Join(root, "other-tap", "keep"), "user tap"
			}
			data, err := os.ReadFile(expectedPath)
			if err != nil || string(data) != expected {
				t.Fatalf("user's existing install/tap was changed: %q, %v", data, err)
			}
		})
	}
}

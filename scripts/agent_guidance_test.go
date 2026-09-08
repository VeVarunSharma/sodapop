package main

import (
	"bytes"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func discoverAgentGuidance(root string, walkDir func(string, fs.WalkDirFunc) error) ([]string, error) {
	var paths []string
	err := walkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative == "." {
				return nil
			}
			switch entry.Name() {
			case ".git", "bin", "dist", "node_modules":
				return filepath.SkipDir
			}
			switch relative {
			case "site/.astro", "site/.generated", "site/src/content/docs", "site/public/assets",
				"site/test-results", "site/playwright-report":
				return filepath.SkipDir
			}
		} else if entry.Name() == "AGENTS.md" {
			paths = append(paths, relative)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func validateAgentGuidanceIndex(index []byte, relative string) error {
	if !bytes.Contains(index, []byte("]("+relative+")")) {
		return errors.New("add this scoped guide to the root AGENTS.md map")
	}
	return nil
}

func TestAgentGuidance(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}

	const bridge = ".github/copilot-instructions.md"
	guides, err := discoverAgentGuidance(root, filepath.WalkDir)
	if err != nil {
		t.Fatal(err)
	}
	paths := append([]string{bridge, "docs/agent-guidance.md"}, guides...)

	// Guides use inline Markdown links; external URLs are not fetched by tests.
	links := regexp.MustCompile(`\[[^\]\n]+\]\(([^)\n]+)\)`)
	for _, relative := range paths {
		t.Run(relative, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(relative))
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !info.Mode().IsRegular() {
				t.Fatal("guidance must be a regular file, not a symlink")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(bytes.TrimSpace(data)) == 0 {
				t.Fatal("guidance must not be empty")
			}

			var maxLines, maxBytes int
			switch {
			case relative == "AGENTS.md":
				maxLines, maxBytes = 100, 8*1024
			case relative == bridge:
				maxLines, maxBytes = 12, 1024
				if !bytes.Contains(data, []byte("](../AGENTS.md)")) {
					t.Error("Copilot instructions must point to the root AGENTS.md")
				}
			case filepath.Base(path) == "AGENTS.md":
				maxLines, maxBytes = 60, 4*1024
				if err := validateAgentGuidanceIndex(index, relative); err != nil {
					t.Error(err)
				}
			}
			if maxLines > 0 {
				lines := bytes.Count(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) + 1
				if lines > maxLines || len(data) > maxBytes {
					t.Errorf("guide is %d lines / %d bytes; limit is %d / %d: move detail to linked docs instead",
						lines, len(data), maxLines, maxBytes)
				}
			}

			for _, match := range links.FindAllStringSubmatch(string(data), -1) {
				link, err := url.Parse(match[1])
				if err != nil {
					t.Errorf("invalid link %q: %v", match[1], err)
					continue
				}
				if link.IsAbs() || link.Host != "" || link.Path == "" {
					continue
				}
				if filepath.IsAbs(link.Path) {
					t.Errorf("use a repository-relative link instead of %q", match[1])
					continue
				}
				target := filepath.Join(filepath.Dir(path), filepath.FromSlash(link.Path))
				targetRelative, err := filepath.Rel(root, target)
				if err != nil {
					t.Errorf("resolve link %q: %v", match[1], err)
					continue
				}
				if targetRelative == ".." || strings.HasPrefix(targetRelative, ".."+string(filepath.Separator)) {
					t.Errorf("link %q leaves the repository", match[1])
					continue
				}
				if _, err := os.Stat(target); err != nil {
					t.Errorf("broken local link %q: %v", match[1], err)
				}
			}
		})
	}
}

func TestAgentGuidanceDiscoveryExcludesDependenciesAndGeneratedTrees(t *testing.T) {
	root := t.TempDir()
	owned := []string{
		"AGENTS.md",
		".github/AGENTS.md",
		".astro/AGENTS.md",
		".generated/AGENTS.md",
		"docs/AGENTS.md",
		"site/AGENTS.md",
		"site/node_modules-guide/AGENTS.md",
		"site/public/AGENTS.md",
		"site/src/AGENTS.md",
		"site/src/content/AGENTS.md",
		"site/src/components/.generated/AGENTS.md",
		"site/src/components/test-results/AGENTS.md",
	}
	excluded := []string{
		".git/AGENTS.md",
		"bin/AGENTS.md",
		"dist/AGENTS.md",
		"node_modules/@vendor/dependency/AGENTS.md",
		"site/node_modules/dependency/AGENTS.md",
		"site/node_modules/dependency/node_modules/transitive/AGENTS.md",
		"site/bin/AGENTS.md",
		"site/dist/AGENTS.md",
		"site/.astro/AGENTS.md",
		"site/.generated/content/AGENTS.md",
		"site/src/content/docs/AGENTS.md",
		"site/public/assets/AGENTS.md",
		"site/test-results/AGENTS.md",
		"site/playwright-report/AGENTS.md",
	}
	for _, relative := range owned {
		writeFixtureFile(t, root, relative, "# Owned instructions\n", 0600)
	}
	for _, relative := range excluded {
		writeFixtureFile(t, root, relative, "# Unindexed generated instructions\n[Missing](missing)\n", 0600)
	}

	paths, err := discoverAgentGuidance(root, filepath.WalkDir)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(owned)
	if !slices.Equal(paths, owned) {
		t.Fatalf("discovered guidance = %v, want only owned guides %v", paths, owned)
	}
}

func TestAgentGuidanceDiscoveryRetainsUnindexedOwnedGuides(t *testing.T) {
	root := t.TempDir()
	index := []byte("# Repository instructions\n[Website](site/AGENTS.md)\n")
	writeFixtureFile(t, root, "AGENTS.md", string(index), 0600)
	writeFixtureFile(t, root, "site/AGENTS.md", "# Website instructions\n", 0600)
	writeFixtureFile(t, root, "site/src/AGENTS.md", "", 0600)
	writeFixtureFile(t, root, "site/unindexed/AGENTS.md", "# Unindexed owned instructions\n", 0600)

	paths, err := discoverAgentGuidance(root, filepath.WalkDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"AGENTS.md", "site/AGENTS.md", "site/src/AGENTS.md", "site/unindexed/AGENTS.md"}
	if !slices.Equal(paths, want) {
		t.Fatalf("discovery hid an unindexed or empty owned guide: %v", paths)
	}
	if err := validateAgentGuidanceIndex(index, "site/AGENTS.md"); err != nil {
		t.Fatalf("indexed website guide rejected: %v", err)
	}
	for _, relative := range want[2:] {
		if err := validateAgentGuidanceIndex(index, relative); err == nil ||
			!strings.Contains(err.Error(), "root AGENTS.md map") {
			t.Fatalf("unindexed owned guide %s did not fail: %v", relative, err)
		}
	}
}

func TestAgentGuidanceDiscoveryPropagatesFilesystemErrors(t *testing.T) {
	t.Run("missing root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "missing")
		paths, err := discoverAgentGuidance(root, filepath.WalkDir)
		if !errors.Is(err, fs.ErrNotExist) || paths != nil {
			t.Fatalf("missing root returned partial guidance or lost its error: %v, %v", paths, err)
		}
	})
	for _, relative := range []string{"site/src", "site/node_modules"} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, "AGENTS.md", "# Repository instructions\n", 0600)
			writeFixtureFile(t, root, relative+"/AGENTS.md", "# Instructions\n", 0600)
			failedPath := filepath.Join(root, filepath.FromSlash(relative))
			failure := &fs.PathError{Op: "readdir", Path: failedPath, Err: fs.ErrPermission}
			walkDir := func(root string, visit fs.WalkDirFunc) error {
				return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
					if path == failedPath && walkErr == nil {
						return visit(path, nil, failure)
					}
					return visit(path, entry, walkErr)
				})
			}
			paths, err := discoverAgentGuidance(root, walkDir)
			if !errors.Is(err, failure) || paths != nil {
				t.Fatalf("walk failure returned partial guidance or lost its error: %v, %v", paths, err)
			}
		})
	}
}

package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func gitFixture(t *testing.T) (string, *Service) {
	t.Helper()
	isolateGit(t)
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	service, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, service
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	options := []string{"--no-pager", "-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgSign=false"}
	if len(args) != 0 && args[0] == "commit" {
		options = append(options, "-c", "user.name=Sodapop fixture", "-c", "user.email=fixture@example.invalid")
	}
	cmd := exec.CommandContext(t.Context(), "git", append(options, args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnvironment()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture Git %v: %s, %v", args, out, err)
	}
	return string(out)
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func executableSentinel(t *testing.T) (helper, marker string) {
	t.Helper()
	marker = filepath.Join(t.TempDir(), "invoked")
	helper = filepath.Join(t.TempDir(), "helper")
	t.Setenv("SODAPOP_GIT_HELPER_MARKER", marker)
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf called > \"$SODAPOP_GIT_HELPER_MARKER\"\ncat\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return helper, marker
}

func TestNonGitAndMissingExecutable(t *testing.T) {
	isolateGit(t)
	service, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil || status.IsRepository {
		t.Fatalf("non-Git status: %#v, %v", status, err)
	}
	diff, err := service.Diff(context.Background(), "")
	if err != nil || diff.IsRepository || !strings.Contains(diff.Text, "Not a Git") {
		t.Fatalf("non-Git diff: %#v, %v", diff, err)
	}
	service.git = filepath.Join(t.TempDir(), "missing-git")
	if _, err := service.Status(context.Background()); err == nil {
		t.Fatal("missing Git was silently treated as non-Git")
	}
	if _, err := service.Diff(context.Background(), "all"); err == nil {
		t.Fatal("missing Git was silently treated as an empty diff")
	}
}

func TestUnbornStagedUnstagedAndUntracked(t *testing.T) {
	dir, service := gitFixture(t)
	file := filepath.Join(dir, "example.txt")
	writeFixture(t, file, "staged\n")
	gitRun(t, dir, "add", "example.txt")
	writeFixture(t, file, "unstaged\n")
	writeFixture(t, filepath.Join(dir, "odd\nname.txt"), "untracked preview\n")
	status, err := service.Status(context.Background())
	if err != nil || status.Branch != "main" || len(status.Entries) != 2 {
		t.Fatalf("unborn status: %#v, %v", status, err)
	}
	before := gitRun(t, dir, "status", "--porcelain=v1", "-z")
	diff, err := service.Diff(context.Background(), "all")
	if err != nil || !diff.IsRepository || diff.Truncated {
		t.Fatalf("unborn diff: %#v, %v", diff, err)
	}
	for _, text := range []string{"STAGED", "UNSTAGED", "UNTRACKED", "staged", "unstaged", "untracked preview", `odd\nname.txt`} {
		if !strings.Contains(diff.Text, text) {
			t.Errorf("diff missing %q: %s", text, diff.Text)
		}
	}
	if after := gitRun(t, dir, "status", "--porcelain=v1", "-z"); after != before {
		t.Fatal("inspection mutated the worktree")
	}
}

func TestRenamesAndDetachedHead(t *testing.T) {
	dir, service := gitFixture(t)
	writeFixture(t, filepath.Join(dir, "old name"), "initial\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	gitRun(t, dir, "mv", "old name", "new name")
	status, err := service.Status(context.Background())
	if err != nil || len(status.Entries) != 1 || status.Entries[0].OriginalPath != "old name" || status.Entries[0].Path != "new name" {
		t.Fatalf("rename parsing: %#v, %v", status, err)
	}
	gitRun(t, dir, "commit", "-m", "rename")
	gitRun(t, dir, "checkout", "--detach")
	status, err = service.Status(context.Background())
	if err != nil || !strings.HasPrefix(status.Branch, "detached@") {
		t.Fatalf("detached status: %#v, %v", status, err)
	}
}

func TestUntrackedSymlinksAndBinaryFiles(t *testing.T) {
	dir, service := gitFixture(t)
	outside := filepath.Join(t.TempDir(), "outside")
	writeFixture(t, outside, "DO NOT PREVIEW THIS")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "binary"), "before\x00after")
	diff, err := service.Diff(context.Background(), "all")
	if err != nil || strings.Contains(diff.Text, "DO NOT PREVIEW") || !strings.Contains(diff.Text, "binary file") || !strings.Contains(diff.Text, "contents not followed") {
		t.Fatalf("unsafe preview: %s, %v", diff.Text, err)
	}
}

func TestExternalHelpersAreNotExecuted(t *testing.T) {
	dir, service := gitFixture(t)
	writeFixture(t, filepath.Join(dir, "example.txt"), "before\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	helper, marker := executableSentinel(t)
	gitRun(t, dir, "config", "core.fsmonitor", helper)
	gitRun(t, dir, "config", "core.pager", helper)
	gitRun(t, dir, "config", "pager.diff", helper)
	gitRun(t, dir, "config", "pager.status", helper)
	gitRun(t, dir, "config", "diff.external", helper)
	gitRun(t, dir, "config", "diff.audit.command", helper)
	gitRun(t, dir, "config", "diff.audit.textconv", helper)
	gitRun(t, dir, "config", "filter.audit.clean", helper)
	gitRun(t, dir, "config", "filter.audit.smudge", helper)
	gitRun(t, dir, "config", "filter.audit.process", helper)
	gitRun(t, dir, "config", "filter.audit.required", "true")
	t.Setenv("GIT_EXTERNAL_DIFF", helper)
	t.Setenv("GIT_PAGER", helper)
	t.Setenv("PAGER", helper)
	writeFixture(t, filepath.Join(dir, ".gitattributes"), "*.txt filter=audit diff=audit\n")
	writeFixture(t, filepath.Join(dir, "example.txt"), "after\n")
	if _, err := service.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Diff(context.Background(), "all"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a repository-configured executable helper was invoked")
	}
}

func TestLargeOutputIsExplicitlyTruncated(t *testing.T) {
	dir, service := gitFixture(t)
	writeFixture(t, filepath.Join(dir, "large.txt"), strings.Repeat("a", maxOutput+100))
	diff, err := service.Diff(context.Background(), "all")
	if err != nil || !diff.Truncated || !strings.Contains(diff.Text, "Output truncated") || len(diff.Text) > maxOutput+256 {
		t.Fatalf("large output: length=%d, truncated=%v, error=%v", len(diff.Text), diff.Truncated, err)
	}
}

func TestStatusParserRejectsPartialRecords(t *testing.T) {
	for _, raw := range []string{"?? incomplete", "R  new\x00", "R  new\x00\x00", "bad\x00"} {
		if _, err := parseStatus(raw); err == nil {
			t.Errorf("accepted malformed status %q", raw)
		}
	}
}

func TestNewCanonicalDirectory(t *testing.T) {
	dir := t.TempDir()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	service, err := New(link)
	if err != nil || service.directory != canonical {
		t.Fatalf("canonical directory: %#v, %v; want %q", service, err, canonical)
	}
	file := filepath.Join(dir, "file")
	writeFixture(t, file, "not a directory")
	for _, path := range []string{file, filepath.Join(dir, "missing")} {
		if _, err := New(path); err == nil {
			t.Errorf("accepted non-directory %q", path)
		}
	}
}

func TestCleanAndStagedUnstagedSameFile(t *testing.T) {
	dir, service := gitFixture(t)
	status, err := service.Status(t.Context())
	if err != nil || !status.IsRepository || status.Branch != "main" || len(status.Entries) != 0 {
		t.Fatalf("empty unborn repository: %#v, %v", status, err)
	}
	file := filepath.Join(dir, "both.txt")
	writeFixture(t, file, "original-value\n")
	gitRun(t, dir, "add", "both.txt")
	gitRun(t, dir, "commit", "-m", "fixture")
	status, err = service.Status(t.Context())
	if err != nil || !status.IsRepository || len(status.Entries) != 0 {
		t.Fatalf("clean repository: %#v, %v", status, err)
	}
	clean, err := service.Diff(t.Context(), "")
	if err != nil || clean.Truncated || strings.Count(clean.Text, "(none)") != 3 {
		t.Fatalf("clean diff: %#v, %v", clean, err)
	}

	writeFixture(t, file, "staged-value\n")
	gitRun(t, dir, "add", "both.txt")
	writeFixture(t, file, "unstaged-value\n")
	writeFixture(t, filepath.Join(dir, "untracked.txt"), "untracked-value\n")
	indexPath := filepath.Join(dir, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	status, err = service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]Entry)
	for _, entry := range status.Entries {
		entries[entry.Path] = entry
	}
	if entries["both.txt"].Code != "MM" || entries["untracked.txt"].Code != "??" {
		t.Fatalf("staged and unstaged changes were not distinguished: %#v", entries)
	}
	for _, mode := range []string{"all", "staged", "unstaged", " STAGED "} {
		diff, err := service.Diff(t.Context(), mode)
		if err != nil || !diff.IsRepository || diff.Truncated {
			t.Fatalf("%s diff: %#v, %v", mode, diff, err)
		}
		if !strings.Contains(diff.Text, "ENTIRE WORKING TREE") || !strings.Contains(diff.Text, "not exclusively Sodapop") {
			t.Errorf("diff attribution is misleading: %s", diff.Text)
		}
		switch strings.TrimSpace(strings.ToLower(mode)) {
		case "staged":
			if !strings.Contains(diff.Text, "+staged-value") || strings.Contains(diff.Text, "+unstaged-value") || strings.Contains(diff.Text, "\nUNTRACKED") {
				t.Errorf("staged diff mixed scopes: %s", diff.Text)
			}
		case "unstaged":
			if !strings.Contains(diff.Text, "+unstaged-value") || strings.Contains(diff.Text, "-original-value") || strings.Contains(diff.Text, "\nUNTRACKED") {
				t.Errorf("unstaged diff mixed scopes: %s", diff.Text)
			}
		case "all":
			for _, expected := range []string{"\nSTAGED\n", "\nUNSTAGED\n", "\nUNTRACKED\n", "+staged-value", "+unstaged-value", "untracked-value"} {
				if !strings.Contains(diff.Text, expected) {
					t.Errorf("all diff missing %q", expected)
				}
			}
		}
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(indexBefore, indexAfter) || !indexInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("inspection refreshed or modified the Git index")
	}
	content, err := os.ReadFile(file)
	if err != nil || string(content) != "unstaged-value\n" {
		t.Fatalf("inspection changed user content: %q, %v", content, err)
	}
	if _, err := service.Diff(t.Context(), "other"); err == nil {
		t.Fatal("accepted an unsupported diff mode")
	}
}

func TestRenameDeleteAndUnusualPaths(t *testing.T) {
	dir, service := gitFixture(t)
	oldName, newName := "old\n\tname.txt", "new\nname -> \"quoted\".txt"
	for _, name := range []string{oldName, "staged-delete", "unstaged-delete"} {
		writeFixture(t, filepath.Join(dir, name), name+"\n")
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	gitRun(t, dir, "config", "status.renames", "false")
	if err := os.Rename(filepath.Join(dir, oldName), filepath.Join(dir, newName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "staged-delete")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	if err := os.Remove(filepath.Join(dir, "unstaged-delete")); err != nil {
		t.Fatal(err)
	}
	untracked := "--option-like\npath.txt"
	writeFixture(t, filepath.Join(dir, untracked), "literal filename\n")
	status, err := service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]Entry)
	for _, entry := range status.Entries {
		entries[entry.Path] = entry
	}
	if entries[newName].OriginalPath != oldName || entries[newName].Code != "R " {
		t.Errorf("rename paths were not preserved exactly: %#v", entries[newName])
	}
	if entries["staged-delete"].Code != "D " || entries["unstaged-delete"].Code != " D" || entries[untracked].Code != "??" {
		t.Errorf("deletion/untracked status was lost: %#v", entries)
	}
	diff, err := service.Diff(t.Context(), "all")
	if err != nil || !strings.Contains(diff.Text, "literal filename") || !strings.Contains(diff.Text, `--option-like\npath.txt`) {
		t.Fatalf("unusual filename diff: %s, %v", diff.Text, err)
	}
}

func TestSubdirectoryDiffIncludesEntireRepository(t *testing.T) {
	dir, rootService := gitFixture(t)
	subdir := filepath.Join(dir, "nested")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "outside.txt"), "before\n")
	writeFixture(t, filepath.Join(subdir, "inside.txt"), "before\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	gitRun(t, dir, "config", "diff.relative", "true")
	writeFixture(t, filepath.Join(dir, "outside.txt"), "root modification\n")
	writeFixture(t, filepath.Join(subdir, "inside.txt"), "nested modification\n")
	writeFixture(t, filepath.Join(dir, "untracked.txt"), "root untracked preview\n")
	service, err := New(subdir)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(t.Context())
	if err != nil || status.Root != rootService.directory || len(status.Entries) != 3 {
		t.Fatalf("nested status: %#v, %v", status, err)
	}
	diff, err := service.Diff(t.Context(), "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"root modification", "nested modification", "root untracked preview"} {
		if !strings.Contains(diff.Text, text) {
			t.Errorf("nested diff omitted %q: %s", text, diff.Text)
		}
	}
}

func TestInvalidRepositoriesAreErrors(t *testing.T) {
	isolateGit(t)
	for _, kind := range []string{"empty-marker", "invalid-gitfile", "corrupt-index"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			switch kind {
			case "empty-marker":
				if err := os.Mkdir(filepath.Join(dir, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
			case "invalid-gitfile":
				writeFixture(t, filepath.Join(dir, ".git"), "not a gitfile\n")
			case "corrupt-index":
				gitRun(t, dir, "init", "-b", "main")
				writeFixture(t, filepath.Join(dir, ".git", "index"), "invalid index")
			}
			service, err := New(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Status(t.Context()); err == nil {
				t.Fatal("invalid repository was misreported as clean or non-Git")
			}
			if _, err := service.Diff(t.Context(), "all"); err == nil {
				t.Fatal("invalid repository was misreported as an empty diff")
			}
		})
	}
}

func TestUnreadableRepositoryIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows access control is not represented by chmod mode bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir, service := gitFixture(t)
	marker := filepath.Join(dir, ".git")
	if err := os.Chmod(marker, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(marker, 0700); err != nil {
			t.Error(err)
		}
	})
	if _, err := service.Status(t.Context()); err == nil {
		t.Fatal("unreadable repository was treated as non-Git")
	}
	if _, err := service.Diff(t.Context(), "all"); err == nil {
		t.Fatal("unreadable repository was treated as an empty diff")
	}
}

func TestMissingGitOnPathAndCancellation(t *testing.T) {
	_, service := gitFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Status(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("status cancellation was lost: %v", err)
	}
	if _, err := service.Diff(ctx, "all"); !errors.Is(err, context.Canceled) {
		t.Fatalf("diff cancellation was lost: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := service.Status(t.Context()); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("missing Git should surface its executable error: %v", err)
	}
	if _, err := service.Diff(t.Context(), "all"); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("missing Git should not become non-Git: %v", err)
	}
}

func TestBinaryTrackedAndUntracked(t *testing.T) {
	dir, service := gitFixture(t)
	binary := filepath.Join(dir, "tracked.bin")
	writeFixture(t, binary, "\x00before")
	gitRun(t, dir, "add", "tracked.bin")
	gitRun(t, dir, "commit", "-m", "fixture")
	writeFixture(t, binary, "\x00after")
	writeFixture(t, filepath.Join(dir, "invalid-utf8.bin"), "\xff\xfe\x80")
	diff, err := service.Diff(t.Context(), "all")
	if err != nil || !strings.Contains(diff.Text, "Binary files") || !strings.Contains(diff.Text, "binary file;") || strings.ContainsRune(diff.Text, 0) || !utf8.ValidString(diff.Text) {
		t.Fatalf("binary diff is not informative text: %q, %v", diff.Text, err)
	}
}

func TestLimitedBufferCannotBypassCapThroughCopy(t *testing.T) {
	buffer := &limitedBuffer{limit: 17}
	source := io.LimitReader(strings.NewReader(strings.Repeat("x", 10000)), 10000)
	n, err := io.Copy(buffer, source)
	if err != nil || n != 10000 || buffer.Len() != 17 || !buffer.truncated {
		t.Fatalf("copy bypassed output cap: n=%d len=%d truncated=%v err=%v", n, buffer.Len(), buffer.truncated, err)
	}
}

func TestLargeGitPatchIsBounded(t *testing.T) {
	dir, service := gitFixture(t)
	file := filepath.Join(dir, "tracked.txt")
	writeFixture(t, file, "before\n")
	gitRun(t, dir, "add", "tracked.txt")
	gitRun(t, dir, "commit", "-m", "fixture")
	writeFixture(t, file, strings.Repeat("large patch line\n", maxOutput/8))
	output, truncated, err := service.run(t.Context(), nil, "diff", "--no-ext-diff", "--no-textconv", "--color=never")
	if err != nil || !truncated || len(output) != maxOutput {
		t.Fatalf("Git capture: len=%d truncated=%v err=%v", len(output), truncated, err)
	}
	diff, err := service.Diff(t.Context(), "unstaged")
	if err != nil || !diff.Truncated || len(diff.Text) > maxOutput+256 || !strings.Contains(diff.Text, "Output truncated") {
		t.Fatalf("large tracked diff: len=%d truncated=%v err=%v", len(diff.Text), diff.Truncated, err)
	}
}

func TestPreviewCapKeepsLaterFilesAndUTF8(t *testing.T) {
	dir, service := gitFixture(t)
	content := strings.Repeat("a", maxUntrackedPreview-1) + strings.Repeat("\u754c", 20)
	writeFixture(t, filepath.Join(dir, "a-large.txt"), content)
	writeFixture(t, filepath.Join(dir, "z-small.txt"), "later file is still shown\n")
	diff, err := service.Diff(t.Context(), "all")
	if err != nil || !diff.Truncated || !strings.Contains(diff.Text, "later file is still shown") || !strings.Contains(diff.Text, "File preview truncated") || strings.Contains(diff.Text, "binary file") || !utf8.ValidString(diff.Text) {
		t.Fatalf("per-file preview cap: len=%d truncated=%v err=%v", len(diff.Text), diff.Truncated, err)
	}
}

func TestPreviewCountIsBounded(t *testing.T) {
	dir, service := gitFixture(t)
	for i := 0; i <= maxUntrackedFiles; i++ {
		writeFixture(t, filepath.Join(dir, fmt.Sprintf("%03d.txt", i)), fmt.Sprintf("preview-%03d\n", i))
	}
	diff, err := service.Diff(t.Context(), "all")
	if err != nil || !diff.Truncated || !strings.Contains(diff.Text, "Additional untracked previews omitted") || strings.Contains(diff.Text, "preview-100") {
		t.Fatalf("preview count cap: %s, %v", diff.Text, err)
	}
}

func TestGlobalIgnoresAndAmbientRepositoryEnvironment(t *testing.T) {
	dir, service := gitFixture(t)
	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	ignore := filepath.Join(t.TempDir(), "ignore")
	writeFixture(t, ignore, "private.txt\n")
	gitRun(t, dir, "config", "--file", globalConfig, "core.excludesFile", ignore)
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "not-this-repository"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "not-this-index"))
	writeFixture(t, filepath.Join(dir, "private.txt"), "ignored content must not be previewed")
	writeFixture(t, filepath.Join(dir, "visible.txt"), "visible content")
	status, err := service.Status(t.Context())
	if err != nil || len(status.Entries) != 1 || status.Entries[0].Path != "visible.txt" {
		t.Fatalf("ambient repository or global excludes mishandled: %#v, %v", status, err)
	}
	diff, err := service.Diff(t.Context(), "all")
	if err != nil || strings.Contains(diff.Text, "ignored content") || !strings.Contains(diff.Text, "visible content") {
		t.Fatalf("global excludes were not respected: %s, %v", diff.Text, err)
	}
}

func TestParseStatusKeepsXYAndRenameOrder(t *testing.T) {
	raw := "MM both\nfile\x00RM new\tname\x00old\nname\x00 D deleted\x00?? --untracked\x00"
	want := []Entry{
		{Path: "both\nfile", Code: "MM"},
		{Path: "new\tname", OriginalPath: "old\nname", Code: "RM"},
		{Path: "deleted", Code: " D"},
		{Path: "--untracked", Code: "??"},
	}
	got, err := parseStatus(raw)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("porcelain -z parse: %#v, %v", got, err)
	}
}

func TestConcurrentInspection(t *testing.T) {
	dir, service := gitFixture(t)
	writeFixture(t, filepath.Join(dir, "untracked.txt"), "shared preview\n")
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := service.Status(t.Context()); err != nil {
				t.Error(err)
			}
			if _, err := service.Diff(t.Context(), "all"); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
}

func TestSubmoduleGitlinksDoNotExecuteNestedHelpers(t *testing.T) {
	dir, service := gitFixture(t)
	nested := filepath.Join(dir, "nested-repository")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, nested, "init", "-b", "main")
	file := filepath.Join(nested, "file.txt")
	writeFixture(t, file, "first\n")
	gitRun(t, nested, "add", ".")
	gitRun(t, nested, "commit", "-m", "nested fixture")
	gitRun(t, dir, "add", "nested-repository")
	gitRun(t, dir, "commit", "-m", "fixture gitlink")
	writeFixture(t, file, "second\n")
	gitRun(t, nested, "add", ".")
	gitRun(t, nested, "commit", "-m", "nested gitlink update")

	helper, marker := executableSentinel(t)
	gitRun(t, nested, "config", "core.fsmonitor", helper)
	gitRun(t, nested, "config", "filter.nested.clean", helper)
	gitRun(t, nested, "config", "filter.nested.process", helper)
	gitRun(t, nested, "config", "filter.nested.required", "true")
	gitRun(t, nested, "config", "diff.nested.textconv", helper)
	writeFixture(t, filepath.Join(nested, ".gitattributes"), "*.txt filter=nested diff=nested\n")
	writeFixture(t, file, "nested uncommitted content\n")
	status, err := service.Status(t.Context())
	if err != nil || len(status.Entries) != 1 || status.Entries[0].Path != "nested-repository" || status.Entries[0].Code != " M" {
		t.Fatalf("submodule gitlink change was omitted: %#v, %v", status, err)
	}
	diff, err := service.Diff(t.Context(), "all")
	if err != nil || !strings.Contains(diff.Text, "Subproject commit") || !strings.Contains(diff.Text, "nested worktree contents are not inspected") {
		t.Fatalf("submodule diff scope is unclear: %s, %v", diff.Text, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a nested repository's executable helper was invoked")
	}
}

package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestConversationBaselineSkipsLargeFiles(t *testing.T) {
	dir, service := gitFixture(t)
	asset := filepath.Join("docs", "assets", "demos", "commands.gif")
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(asset)), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, asset), "GIF89a\x00"+strings.Repeat("x", 369932-7))
	writeFixture(t, filepath.Join(dir, "z-source.txt"), "before\n")
	gitRun(t, dir, "add", ".")
	indexPath := filepath.Join(dir, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	captured, err := service.CaptureBaseline(t.Context())
	if err != nil {
		t.Fatalf("large asset prevented a usable baseline: %v", err)
	}
	defer captured.Close()
	writeFixture(t, filepath.Join(dir, "z-source.txt"), "after\n")
	stateBefore := gitRun(t, dir, "status", "--porcelain=v1", "-z")
	diff, err := captured.Diff(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PARTIAL BASELINE", `"docs/assets/demos/commands.gif"`, "64 KiB", "-before", "+after"} {
		if !strings.Contains(diff.Text, want) {
			t.Errorf("partial baseline diff missing %q:\n%s", want, diff.Text)
		}
	}
	if !diff.Truncated || strings.Contains(diff.Text, "Output truncated at 512 KiB") {
		t.Fatalf("coverage exclusion was not distinguished from output truncation: %#v", diff)
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
		t.Fatal("partial baseline modified the repository index")
	}
	if stateAfter := gitRun(t, dir, "status", "--porcelain=v1", "-z"); stateAfter != stateBefore {
		t.Fatal("partial diff modified working-tree state")
	}
}

func TestConversationBaselineFileSizeBoundaries(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, size := range []int{65535, 65536, 65537} {
			t.Run(fmt.Sprintf("binary=%t/bytes=%d", binary, size), func(t *testing.T) {
				dir, service := gitFixture(t)
				content := strings.Repeat("x", size)
				if binary {
					content = "\x00" + content[1:]
				}
				writeFixture(t, filepath.Join(dir, "file"), content)
				gitRun(t, dir, "add", "file")
				captured, err := service.CaptureBaseline(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer captured.Close()
				snapshot := captured.(*baseline)
				if size <= 65536 {
					data, err := os.ReadFile(filepath.Join(snapshot.directory, "file"))
					if err != nil || string(data) != content {
						t.Fatalf("in-limit snapshot was not byte-exact: bytes=%d err=%v", len(data), err)
					}
				} else if len(snapshot.tracked) != 0 {
					t.Fatal("oversized content was recorded as a complete snapshot")
				}
				diff, err := captured.Diff(t.Context())
				if err != nil || diff.Truncated != (size > 65536) {
					t.Fatalf("unexpected boundary result: %#v, %v", diff, err)
				}
			})
		}
	}
}

func baselineDiskUsage(t *testing.T, directory string) (files int, size int64) {
	t.Helper()
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		size += info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files, size
}

func TestConversationBaselineTotalByteBudget(t *testing.T) {
	dir, service := gitFixture(t)
	full := strings.Repeat("x", 65536)
	for i := 0; i < 255; i++ {
		writeFixture(t, filepath.Join(dir, fmt.Sprintf("a-%03d", i)), full)
	}
	writeFixture(t, filepath.Join(dir, "a-255"), full[:65535])
	writeFixture(t, filepath.Join(dir, "b-too-large-for-remaining-budget"), "xy")
	writeFixture(t, filepath.Join(dir, "z-last-byte"), "z")
	gitRun(t, dir, "add", ".")
	captured, err := service.CaptureBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	snapshot := captured.(*baseline)
	files, size := baselineDiskUsage(t, snapshot.directory)
	if size != 16*1024*1024 || snapshot.capturedBytes != int(size) || files != 257 {
		t.Fatalf("snapshot budget mismatch: files=%d bytes=%d recorded=%d", files, size, snapshot.capturedBytes)
	}
	if snapshot.excludedCount != 1 || !strings.Contains(snapshot.exclusions[0].reason, "16 MiB") {
		t.Fatalf("missing aggregate-budget exclusion: %#v", snapshot.exclusions)
	}
	data, err := os.ReadFile(filepath.Join(snapshot.directory, "z-last-byte"))
	if err != nil || string(data) != "z" {
		t.Fatalf("later fitting file was abandoned: %q, %v", data, err)
	}
}

func TestConversationBaselineFileCountBudget(t *testing.T) {
	dir, service := gitFixture(t)
	for i := 0; i < 1025; i++ {
		writeFixture(t, filepath.Join(dir, fmt.Sprintf("file-%04d", i)), "x")
	}
	gitRun(t, dir, "add", ".")
	captured, err := service.CaptureBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	snapshot := captured.(*baseline)
	files, size := baselineDiskUsage(t, snapshot.directory)
	if files != 1024 || size != 1024 || len(snapshot.tracked) != 1024 {
		t.Fatalf("snapshot count limit mismatch: files=%d bytes=%d captured=%d", files, size, len(snapshot.tracked))
	}
	if snapshot.excludedCount != 1 || snapshot.exclusions[0].path != "file-1024" ||
		!strings.Contains(snapshot.exclusions[0].reason, "1,024") {
		t.Fatalf("missing file-count exclusion: %#v", snapshot.exclusions)
	}
}

func TestConversationBaselineRejectsIncompleteReads(t *testing.T) {
	for _, tc := range []struct {
		name      string
		data      string
		size      int64
		truncated bool
		reason    string
	}{
		{"grew-after-stat", strings.Repeat("x", 65536), 65536, true, "64 KiB"},
		{"opened-size-over-limit", strings.Repeat("x", 65536), 65537, false, "64 KiB"},
		{"data-over-limit", strings.Repeat("x", 65537), 65537, false, "64 KiB"},
		{"utf8-crosses-boundary", strings.Repeat("x", 65535), 65538, true, "64 KiB"},
		{"shrunk-during-read", "x", 2, false, "changed size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := &baseline{directory: t.TempDir()}
			if err := snapshot.saveFile("file", []byte(tc.data), tc.truncated, tc.size); err != nil {
				t.Fatal(err)
			}
			files, size := baselineDiskUsage(t, snapshot.directory)
			if files != 0 || size != 0 || len(snapshot.tracked) != 0 || snapshot.capturedBytes != 0 ||
				snapshot.excludedCount != 1 || !strings.Contains(snapshot.exclusions[0].reason, tc.reason) {
				t.Fatalf("incomplete read became a complete snapshot: %#v, files=%d bytes=%d", snapshot, files, size)
			}
		})
	}
}

func TestConversationBaselineSmallBinaryChangesRemainVisible(t *testing.T) {
	dir, service := gitFixture(t)
	writeFixture(t, filepath.Join(dir, "small.bin"), "\x00before")
	gitRun(t, dir, "add", ".")
	captured, err := service.CaptureBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	writeFixture(t, filepath.Join(dir, "small.bin"), "\x00after")
	diff, err := captured.Diff(t.Context())
	if err != nil || diff.Truncated || !strings.Contains(diff.Text, "Binary files") || strings.ContainsRune(diff.Text, 0) {
		t.Fatalf("small binary behavior changed: %#v, %v", diff, err)
	}
}

func TestConversationBaselineExcludedFileStaysExcluded(t *testing.T) {
	dir, service := gitFixture(t)
	path := filepath.Join(dir, "large")
	writeFixture(t, path, strings.Repeat("x", 65537))
	gitRun(t, dir, "add", ".")
	captured, err := service.CaptureBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	for _, state := range []string{"unchanged", "modified", "shrunk", "deleted"} {
		switch state {
		case "modified":
			writeFixture(t, path, strings.Repeat("y", 65537))
		case "shrunk":
			writeFixture(t, path, "now small\n")
		case "deleted":
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		diff, err := captured.Diff(t.Context())
		if err != nil || !diff.Truncated || !strings.Contains(diff.Text, "0 captured files") ||
			!strings.Contains(diff.Text, `"large"`) || !strings.Contains(diff.Text, "no changes observed in captured files") ||
			strings.Contains(diff.Text, "no tracked file changes observed") || strings.Contains(diff.Text, "diff --git") {
			t.Fatalf("%s excluded file was misrepresented: %#v, %v", state, diff, err)
		}
	}
}

func TestConversationBaselineExclusionDetailsAreBounded(t *testing.T) {
	dir, service := gitFixture(t)
	for i := 0; i < 130; i++ {
		writeFixture(t, filepath.Join(dir, fmt.Sprintf("large-%03d", i)), strings.Repeat("x", 65537))
	}
	gitRun(t, dir, "add", ".")
	captured, err := service.CaptureBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	snapshot := captured.(*baseline)
	if snapshot.excludedCount != 130 || len(snapshot.exclusions) != 100 {
		t.Fatalf("unbounded or inaccurate exclusions: count=%d details=%d", snapshot.excludedCount, len(snapshot.exclusions))
	}
	diff, err := captured.Diff(t.Context())
	if err != nil || !diff.Truncated || len(diff.Text) > maxOutput ||
		!strings.Contains(diff.Text, "130 excluded paths") || !strings.Contains(diff.Text, "30 additional excluded paths not listed") ||
		strings.Contains(diff.Text, `"large-129"`) || strings.Contains(diff.Text, "Output truncated at 512 KiB") {
		t.Fatalf("exclusion summary is inaccurate: %#v, %v", diff, err)
	}
}

func TestConversationBaselineOutputLimitRemainsExplicit(t *testing.T) {
	dir, service := gitFixture(t)
	writeFixture(t, filepath.Join(dir, "excluded"), strings.Repeat("x", 65537))
	for i := 0; i < 8; i++ {
		writeFixture(t, filepath.Join(dir, fmt.Sprintf("text-%d", i)), strings.Repeat("before\n", 8000))
	}
	gitRun(t, dir, "add", ".")
	captured, err := service.CaptureBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	for i := 0; i < 8; i++ {
		writeFixture(t, filepath.Join(dir, fmt.Sprintf("text-%d", i)), strings.Repeat("after\n", 8000))
	}
	diff, err := captured.Diff(t.Context())
	const notice = "\n[Output truncated at 512 KiB; additional content may be omitted.]\n"
	if err != nil || !diff.Truncated || len(diff.Text) > maxOutput+len(notice) ||
		!strings.HasSuffix(diff.Text, notice) || !strings.Contains(diff.Text, `"excluded"`) {
		t.Fatalf("session output did not respect its bound: bytes=%d truncated=%t err=%v", len(diff.Text), diff.Truncated, err)
	}
}

func TestConversationBaselineCanceledCapture(t *testing.T) {
	_, service := gitFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	captured, err := service.CaptureBaseline(ctx)
	if captured != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation became a successful baseline: %v, %v", captured, err)
	}
}

type baselineCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks atomic.Int32
	at     int32
}

func (ctx *baselineCancelContext) Err() error {
	// Cancel at a deterministic read-loop checkpoint, without scheduler timing.
	if ctx.checks.Add(1) == ctx.at {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestConversationBaselineCancellationStopsBetweenFiles(t *testing.T) {
	for _, tc := range []struct {
		name     string
		at       int32
		captured int
	}{
		{"after-first-read", 2, 0},
		{"between-files", 3, 1},
		{"after-final-file", 5, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			writeFixture(t, filepath.Join(source, "first"), "one")
			writeFixture(t, filepath.Join(source, "second"), "two")
			root, err := os.OpenRoot(source)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			snapshot := &baseline{directory: t.TempDir()}
			err = snapshot.captureFiles(&baselineCancelContext{Context: ctx, cancel: cancel, at: tc.at}, root, []string{"first", "second"})
			if !errors.Is(err, context.Canceled) || len(snapshot.tracked) != tc.captured {
				t.Fatalf("capture continued after cancellation: tracked=%v err=%v", snapshot.tracked, err)
			}
			directory := snapshot.directory
			if err := snapshot.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatalf("partial snapshot contents survived cleanup: %v", err)
			}
		})
	}
}

func TestConversationBaselineExcludedSymlinkAndAbsentPath(t *testing.T) {
	dir, service := gitFixture(t)
	outside := filepath.Join(t.TempDir(), "outside")
	writeFixture(t, outside, "outside fixture contents")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	writeFixture(t, filepath.Join(dir, "missing"), "before")
	gitRun(t, dir, "add", ".")
	if err := os.Remove(filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
	captured, err := service.CaptureBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	diff, err := captured.Diff(t.Context())
	if err != nil || !diff.Truncated || !strings.Contains(diff.Text, "2 excluded paths") ||
		!strings.Contains(diff.Text, "non-regular path; contents not followed") ||
		!strings.Contains(diff.Text, "not present at capture") ||
		strings.Contains(diff.Text, "outside fixture contents") {
		t.Fatalf("unrepresented paths were hidden or followed: %#v, %v", diff, err)
	}
}

func TestConversationBaselineRealReadFailureCleansTemporaryState(t *testing.T) {
	dir, service := gitFixture(t)
	parent := filepath.Join(dir, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(parent, "file"), "original")
	gitRun(t, dir, "add", ".")
	if err := os.Remove(filepath.Join(parent, "file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeFixture(t, filepath.Join(outside, "file"), "must not be read")
	if err := os.Symlink(outside, parent); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	snapshotRoot := t.TempDir()
	for _, variable := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(variable, snapshotRoot)
	}
	captured, err := service.CaptureBaseline(t.Context())
	if captured != nil || err == nil || !strings.Contains(err.Error(), "inspect baseline file") {
		t.Fatalf("unsafe source traversal became a partial success: %v, %v", captured, err)
	}
	entries, err := os.ReadDir(snapshotRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed capture leaked temporary state: %v, %v", entries, err)
	}
}

func TestConversationBaselineSnapshotWriteErrorsRemainFailures(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, filepath.Join(directory, "parent"), "not a directory")
	snapshot := &baseline{directory: directory}
	err := snapshot.saveFile("parent/file", []byte("data"), false, 4)
	if err == nil || !strings.Contains(err.Error(), "create baseline path") ||
		len(snapshot.tracked) != 0 || snapshot.excludedCount != 0 {
		t.Fatalf("write failure became an exclusion: %#v, %v", snapshot, err)
	}
	if err := os.Mkdir(filepath.Join(directory, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.saveFile("directory", []byte("data"), false, 4); err == nil ||
		!strings.Contains(err.Error(), "save baseline file") {
		t.Fatalf("snapshot write error was lost: %v", err)
	}
}

func TestConversationBaselineNonGitAndEmptyRepositories(t *testing.T) {
	isolateGit(t)
	nonGit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, empty := gitFixture(t)
	for _, service := range []*Service{nonGit, empty} {
		captured, err := service.CaptureBaseline(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		diff, err := captured.Diff(t.Context())
		if err != nil || diff.Truncated || diff.IsRepository != (service == empty) {
			t.Fatalf("empty project was reported as an incomplete capture: %#v, %v", diff, err)
		}
		if err := captured.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConversationBaselineDiffFailuresAndCancellation(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		t.Run(fmt.Sprintf("tracked=%t", tracked), func(t *testing.T) {
			dir, service := gitFixture(t)
			if tracked {
				writeFixture(t, filepath.Join(dir, "file"), "one")
				gitRun(t, dir, "add", ".")
			}
			captured, err := service.CaptureBaseline(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer captured.Close()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := captured.Diff(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("session diff lost cancellation: %v", err)
			}
			service.git = filepath.Join(t.TempDir(), "missing-git")
			if _, err := captured.Diff(t.Context()); err == nil {
				t.Fatal("missing Git became a successful partial diff")
			}
		})
	}
}

func TestConversationBaselineReadPermissionFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires enforced Unix file permissions")
	}
	source := t.TempDir()
	file := filepath.Join(source, "file")
	writeFixture(t, file, "must be readable")
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(file, 0600); err != nil {
			t.Error(err)
		}
	})
	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	snapshot := &baseline{directory: t.TempDir()}
	err = snapshot.captureFiles(t.Context(), root, []string{"file"})
	if err == nil || !strings.Contains(err.Error(), "read baseline file") ||
		len(snapshot.tracked) != 0 || snapshot.excludedCount != 0 {
		t.Fatalf("permission failure became an intentional exclusion: %#v, %v", snapshot, err)
	}
}

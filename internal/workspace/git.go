// Package workspace provides read-only Git status and whole-working-tree diffs.
//
// Each Git invocation retains at most 512 KiB of stdout and 8 KiB of diagnostics
// and has a 30-second timeout in addition to the caller's context. Status returns
// ErrOutputLimit rather than a misleading partial status. Diff retains at most
// 512 KiB plus its truncation notice, previews at most 100 untracked files, and
// reads at most 64 KiB plus one UTF-8 rune per untracked file. Omitted preview
// content is explicitly labeled and sets Diff.Truncated.
//
// Pagers, external diff, textconv, fsmonitor, hooks, executable content filters,
// optional index writes, and lazy object fetching are disabled. Filtered content
// is inspected without executing its configured conversion programs.
// Submodule gitlink changes are included; nested worktree contents are not
// recursively inspected, so their independently configured helpers cannot run.
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
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxOutput           = 512 * 1024
	maxUntrackedPreview = 64 * 1024
	maxUntrackedFiles   = 100
)

// ErrOutputLimit means complete Git metadata could not fit within the read cap.
var ErrOutputLimit = errors.New("Git inspection output exceeds the 512 KiB limit")

// Service is an immutable project-directory handle, safe for concurrent reads.
type Service struct {
	directory string
	git       string
}

// New resolves symlinks and requires an existing directory, but not a Git repo.
func New(directory string) (*Service, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve project directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve project directory: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("inspect project directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("project path must be a directory")
	}
	return &Service{directory: canonical, git: "git"}, nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *limitedBuffer) Len() int {
	return buffer.buffer.Len()
}

func (buffer *limitedBuffer) String() string {
	return buffer.buffer.String()
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	size := len(data)
	remaining := buffer.limit - buffer.Len()
	if size > remaining {
		buffer.truncated = true
		data = data[:remaining]
	}
	_, err := buffer.buffer.Write(data)
	return size, err
}

type gitFailure struct {
	operation  string
	cause      error
	diagnostic string
	hasOutput  bool
}

func (failure *gitFailure) Error() string {
	if failure.diagnostic == "" {
		return fmt.Sprintf("Git %s: %v", failure.operation, failure.cause)
	}
	return fmt.Sprintf("Git %s: %v: %s", failure.operation, failure.cause, failure.diagnostic)
}

func (failure *gitFailure) Unwrap() error {
	return failure.cause
}

func gitEnvironment() []string {
	var env []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		keepConfigPath := key == "GIT_CONFIG_GLOBAL" || key == "GIT_CONFIG_SYSTEM" || key == "GIT_CONFIG_NOSYSTEM"
		if (strings.HasPrefix(key, "GIT_") && !keepConfigPath) || key == "LC_ALL" || key == "LANG" || key == "LANGUAGE" {
			continue
		}
		env = append(env, value)
	}
	return append(env, "LC_ALL=C", "LANG=C", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0")
}

func (service *Service) run(ctx context.Context, protection []string, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	options := []string{
		"--no-pager", "--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "color.ui=false",
		"-c", "diff.external=",
	}
	options = append(options, protection...)
	options = append(options, args...)
	cmd := exec.CommandContext(ctx, service.git, options...)
	cmd.Dir = service.directory
	cmd.Env = gitEnvironment()
	cmd.WaitDelay = time.Second
	output := &limitedBuffer{limit: maxOutput}
	stderr := &limitedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = output, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", false, fmt.Errorf("Git operation stopped: %w", ctx.Err())
		}
		diagnostic := strings.TrimSpace(stderr.String())
		if stderr.truncated {
			diagnostic += "\n[Git diagnostic truncated at 8 KiB]"
		}
		return "", false, &gitFailure{
			operation: args[0], cause: err, diagnostic: diagnostic, hasOutput: output.Len() != 0,
		}
	}
	return output.String(), output.truncated, nil
}

func exitCode(err error, code int) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == code
}

func silentExit(err error, code int) bool {
	var failure *gitFailure
	return exitCode(err, code) && errors.As(err, &failure) && failure.diagnostic == "" && !failure.hasOutput
}

func (service *Service) protection(ctx context.Context) ([]string, error) {
	output, truncated, err := service.run(ctx, nil, "config", "--null", "--name-only", "--get-regexp", `^filter\..*\.(clean|smudge|process|required)$`)
	if err != nil {
		if silentExit(err, 1) {
			return nil, nil
		}
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("Git filter configuration: %w", ErrOutputLimit)
	}
	if output != "" && !strings.HasSuffix(output, "\x00") {
		return nil, errors.New("incomplete Git filter configuration")
	}
	var options []string
	for _, key := range strings.Split(output, "\x00") {
		if key == "" {
			continue
		}
		value := ""
		if strings.HasSuffix(strings.ToLower(key), ".required") {
			value = "false"
		}
		options = append(options, "-c", key+"="+value)
	}
	return options, nil
}

func (service *Service) inspect(ctx context.Context) (Status, []string, error) {
	state := Status{Root: service.directory}
	inside, truncated, err := service.run(ctx, nil, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if isNonRepository(err) {
			marker, markerErr := hasGitMarker(service.directory)
			if markerErr != nil {
				return state, nil, markerErr
			}
			if !marker {
				return state, nil, nil
			}
		}
		return state, nil, err
	}
	if truncated {
		return state, nil, fmt.Errorf("Git repository discovery: %w", ErrOutputLimit)
	}
	if strings.TrimSpace(inside) == "false" {
		return state, nil, nil
	}
	if strings.TrimSpace(inside) != "true" {
		return state, nil, errors.New("unexpected Git repository discovery output")
	}
	state.IsRepository = true
	protection, err := service.protection(ctx)
	if err != nil {
		return state, nil, err
	}
	root, truncated, err := service.run(ctx, protection, "rev-parse", "--show-toplevel")
	if err != nil {
		return state, nil, err
	}
	if truncated {
		return state, nil, fmt.Errorf("Git repository root: %w", ErrOutputLimit)
	}
	root = strings.TrimSuffix(root, "\n")
	if !filepath.IsAbs(root) {
		return state, nil, errors.New("Git did not report an absolute repository root")
	}
	state.Root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return state, nil, fmt.Errorf("resolve Git repository root: %w", err)
	}
	branch, truncated, err := service.run(ctx, protection, "symbolic-ref", "--quiet", "--short", "HEAD")
	if silentExit(err, 1) {
		branch, truncated, err = service.run(ctx, protection, "rev-parse", "--short", "HEAD")
		branch = "detached@" + strings.TrimSpace(branch)
	}
	if err != nil {
		return state, nil, err
	}
	if truncated {
		return state, nil, fmt.Errorf("Git branch: %w", ErrOutputLimit)
	}
	state.Branch = strings.TrimSpace(branch)
	if state.Branch == "" {
		return state, nil, errors.New("Git did not report a branch or detached HEAD")
	}
	raw, truncated, err := service.run(ctx, protection, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--renames", "--ignore-submodules=dirty")
	if err != nil {
		return state, nil, err
	}
	if truncated {
		return state, nil, fmt.Errorf("repository status: %w", ErrOutputLimit)
	}
	state.Entries, err = parseStatus(raw)
	return state, protection, err
}

func isNonRepository(err error) bool {
	var failure *gitFailure
	if !exitCode(err, 128) || !errors.As(err, &failure) {
		return false
	}
	return strings.HasPrefix(failure.diagnostic, "fatal: not a git repository (or any of the parent directories)") ||
		strings.HasPrefix(failure.diagnostic, "fatal: not a git repository (or any parent up to mount point ")
}

func hasGitMarker(directory string) (bool, error) {
	for {
		_, err := os.Lstat(filepath.Join(directory, ".git"))
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("inspect Git repository marker: %w", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return false, nil
		}
		directory = parent
	}
}

func parseStatus(raw string) ([]Entry, error) {
	if raw == "" {
		return nil, nil
	}
	if !strings.HasSuffix(raw, "\x00") {
		return nil, errors.New("incomplete Git status output")
	}
	records := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	var entries []Entry
	for i := 0; i < len(records); i++ {
		record := records[i]
		if len(record) < 4 || record[2] != ' ' {
			return nil, errors.New("unexpected Git status record")
		}
		entry := Entry{Code: record[:2], Path: record[3:]}
		if strings.ContainsAny(entry.Code, "RC") {
			i++
			if i == len(records) {
				return nil, errors.New("incomplete Git rename record")
			}
			entry.OriginalPath = records[i]
			if entry.OriginalPath == "" {
				return nil, errors.New("empty Git rename source path")
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Status reports repository-root-relative paths and two-character Git XY codes.
// A non-Git directory is a successful result with IsRepository=false.
func (service *Service) Status(ctx context.Context) (Status, error) {
	state, _, err := service.inspect(ctx)
	return state, err
}

// Diff defaults to all and separates staged, unstaged, and untracked changes.
// All reads are local, and no tracked or untracked user changes are modified.
func (service *Service) Diff(ctx context.Context, mode string) (Diff, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "all"
	}
	if mode != "all" && mode != "staged" && mode != "unstaged" {
		return Diff{}, errors.New("diff mode must be all, staged, or unstaged")
	}
	state, protection, err := service.inspect(ctx)
	if err != nil {
		return Diff{}, err
	}
	if !state.IsRepository {
		return Diff{Text: "Not a Git working tree. Sodapop can still help with files in this directory."}, nil
	}
	output := &limitedBuffer{limit: maxOutput}
	fmt.Fprintln(output, "ENTIRE WORKING TREE (including pre-existing edits; not exclusively Sodapop changes)")
	fmt.Fprintln(output, "Submodule gitlink changes are included; nested worktree contents are not inspected.")
	truncated := false
	for _, section := range []string{"staged", "unstaged"} {
		if mode != "all" && mode != section {
			continue
		}
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--color=never", "--ignore-submodules=dirty", "--submodule=short", "--no-relative"}
		if section == "staged" {
			args = append(args, "--cached")
		}
		text, cut, err := service.run(ctx, protection, args...)
		if err != nil {
			return Diff{}, err
		}
		fmt.Fprintf(output, "\n%s\n", strings.ToUpper(section))
		if text == "" {
			fmt.Fprintln(output, "(none)")
		} else {
			fmt.Fprint(output, text)
		}
		truncated = truncated || cut
	}
	if mode == "all" {
		cut, err := writeUntracked(ctx, output, state)
		if err != nil {
			return Diff{}, err
		}
		truncated = truncated || cut
	}
	truncated = truncated || output.truncated
	text := output.String()
	if truncated {
		text += "\n[Output truncated: 512 KiB total, 64 KiB per untracked preview, at most 100 previews. Additional content may be omitted.]\n"
	}
	return Diff{Text: text, IsRepository: true, Truncated: truncated}, nil
}

func writeUntracked(ctx context.Context, output *limitedBuffer, state Status) (truncated bool, resultErr error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	root, err := os.OpenRoot(state.Root)
	if err != nil {
		return false, fmt.Errorf("open repository for untracked previews: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close repository preview handle: %w", err))
		}
	}()
	fmt.Fprintln(output, "\nUNTRACKED")
	found, count := false, 0
	for _, entry := range state.Entries {
		if entry.Code != "??" {
			continue
		}
		found = true
		if output.truncated || count == maxUntrackedFiles {
			fmt.Fprintln(output, "[Additional untracked previews omitted at the display limit.]")
			return true, nil
		}
		if err := ctx.Err(); err != nil {
			return truncated, fmt.Errorf("untracked preview stopped: %w", err)
		}
		count++
		fmt.Fprintf(output, "\n%q\n", entry.Path)
		info, err := root.Lstat(entry.Path)
		if err != nil {
			return truncated, fmt.Errorf("inspect untracked file %q: %w", entry.Path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			fmt.Fprintln(output, "(skipped symlink; contents not followed)")
			continue
		}
		if !info.Mode().IsRegular() {
			fmt.Fprintln(output, "(non-regular file; preview skipped)")
			continue
		}
		data, cut, size, err := readPreview(root, entry.Path)
		if err != nil {
			return truncated, fmt.Errorf("preview untracked file %q: %w", entry.Path, err)
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			fmt.Fprintf(output, "(binary file; %d bytes, contents not displayed)\n", size)
			continue
		}
		output.Write(data)
		fmt.Fprintln(output)
		if cut {
			truncated = true
			fmt.Fprintln(output, "[File preview truncated at 64 KiB.]")
		}
	}
	if !found {
		fmt.Fprintln(output, "(none)")
	}
	return truncated, nil
}

func readPreview(root *os.Root, path string) (data []byte, truncated bool, size int64, resultErr error) {
	// Root confines traversal; NOFOLLOW and NONBLOCK also protect against a final
	// component being swapped to a symlink or FIFO after the preceding Lstat.
	file, err := openPreviewFile(root, path)
	if err != nil {
		return nil, false, 0, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, false, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, 0, errors.New("file is no longer a regular file")
	}
	data = make([]byte, maxUntrackedPreview+utf8.UTFMax)
	n, err := io.ReadFull(file, data)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, false, 0, err
	}
	data = data[:n]
	truncated = n > maxUntrackedPreview
	if truncated {
		end := maxUntrackedPreview
		for end > maxUntrackedPreview-utf8.UTFMax && !utf8.RuneStart(data[end]) {
			end--
		}
		data = data[:end]
	}
	return data, truncated, info.Size(), nil
}

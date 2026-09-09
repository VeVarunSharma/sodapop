# Hostile-input Git inspection

Scope: `internal/workspace/`. Apply the [repository rules](../../AGENTS.md) first.

## Rules and reasons

- Keep inspection read-only and scoped to the canonical project. `/diff` describes
  the whole worktree, including pre-existing edits, not only agent-created changes.
- Preserve environment/config hardening for every Git invocation: no pagers,
  hooks, filters, fsmonitor, external diff/textconv, optional locks, prompts, or
  lazy fetching. A read-looking Git operation must not execute repository helpers.
- Retain timeouts and bounds on stdout, diagnostics, untracked previews, and file
  counts. A hostile or huge repository must not stall the UI or exhaust memory.
- Fail incomplete status metadata and mark partial diffs/previews as truncated;
  never present omitted changes as a complete inspection.
- Conversation baselines keep complete, bounded file snapshots. Size/budget
  exclusions must preserve useful captures and appear in `/diff session`, not
  fail startup. Real read errors still fail; partial coverage must not imply a
  complete inspection.
- Preserve filename, symlink, and submodule boundaries. Do not recursively inspect
  independently configured nested worktrees or let previews escape the project.

## Start here

[git.go](git.go) documents limits and hardening; [types.go](types.go) is the public result
contract. [git_test.go](git_test.go) exercises hostile repositories in temporary fixtures.
[baseline_test.go](baseline_test.go) covers partial snapshots, exact budgets, and cleanup.

## Checks

From the repository root: `go test ./internal/workspace`.
For affected paths, assert unchanged worktree/index, bounded output, cancellation,
explicit truncation, and that configured helper programs were not executed.

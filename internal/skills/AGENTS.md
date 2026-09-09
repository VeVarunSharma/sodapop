# Skill storage and activation

Scope: `internal/skills/`. Apply the [repository rules](../../AGENTS.md) first.

## Rules and reasons

- Treat every non-bundled skill as untrusted until its canonical local root or
  normalized Git repository is explicitly trusted. Trust never grants tool
  permissions or enables a skill for a project.
- Copy validated skill content into private, content-addressed Sodapop state.
  Never execute from mutable import paths, follow symlinks, or load ambient
  Copilot skill directories.
- Preserve old digest directories when catalog entries change or are removed:
  saved sessions bind exact digests and must either resume with those bytes or
  fail clearly when the content is missing.
- Keep project activation separate from installation and key it by canonical
  project identity. Enabling or disabling skills requires a fresh engine so a
  live conversation cannot change capabilities invisibly.
- Git installs must disable credential prompting, pin the resolved commit, and
  reject URLs containing credentials. Private-repository authentication needs a
  separate explicit design.

## Start here

[manager.go](manager.go) owns validation, trust, acquisition, immutable storage,
and activation. [manager_test.go](manager_test.go) covers isolation and unsafe input.
Runtime loading is in [engine session configuration](../engine/session_config.go).

## Checks

From the repository root: `go test ./internal/skills ./internal/engine ./internal/ui`.

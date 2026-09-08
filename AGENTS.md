# Working on Sodapop

Sodapop is a Go and Charm inspired terminal application backed by the official Copilot SDK.
Use `sodapop`, `SODAPOP_`, and `github.com/VeVarunSharma/sodapop` for first-party names.
The canonical [website](https://sodapop.sh) and [repository](https://github.com/VeVarunSharma/sodapop)
use Sodapop branding; preserve upstream names, license terms, and pinned versions.

## Read only what the task needs

This file applies to the whole repository. Before editing, read the `AGENTS.md`
files along the target file's directory path, from root to nearest scope.
Scoped rules add to these rules; they do not relax them. For changes spanning
boundaries, read each affected guide. Links are navigation, not automatic includes.

| Working on | Local guidance |
| --- | --- |
| Startup, flags, identity wiring | [internal/app/AGENTS.md](internal/app/AGENTS.md) |
| UI state, rendering, asynchronous work | [internal/ui/AGENTS.md](internal/ui/AGENTS.md) |
| SDK, sessions, permissions, event streams | [internal/engine/AGENTS.md](internal/engine/AGENTS.md) |
| OAuth and credential storage | [internal/auth/AGENTS.md](internal/auth/AGENTS.md) |
| Slash commands, help, completion | [internal/commands/AGENTS.md](internal/commands/AGENTS.md) |
| Preferences and account-scoped paths | [internal/config/AGENTS.md](internal/config/AGENTS.md) |
| Read-only Git inspection | [internal/workspace/AGENTS.md](internal/workspace/AGENTS.md) |
| Runtime pins, extraction, isolation | [internal/runtimebundle/AGENTS.md](internal/runtimebundle/AGENTS.md) |
| Cross-package and opt-in live tests | [internal/integration/AGENTS.md](internal/integration/AGENTS.md) |
| Build, packaging, coverage, demos | [scripts/AGENTS.md](scripts/AGENTS.md) |
| CI, tagged releases, instruction entry point | [.github/AGENTS.md](.github/AGENTS.md) |
| Website, public documentation, Pages delivery | [site/AGENTS.md](site/AGENTS.md) |

Read [architecture](docs/architecture.md) for ownership and cross-package changes,
[README](README.md) for product behavior, and [the guidance design](docs/agent-guidance.md)
when changing this instruction layout. Keep `cmd/sodapop` thin: signal cancellation
and delegation to `internal/app.Run`, not application logic.

## Non-negotiable boundaries

- Keep engines and their token sources bound to the explicit account; scope session
  history by account and canonical project. Never fall back to ambient GitHub/Copilot
  credentials, an installed `copilot`, or ordinary `~/.copilot` state.
- Permission handling fails closed. Only structured reads inside the canonical
  project may bypass a prompt by default. Other operations require an explicit
  decision; planning is advisory, and approved shell commands are not sandboxed.
- Cancellation does not undo edits. Never automatically replay a failed/canceled
  action or stage, reset, commit, or push the user's worktree.
- Keep secrets out of source, preferences, logs, and fixtures. Local `.sodapop.env`
  holds only the public `SODAPOP_GITHUB_CLIENT_ID`; preserve the existing OAuth
  registration and grants. See [authentication](docs/authentication.md).
- Generated `internal/runtimebundle/zcopilot*.go`, `bin/`, and `dist/` are outputs.
  Change pins or tooling, not generated artifacts, unless regeneration is requested.

## Development loop

Use the Go version required by [go.mod](go.mod) or later. Run commands from the
repository root. Keep `SODAPOP_LIVE_QUALIFY` and `SODAPOP_RUNTIME_SMOKE` unset or `0`
for normal checks; live usage requires explicit authorization.

| Need | Command |
| --- | --- |
| Focused package (example) | `go test ./internal/engine` |
| Build/install/package fixtures, without bundling | `go test ./scripts` |
| Credential-free suite | `make test` |
| Coverage ratchet, race suite, vet | `make check` |
| Build the executable with the pinned runtime (network required) | `make build` |
| Rebuild and launch the TUI | `make start` (`make run` is an alias) |

- Add or update focused tests for every feature, fix, or observable behavior change.
  Assert success, relevant failures, and boundaries, not just snapshots or coverage.
  Cancellation, concurrency, stale results, exact-once callbacks, redaction, and
  fail-closed behavior need dedicated cases when affected.
- Use injected dependencies, fakes, and temporary directories. Normal tests must
  not require live OAuth, Copilot, network services, or an interactive terminal.
- Run focused checks while iterating; add `-race` for concurrency-sensitive changes.
  Format changed Go files with `gofmt -w`, then run `make check` before completion.
  Never lower [the baseline](.github/coverage-baseline.txt) or
  [package floors](.github/coverage-targets.txt); `make coverage-baseline` is only
  for a genuine increase from the full passing suite.
- If no tests change, give an explicit PR justification: documentation-only and
  generated-only changes can qualify; refactors are not automatically exempt.
  Native smoke and [live qualification](docs/live-qualification.md) are separate gates.

## Keep this a map

Put local rules in the narrowest useful guide, with the reason and links to code/tests.
Keep detailed explanations in `docs/`; do not copy them into every guide.
Update guidance with the code it describes. If code and guidance disagree, surface
and resolve the mismatch rather than silently discarding a safety constraint.

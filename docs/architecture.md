# Sodapop architecture

Sodapop is a Go terminal application. Bubble Tea owns input and UI state; Bubbles, Lip Gloss, and Glamour provide components and presentation. A small engine interface separates the UI from the official Copilot Go SDK.

The SDK starts an embedded, explicitly pinned Copilot runtime over a managed stdio connection. Sodapop provides credentials and permission decisions; the runtime owns model orchestration, tools, and conversation persistence. Sodapop does not run another agent loop or scrape terminal output.

## Ownership

| Package | Responsibility |
|---|---|
| `internal/app` | Startup, display flags, identity/project wiring, terminal lifecycle |
| `internal/ui` | Composer, transcript, tool cards, palettes, approval and account screens |
| `internal/commands` | One command catalog and parser for help, completion, and execution |
| `internal/auth` | OAuth device authorization and secure credentials |
| `internal/engine` | SDK integration, typed events, sessions, and permission bridges |
| `internal/workspace` | Read-only Git status and diff |
| `internal/config` | Versioned preferences and account-scoped state paths |
| `internal/skills` | Skill trust, validation, immutable storage, and project activation |
| `internal/runtimebundle` | Runtime pins, embedded artifacts, environment isolation, local handshake |

The UI processes typed events; blocking operations run outside the update loop. Streamed and finalized messages share identity so they can be reconciled. Permission/question callbacks are tied to their originating session and must resolve on cancellation or shutdown.

## State boundaries

Sodapop preferences live under the platform configuration directory. Runtime session state and explicit stdio MCP definitions are account-scoped under Sodapop's state directory, with Windows using the local application-data directory rather than roaming configuration storage. Unix modes and Windows protected ACLs keep those files private, with project identity tracked for resume. MCP definitions contain environment-variable names rather than secret values. Tokens belong only in a secure credential store or explicitly selected process memory.

The runtime's binary extraction cache is managed by the SDK. Its version is pinned independently of any user-installed `copilot` executable. Sodapop does not fall back to an arbitrary executable or ambient GitHub token if its bundle or credentials are unavailable.

MCP is opt-in. Sodapop never enables runtime configuration discovery or imports ambient MCP servers. The application validates an account-scoped registry, resolves explicitly referenced environment variables in memory, and passes only enabled servers into an immutable engine configuration snapshot. Create and resume use the same snapshot; configuration changes require a reconnect. MCP tool requests remain subject to the ordinary fail-closed permission lifecycle.

Skills are also opt-in and never discovered from ambient Copilot state. Sodapop
validates Copilot-native `SKILL.md` directories, copies them into private
content-addressed state, and records enablement by canonical project. Each
conversation stores exact skill digests; create and resume pass only those
immutable parent directories to the SDK. Bundled skills are trusted but disabled
until selected, while local roots and public Git repositories require explicit
trust before installation. Skill instructions cannot grant tool permissions.

`/plan` is an application-level advisory focus, not a security boundary. `/compact` uses the runtime's manual history compaction RPC while keeping the rendered transcript intact. `/diff` describes the whole working tree and never mutates it. Starting a new conversation disconnects/preserves prior history; cancellation never implies file rollback.

## Shipping

The runtime and SDK pins are in `internal/runtimebundle/version.go` and `go.mod`. The build invokes the official SDK bundler with an explicit runtime version and supported target. Generated artifacts stay out of Git.

The first-party module is `github.com/VeVarunSharma/sodapop`; `cmd/sodapop` builds to `bin/sodapop` by default and `bin/sodapop.exe` on Windows. Supported target names are `darwin/arm64`, `darwin/amd64`, `linux/arm64`, `linux/amd64`, `windows/arm64`, and `windows/amd64`. The project lives at [sodapop.sh](https://sodapop.sh) and [github.com/VeVarunSharma/sodapop](https://github.com/VeVarunSharma/sodapop).

Packaging uses fresh temporary staging and an explicit payload list, so local
environment files and stale output cannot enter a candidate archive. macOS/Linux
use `sodapop-<version>-<goos>-<goarch>.tar.gz`; Windows uses `.zip` with
`sodapop.exe`. Both include project documentation/license and a fresh `LICENSES/`
directory with the pinned runtime's terms and dependency notices. Adjacent
`.sha256` files reference archive basenames. Release metadata binds the commit,
both version pins, archive hashes, and native executable hashes.

`scripts/releasectl` owns release verification and safe extraction.
`scripts/installcheck` exercises the archived command and a copied portable
installation in an isolated home with Go, Node, and Copilot removed from PATH.
Package-manager jobs consume those verified bytes, not separately rebuilt
executables. Public availability, package-registry publication, and signing
remain distinct from local fixture tests; see [distribution gates](distribution.md).

Credential-free CI checks components and state transitions on macOS, Linux, and Windows. Windows ACL and file-lock behavior requires the native Windows runner; cross-compilation covers build tags but cannot replace those tests. Native bundle checks exercise process startup and protocol status. Owner-approved live OAuth/Copilot checks are a separate release gate; they cannot be replaced by fake events, generic GitHub login, or cross-compilation.

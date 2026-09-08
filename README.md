# Sodapop

<p align="center">
  <a href="images/sodapop-title.png">
    <picture>
      <source media="(prefers-reduced-motion: reduce)" srcset="images/sodapop-title.png" />
      <img src="images/sodapop-title.gif" width="640" alt="Sodapop's pink-to-purple title, neon carbonation, and smiling soda-can mascot. Open for a still image." />
    </picture>
  </a>
</p>

A polished terminal coding companion, built with Go and Charm and powered by the GitHub Copilot SDK.

[Website](https://sodapop.sh) | [Source](https://github.com/VeVarunSharma/sodapop)

Run `sodapop` from a project directory for a colorful conversation, streamed tool activity, explicit approvals, and a discoverable `/` command palette. Sodapop provides its own interface and OAuth onboarding; it does not wrap the Copilot CLI's screen or fork Crush.

<p align="center">
  <img src="docs/assets/demos/overview.gif" width="800" alt="Sodapop's soda-can welcome animation, command palette, chat-to-plan toggle, and read-only Git diff in a local example project." />
</p>

[View a still image](docs/assets/demos/overview.png). This local-only demo uses prepared Git changes and stays signed out; no AI responses are simulated.

The welcome panel introduces Sodapop's happy soda-can mascot: it lifts its pull tab, pops open, and releases foam and rising bubbles before settling into a smile. The roughly 2.4-second animation runs once per launch alongside background startup, never blocks the composer, and does not imply that Copilot is connected. Type, paste, or press Esc to skip it without losing input; Ctrl+C keeps its normal cancel/exit behavior. Dialogs and errors take priority. Smaller terminals automatically animate a compact version of the same opening sequence, terminals too small to fit the mascot use text-only guidance, and reduced motion shows the resting pose immediately. Use `--no-banner` to keep the text-only welcome panel.

## Build and run

Development requires Go 1.27.1 or later, Git, and network access to fetch the pinned dependencies and runtime. Node.js 18 or later is also needed for npm distribution checks and package generation. The supported targets are macOS (`darwin/arm64`, `darwin/amd64`), glibc-based Linux (`linux/arm64`, `linux/amd64`), and Windows x64 (`windows/amd64`).

```sh
make build
make start
```

The build downloads the explicitly pinned Copilot runtime, verifies the upstream asset checksums, and embeds it. End users of the resulting executable do not need Go, Node.js, or an existing Copilot installation.

On Windows x64, run `bash scripts/build.sh` from Git Bash and launch `bin/sodapop.exe`. Windows release packaging likewise uses `bash scripts/package.sh`; `make install` remains the Unix local-install helper.

`make start` rebuilds and launches Sodapop; `make run` is an alias. Both use `SODAPOP_OUTPUT` when set, otherwise `bin/sodapop`. Run `make help` to list the available development commands. For local OAuth configuration, copy `.sodapop.env.example` to `.sodapop.env` and set only `SODAPOP_GITHUB_CLIENT_ID` to the project's existing public GitHub OAuth Client ID. The ignored `.sodapop.env` file is loaded automatically by the Makefile, not by direct script invocations or the installed executable.

To install the command into your user bin directory:

```sh
make install
export PATH="$HOME/.local/bin:$PATH"  # If this directory is not already on PATH.
sodapop
```

`SODAPOP_INSTALL_DIR` overrides the installation directory. Relative paths resolve from the repository root; the installer prints an absolute directory for PATH setup. Installation does not edit your shell profile.

## Install a published release

Once the owned tap and npm scope are published, the target package-manager
commands are:

```sh
# Homebrew tap (macOS and Linux)
brew install VeVarunSharma/sodapop/sodapop

# npm launcher (macOS, glibc-based Linux, and Windows x64)
npm install --global @sodapop-sh/cli

# Windows x64: download the matching .zip from the GitHub Release,
# verify its .sha256 sidecar, then extract sodapop.exe into a PATH directory.
```

The release workflow produces the exact npm packages and Homebrew formula
needed for those channels; publishing the external tap and npm scope requires
their ownership and registry configuration. Direct archives remain the
fallback for Windows and other environments. Package-manager installs do not
require Go, Node.js (except for the npm launcher), or a separate Copilot
installation. The exact asset, checksum, manifest, and installed-command
checks are defined in
[distribution testing](docs/distribution.md). Windows MSI, WinGet, and Scoop
publication have additional native installation and ownership gates; generated
installer metadata alone does not mean a public channel is available.
See [Windows delivery](packaging/windows/README.md) for verified ZIP extraction,
per-user MSI recipes, and WinGet/Scoop manifests.

### Native sign-in prerequisite

Sodapop uses its **own registered GitHub OAuth client**, with device flow enabled. Configure its **public Client ID** at launch:

```sh
export SODAPOP_GITHUB_CLIENT_ID="your_registered_public_client_id"
sodapop
```

A distributor can set the same variable at build time to bake the public ID into the executable. **Never supply a client secret or access token in this variable.** Do not copy another application's client ID.

Sign-in stays inside Sodapop: it displays a verification code/link and waits while you authorize in the browser. Sodapop stores credentials in the macOS Keychain, Linux Secret Service, or Windows Credential Manager. Session-only sign-in is an explicit alternative when secure persistence is unavailable; there is no plaintext fallback.

**Current release gate:** a Sodapop-owned client ID and a successful real Copilot entitlement/session check are required before native sign-in can be considered qualified. Without a client ID, the interface and local commands remain available, but the application does not pretend to be connected. See [authentication setup](docs/authentication.md).

## Commands

Type `/` to discover commands, then filter, navigate with arrows, and complete with Tab.

| Command | Purpose |
|---|---|
| `/help [command]` | Commands and keyboard shortcuts |
| `/login` | Sign in to GitHub or reconnect Copilot |
| `/logout` | Sign out while keeping conversation history |
| `/model [id]` | Choose an available Copilot model |
| `/clear` | Abandon the current conversation and start fresh without deleting history |
| `/resume [id]` | Resume a Sodapop session for this account and project |
| `/context` | Show context-window token usage and visualization |
| `/compact [focus instructions]` | Summarize older model context while keeping the visible transcript |
| `/plan [prompt]` | Enable advisory planning; `/plan off` returns to ordinary conversation |
| `/allow-all` | Approve every tool request in this conversation; `/allow-all off` restores prompts |
| `/diff [all\|staged\|unstaged]` | Inspect the current working tree without modifying it |
| `/theme [name]` | Appearance, contrast, motion, and personality preferences |
| `/exit` | Shut down gracefully |

Planning is **advisory, not read-only**: normal tool approvals still apply. The diff view includes pre-existing changes, not only changes made by Sodapop. Opening Sodapop starts fresh; resuming is always explicit.

`/compact` asks the current model to summarize older conversation context and may consume model tokens. Optional focus instructions tell the summary what to preserve. Sodapop keeps the rendered transcript visible and reports the context reduction when compaction completes.

The composer stays one line tall until a wrapped or multiline draft needs more room. Enter sends, Ctrl+J adds a newline, and Shift+Tab switches between normal chat and advisory plan mode; the composer border, prompt and cursor turn amber in plan mode and cyan in chat mode. A bubble to the left of the `chat>` / `plan>` prompt pulses while Sodapop is working (it stays still when reduced motion is on). Use the mouse wheel, PgUp/PgDown, or Alt+Up/Alt+Down to review conversation history; scrolling up pauses live-follow and Ctrl+End returns to current output. The wheel also scrolls long dialog details. Escape dismisses menus, and Ctrl+C cancels active work. Use `/login` for account actions, `/logout` to sign out, and `//` at the start of a message to send a literal leading slash rather than a command. The in-app help describes available actions.

<details>
<summary>Watch: command discovery and advisory planning</summary>

<p><img src="docs/assets/demos/commands.gif" width="800" alt="Filter the slash-command palette, complete with Tab, and preserve an unsent draft while switching between amber plan mode and cyan chat mode." /></p>

[Static command-palette image](docs/assets/demos/commands.png). Planning is advisory, not a read-only permission boundary.

</details>

Wide terminals show a right sidebar with a static three-row Sodapop wordmark surrounded above, below, and on both sides by colorful diagonal fields, plus independently scrollable workspace, model, MCP server, and skill details. The title and surrounding fields share a continuous pink-to-purple gradient, place the current version directly above the title, and fall back to compact or ASCII-safe art when terminal space or capabilities require it. Active-work motion remains beside the chat composer rather than in the sidebar. Green capability dots mean active and red dots mean inactive. F3 focuses the sidebar for keyboard scrolling and Esc returns to the composer. The sidebar automatically disappears when the terminal narrows so conversation and composer space always take priority.

Sodapop includes Neon Arcade, Graphite, Midnight, High Contrast, and Daylight themes. Graphite is the most restrained option; Neon Arcade keeps the product's color identity while reserving saturated color for active and semantic accents. Terminal font selection belongs to the terminal emulator rather than Sodapop; a modern monospace font with clear punctuation and Unicode coverage produces the best result.

<details>
<summary>Watch: Neon Arcade, Graphite, and Daylight</summary>

<p><img src="docs/assets/demos/themes.gif" width="800" alt="Change Sodapop's appearance from Neon Arcade to Graphite and Daylight, then return to Neon Arcade." /></p>

[Static Daylight image](docs/assets/demos/themes.png).

</details>

`/theme` also offers quiet, playful, and extra personality modes. Personality can add light carbonation and reactions to interactions, contextual startup and recovery copy, and a bottle-cap recap in exit confirmation. The **Perfect Pour** celebration is deliberately conservative: it appears only after Sodapop recognizes a successful validation result, not merely a completed response. Reduced motion, no-color, and ASCII settings remain independent, so personality never requires animation, color, or Unicode.

## Permissions and privacy

<details>
<summary>Watch: inspect staged and unstaged changes without modifying them</summary>

<p><img src="docs/assets/demos/diff.gif" width="800" alt="Inspect prepared staged and unstaged changes in a small Go project, then scroll the combined read-only working-tree diff." /></p>

[Static diff image](docs/assets/demos/diff.png). The example changes are prepared fixtures, not edits made by an AI session.

</details>

Only structured reads contained in the canonical project directory are approved automatically by default. File edits, shell commands, outside-project reads, and external tool access offer **Deny**, **Allow once**, and **Allow all**. Allow all applies only to the current conversation and can also be toggled with `/allow-all` and `/allow-all off`. An approved shell command is **not sandboxed**.

Cancellation does not roll back completed file changes. Sodapop never automatically stages, resets, commits, or pushes the worktree.

Conversations are persisted by the Copilot runtime in Sodapop-scoped account state, separate from the default Copilot configuration. Sodapop does not import your existing MCP servers, plugins, or skills by default. OAuth/Copilot connections use GitHub services and normal Copilot entitlement and usage rules; see [architecture](docs/architecture.md) for boundaries.

## Terminal options

```sh
sodapop --help
sodapop --version
sodapop --no-color
sodapop --reduced-motion
sodapop --ascii
sodapop --no-banner
sodapop --check-runtime
```

`NO_COLOR` is also respected. No Nerd Font is required. The runtime check performs a local process/protocol handshake without authenticating or opening a model session.

## Development and packaging

```sh
make test
make distribution-test
make coverage
make check
make runtime-smoke
SODAPOP_TARGET=linux/amd64 SODAPOP_VERSION=0.0.2 make package
```

Windows builds use `bin/sodapop.exe` by default. Cross-compilation checks build tags and packaging, but Windows ACL enforcement, session locking, Credential Manager access, and the bundled runtime handshake must run on a native Windows x64 runner.

Normal tests are credential-free. `make coverage` enforces both the committed total statement-coverage baseline and package floors for `internal/app`, `internal/engine`, and handwritten `internal/runtimebundle` code. Generated embedded-runtime sources are excluded from the runtime package floor but remain compiled and covered by native smoke checks. `make check` adds race testing and vet. Feature, bug-fix, and observable behavior changes should add or update focused tests in the same change. If the full suite genuinely raises coverage, run `make coverage-baseline` and review the baseline increase; the command refuses to keep or lower the existing value.

`make demos` regenerates the README GIFs and PNG alternatives using a pinned VHS recorder and an isolated, signed-out instance of the real UI. Use `make demos SODAPOP_DEMO=themes` to regenerate one clip. See the [recording guide](docs/vhs/README.md) for dependencies, fixture isolation, and tape editing.

`make brand` regenerates the animated title and its static alternative from the existing wordmark and mascot. This separate artwork workflow uses Pillow, not VHS or a live AI session. See [animated branding](docs/branding.md).

`make runtime-smoke` requires `make bundle` first. The release-candidate workflow runs it on every supported native target. A native-runtime handshake or cross-compilation is not a substitute for real OAuth/Copilot testing on a supported platform.

For an explicit live check using your existing Sodapop sign-in and local public-client configuration:

```sh
make qualify SODAPOP_LIVE_MODEL="your model ID or exact model name"
```

This opts in to normal Copilot usage and a disposable fixture workspace; it is never part of ordinary CI. See [live qualification](docs/live-qualification.md) for prerequisites, scope, and exact completion criteria.

`SODAPOP_TARGET`, `SODAPOP_OUTPUT`, and `SODAPOP_VERSION` control builds. The version defaults to `dev`; the output defaults to `bin/sodapop` and is also used by `make start`, `make run`, and `make install`. Packaging selects its own output in fresh temporary staging, leaving existing files in `dist/` out of the archive.

Candidate archives are named `dist/sodapop-<version>-<goos>-<goarch>.tar.gz`
on macOS/Linux and `.zip` on Windows. Each has a matching `.sha256` sidecar and
a same-named top-level directory containing `sodapop` or `sodapop.exe`.
The release manifest binds the version, commit, SDK/runtime pins, archive hash,
and executable hash. Archives retain documentation and all required license
notices; environment files and unrelated staging content are excluded.

An owned Homebrew tap can generate its formula from the checksums attached to an exact tagged release; the template and stale-formula check are documented in [Homebrew tap packaging](packaging/homebrew/README.md). This repository does not claim or configure `homebrew-core` distribution.

The tagged release workflow takes the public Client ID from the repository
Actions variable `SODAPOP_GITHUB_CLIENT_ID`. It exercises the archived commands
and package-manager installations on native runners before preparing a draft
release and exact-version channel packages. It does not bypass signing,
notarization, registry ownership, or owner approval for publication.
The separate native-candidate workflow uses explicit test-only versions and a
fixture client ID for installation mechanics, never real sign-in or publication.
Keep the existing client ID and OAuth grants; Sodapop's original code is licensed
under [MIT](LICENSE), while the bundled Copilot runtime retains its separate terms.

For a local native archive with a generated manifest:

```sh
make native-install-test SODAPOP_VERSION=0.0.2 SODAPOP_RELEASE_DIR=dist
```

After a release is actually public, exercise its unauthenticated download:

```sh
make public-download-test SODAPOP_VERSION=0.0.2
```

These commands verify and run the installed executable without an ambient
Copilot installation or sign-in. They fail when an asset or prerequisite is
missing; a fixture suite passing is not substituted for a native installation.

## Brand assets

Sodapop's visual identity pairs its smiling soda-can mascot and silver pull tab with a pink-to-purple gradient and cyan accents. Use the supplied artwork rather than a GitHub or Copilot logo to represent Sodapop.

| Use | Asset |
|---|---|
| OAuth application logo | [512 x 512 PNG](images/sodapop-oauth.png); badge background `#09090B` |
| Repository social preview | [1280 x 640 PNG](images/sodapop-repo-social.png) |
| Animated README title | [GIF](images/sodapop-title.gif) or [static PNG](images/sodapop-title.png) |
| Static wide banners | [PNG](images/sodapop-banner.png) or [SVG](images/sodapop-banner.svg) |
| Main brand image | [PNG](images/sodapop-main.png) or [SVG](images/sodapop-main.svg) |
| Transparent website mascot | [SVG](images/sodapop-mascot.svg), [PNG](images/sodapop-mascot.png), or [WebP](images/sodapop-mascot.webp) |
| Website logo | [For dark backgrounds](images/sodapop-lockup-on-dark.svg) or [for light backgrounds](images/sodapop-lockup-on-light.svg) |

The [images directory](images/) also includes browser icons, a landing-page social card, and editable vector masters. Open `images/index.html` locally for the visual gallery and usage guidance.

## Website development

The landing page and documentation hub live in the private `site/` package,
independently of the Go application and CLI installer packages:

```sh
npm --prefix site ci
npm --prefix site run dev
```

The website reuses selected documentation and artwork; it does not publish the
checkout or rebuild the bundled Copilot runtime. Its Pages workflow keeps
pre-release availability explicit and deploys only `site/dist/`. See the
[website development guide](https://github.com/VeVarunSharma/sodapop/blob/main/site/README.md)
for content, release metadata, and deployment configuration.

## Boundaries

The first release supports macOS, glibc-based Linux, and Windows x64 local coding. Windows ARM64, BYOK, explicit MCP/skills/plugin management, fleet orchestration, remote sessions, IDE integration, and automatic rollback are not included.

Sodapop is an independent application, not the official GitHub Copilot CLI. See [third-party notices](THIRD_PARTY_NOTICES.md).

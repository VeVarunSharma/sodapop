# Recording Sodapop's README demos

The README media is generated with [Charm VHS](https://github.com/charmbracelet/vhs).
The framing takes inspiration from [Crush](https://github.com/charmbracelet/crush):
a short, uncluttered product overview followed by focused feature demonstrations.
All artwork and application output come from Sodapop.

## Prerequisites

- Go 1.27.1 or later and Git.
- VHS **0.11.0**, pinned in `docs/vhs/vhs-version`.
- `ttyd`, `ffmpeg`, and a Chrome/Chromium browser usable by VHS.
- The **Menlo** font for the committed macOS captures. Other systems need an
  installed monospace font selected in `common.tape`; review wrapping after changing it.

On macOS, `brew install vhs` installs VHS, ttyd, and ffmpeg. If Homebrew's version
differs from the pin, use the matching [VHS release](https://github.com/charmbracelet/vhs/releases/tag/v0.11.0)
instead of silently regenerating with a different recorder. VHS can use an existing
Google Chrome installation; browser/dependency setup may require network access.

## Render

From the repository root:

```sh
make demos
make demos SODAPOP_DEMO=overview
make demos SODAPOP_DEMO=commands
make demos SODAPOP_DEMO=themes
make demos SODAPOP_DEMO=diff
```

The command builds a native development-only launcher, creates a fresh temporary
Git repository for each clip, validates the tapes, and renders GIFs plus PNG stills.
It does not call `make build`, download the Copilot runtime, or require a Sodapop
sign-in. Go may download dependencies if they are not already cached.

Outputs go to `docs/assets/demos/`. The overview uses a wide 16:9 terminal to show
the sidebar; focused recordings use larger text and Sodapop's narrower layout.
Shared settings live in `common.tape`: Menlo, a dark terminal canvas, no decorative
window chrome, 25 fps, and 45 ms typing intervals. The themes clip changes the
application theme, not the terminal palette.

| Tape | Content | PNG still |
|---|---|---|
| `overview.tape` | Mascot, command completion, Chat/Plan/Autopilot cycle, and a scrolling diff | Resting mascot and sidebar |
| `commands.tape` | Vending-machine discovery and an unsent draft preserved across all composer modes | Curated vending-machine launcher |
| `themes.tape` | Neon Arcade, Graphite, and Daylight | Daylight |
| `diff.tape` | Staged, unstaged, and combined changes | Unstaged diff |

## Isolation and truthful demos

`scripts/recording` constructs the **production `internal/ui` model** with the real,
read-only workspace service. Its signed-out authentication adapter cannot return
credentials or start device flow. There is no engine factory, preference writer,
session replay, or simulated AI response. The sidebar labels the version `demo`,
and the interface remains visibly signed out.

This separate launcher matters on macOS: changing `HOME` does not isolate Keychain.
The production `sodapop` executable and its authentication behavior are unchanged.
The launcher's preferences are deliberately in-memory, so each clip starts fresh;
the normal application still persists appearance choices.

The recording script gives each capture an allowlisted environment, fresh home,
config and state directories, and a disposable `soda-shop` repository. Staging
defaults to `/tmp`, avoiding macOS's host-specific user-cache path in the sidebar.
`SODAPOP_DEMO_TMPDIR` can select another existing absolute temporary directory;
that path can appear in the overview, so choose a public-safe location. The recording
process does not inherit OAuth tokens, public client IDs, shell startup files, user Git configuration,
or `NO_COLOR`. Git hooks and signing are disabled for fixture setup. The caller's
project, settings, keychain, Git index, and sign-in are not modified.

The three `fixtures/main.go.*` files define the initial commit, staged change,
and additional unstaged change. These are prepared example edits, **not AI output**.
Planning is advisory; the recording does not claim it makes tools read-only.
The overview and commands clips briefly show the Autopilot composer state but do
not connect an engine, approve a tool, or claim that approved commands are sandboxed.

## Editing and publishing

Use screen-scoped waits with explicit timeouts for state transitions, and sleeps
only for presentation pacing. Hide launch/exit commands, but do not hide errors or
present failed interactions as successful. Keep the overview's opening animation;
focused clips use reduced motion and start with the resting mascot.

Aim below 2 MB per GIF. The script rejects GIFs over 3 MiB, missing outputs, and
failed renders before replacing any published assets. Temporary fixtures, browser
files, and the launcher are cleaned up on exit. It never uploads media.

Review the GIFs at the README's 800 px display width, including the first frame,
loop boundary, text wrapping, sidebar, mode colors, and diff contents. Keep the PNG
alternatives and alt text alongside the animations. Capture timing and font
rasterization can vary slightly by host; byte-identical GIFs are not required.

Credential-free workflow tests run with:

```sh
go test ./scripts/recording ./scripts -run '^(TestRecording|TestMakeDemos|TestDemo)'
make check
```

Rendering is intentionally manual, not an automatic CI commit or a live Copilot
qualification step.

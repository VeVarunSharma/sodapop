# Commands and shortcuts

Type `/` at the beginning of the composer to open the command palette. Continue
typing to filter, use the arrow keys to choose, and press Tab to complete a
command before adding its arguments. `/help` and `/help command` work without
signing in.

## Slash commands

| Command | Purpose |
| --- | --- |
| `/help [command]` | Show commands, usage, and keyboard shortcuts. |
| `/login` | Sign in to GitHub or reconnect Copilot. |
| `/logout` | Disconnect and sign out, keeping conversation history. |
| `/model [id]` | Choose a model available to the connected account. |
| `/clear` | Start a fresh conversation without deleting prior history. |
| `/resume [id]` | Resume a Sodapop conversation for this account and project. |
| `/context` | Show available context-window usage and its visualization. |
| `/compact [focus instructions]` | Summarize older model context while keeping the visible transcript. |
| `/plan [prompt]` | Enable advisory planning; `/plan off` returns to ordinary chat. |
| `/allow-all` | Approve every tool request for this conversation; `/allow-all off` restores prompts. |
| `/diff [all\|staged\|unstaged]` | Inspect the whole working tree without modifying it. |
| `/theme [name]` | Choose appearance, personality, contrast, and motion settings. |
| `/exit` | Shut down gracefully, confirming interruption when needed. |

Command names are case-insensitive. A command is recognized only at byte zero
of the composer, not after leading spaces, on a later line, or inside ordinary
text. Start a message with `//` to send one literal leading slash:
`//path` sends `/path`. Slashes elsewhere are unchanged.

## Conversation controls

`/plan` is **advisory, not read-only**. A prompt supplied after `/plan` keeps its
freeform spacing. Switching modes does not weaken approval rules.

`/compact` asks the current model to summarize older conversation context and
may consume model tokens. Optional focus instructions describe what the
summary should preserve. The rendered transcript stays visible even though
the model's context is reduced.

`/allow-all` removes per-tool prompts only for the current conversation. It
does not answer the agent's questions and turns off when you start or resume
a conversation. Approved shell commands are not sandboxed. Read
[permissions and privacy](permissions-and-privacy.md) before using it.

Help, context, theme, diff, allow-all, logout, and exit remain available while
work is active. Finish or cancel active work before signing in, changing
models, changing conversations, compacting, or changing planning mode.
Unavailable commands are not queued for a later automatic attempt.

## Keyboard and mouse

| Input | Action |
| --- | --- |
| Enter | Send the composer, or choose the highlighted palette/dialog action. |
| Ctrl+J | Insert a newline in the composer. |
| Shift+Tab | Toggle chat/planning when the composer is focused and the palette is closed. |
| Tab / arrow keys | Complete and navigate palette choices. |
| Esc | Close a menu or return from a focused panel. |
| Ctrl+C | Cancel active work; completed edits remain on disk. |
| Ctrl+P | Open the local action palette. |
| F1 | Open help. |
| F3 | Focus the sidebar when it is visible; Esc returns to the composer. |
| F4 | Focus tool cards; Enter or Space expands a selected card. |
| PgUp / PgDown | Scroll conversation history. |
| Alt+Up / Alt+Down | Scroll history in smaller steps. |
| Ctrl+Home / Ctrl+End | Jump to the oldest/current output. |
| Mouse wheel | Scroll conversation history or the panel/dialog under the pointer. |
| Ctrl+Q | Open exit confirmation. |

Scrolling up pauses live-follow. Ctrl+End returns to current output and resumes
following new messages. Some terminal emulators reserve function or modified
keys; the slash commands remain the reliable alternative.

![The command palette and composer in a signed-out local demonstration.](assets/demos/commands.png)

This is a still from a real signed-out UI recording, not a live AI interaction.
For theme identifiers and terminal flags, see [customization](customization.md).
The contributor-facing [command catalog](../internal/commands/registry.go)
is the application source for names and parsing rules.

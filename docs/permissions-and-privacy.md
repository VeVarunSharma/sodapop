# Permissions and privacy

Sodapop can help inspect and change a project, but its UI is not a sandbox.
Understand an action before approving it, and review the resulting changes.

## Approval defaults

Only structured reads that resolve inside the canonical project directory may
be approved automatically by default. File writes, shell commands, reads outside
that directory, URLs, external tools, unknown requests, and sandbox-bypass or
enterprise-managed requests need an explicit decision.

An approval dialog offers Deny, Allow once, and Enable Autopilot. Deny is appropriate
when the requested operation is unclear or exceeds the task. Allow once approves
that request without broadly trusting later actions.

**An approved shell command is not sandboxed.** It can have the same filesystem
or network effects as running it yourself. A safe-looking command label is not
a substitute for inspecting its arguments and scope.

## Planning and Autopilot

`/plan` provides an advisory planning focus. Shift+Tab cycles Chat, Plan, and
Autopilot; these modes are mutually exclusive. Planning is not read-only and
does not prevent writes after approval. Normal permission rules still apply.

`/autopilot` approves every tool request in the current conversation without
another per-action prompt. F2 provides the same toggle, and `/allow-all` remains
a compatibility alias. Use Autopilot only when you understand and trust the
scope of the requested work. It does not answer the agent's questions.

Use `/autopilot off`, F2, or Shift+Tab from Autopilot to return to Chat and
restore prompts. Starting or resuming a conversation also turns Autopilot off;
it is not a saved global preference.

## Workflow guidance and evidence

`/fizz` asks the model to compare ideas without beginning implementation.
`/taste-test` asks it to run focused checks and return a report without applying
fixes. Those instructions shape the request; they do not grant or remove tool
permissions. Any tool request still follows normal approvals or the current
Autopilot setting.

Taste Test uses the bounded conversation baseline behind `/diff session`.
Missing, partial, unreadable, or additionally truncated evidence is reported
instead of silently falling back to the whole working tree. Its PASSED, FAILED,
and UNVERIFIED sections should be read as evidence from the checks that actually
ran, not a guarantee that every path or requirement was covered. A Perfect Pour
celebration requires recognized successful validation commands, but remains
presentation rather than proof.

## Cancellation and file changes

Ctrl+C cancels active work and releases pending decisions. It does **not**
roll back completed edits or undo the effects of a command that already ran.
An interrupted or failed request is not automatically replayed.

Use `/diff` to inspect the whole working tree afterward. That view includes
pre-existing changes, staged changes, and unstaged changes; it is not a log
of only Sodapop's edits. Large diffs may be explicitly marked as truncated.

Sodapop does not automatically stage, reset, commit, or push your worktree.
Keep your own version-control and backup practices.

## Accounts and local state

Sodapop uses its own GitHub OAuth device flow. Persistent credentials belong in
macOS Keychain, Linux Secret Service, or Windows Credential Manager. If the
secure store is unavailable, you may explicitly choose session-only sign-in or
cancel. There is no plaintext-token fallback.

Session-only credentials last for the process. `/logout` disconnects the account
and removes Sodapop's saved credential, but keeps conversation history. Exiting
normally is not the same as signing out.

Conversation state is persisted by the bundled runtime under Sodapop's
account-scoped state directory. Resume is limited to the signed-in account and
canonical project; it is explicit rather than automatic. Preferences are stored
separately from tokens.

The application does not fall back to ambient GitHub credentials, a separately
installed Copilot executable, or ordinary Copilot session state. It does not
import existing MCP servers, plugins, or skills. Explicit stdio MCP definitions
are stored in the signed-in account's private Sodapop state; environment entries
store variable names rather than values. Servers start only after enablement and
reconnection, and every MCP tool invocation requires the same explicit decision
as other external tools unless Autopilot is enabled for that conversation.

Explicit skills are stored separately in private, content-addressed Sodapop
state. Bundled skills are trusted by default but remain disabled until selected
for a project. Local directories and public Git repositories must be trusted
explicitly before import; Git URLs containing credentials are rejected and
interactive credential prompts are disabled. Project activation stores exact
content digests, and saved conversations retain their digest set. A skill can
shape model instructions, but it cannot authorize edits, shell commands, URLs,
MCP calls, or any other tool request.

## Connected-service boundaries

Sodapop is not an offline model. Connected coding requests use GitHub's Copilot
services; prompts, relevant project context, and tool results can be involved
in those requests. Copilot entitlement, usage rules, and organization policies
still apply. Signing in to GitHub alone does not prove model access.

Do not paste secrets into prompts or include them in diagnostics. Before sharing
an issue, screenshot, or recording, remove tokens, device codes, personal paths,
and confidential project content.

The website's demonstrations are real local UI recordings made while signed
out, with prepared Git changes. They do not demonstrate a live model response
or establish anyone's Copilot access.

For implementation boundaries, see [architecture](architecture.md). For account
problems, see [troubleshooting](troubleshooting.md#sign-in-and-copilot-access).

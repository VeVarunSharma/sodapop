# Troubleshooting

Start with the exact error and the command you installed. Avoid deleting state
or changing credentials just to make an error disappear.

## Installation or command not found

Check the [download page](/download/) before assuming a package-manager channel
has been published. The [installation guide](installation.md#supported-platforms)
lists the five native targets. Homebrew is scoped to macOS/Linux. Windows x64
npm support depends on the exact release manifest including its qualified ZIP
and the matching `@sodapop-sh/windows-amd64` package version being published.

For a manually extracted archive, confirm that the directory on your `PATH`
contains `sodapop` or, on Windows, `sodapop.exe`. Run the executable by its full
path to distinguish a PATH problem from a startup problem. Check whether another
installation channel already owns the command before replacing it.

A wrong-architecture executable or a Linux build on an unsupported C library
is not fixed by changing OAuth settings. Choose the matching supported target.
If the operating system blocks an archive, check its origin and integrity;
do not disable security protections.

For npm, distinguish an unavailable platform from a missing native payload.
If the installed launcher's version does not declare Windows, do not force a
Windows package into it or rely on the development template's platform list.
Use a confirmed matching native ZIP when available instead.

If that version declares your platform but its payload is missing, reinstall
from the confirmed channel with optional dependencies enabled. Launcher and
native package versions must match. The launcher does not download a missing
payload on first run, and a hash mismatch is not a reason to bypass verification.

## Noninteractive terminal or runtime errors

The conversation interface needs an interactive terminal for both input and
output. Piping its output to a file is not a supported headless chat mode.
These diagnostic commands work without an interactive session:

```sh
sodapop --help
sodapop --version
sodapop --check-runtime
```

The runtime check starts the bundled runtime and performs a local protocol
handshake. It does not authenticate or call a model. Record the full version
output when reporting a runtime mismatch.

If the bundle is missing or invalid in a source build, return to the
[build instructions](development.md#build-and-run). Installing a different
`copilot` command is not a supported fallback. For an installed archive, verify
the matching release rather than substituting an unrelated runtime.

## Sign-in and Copilot access

Use `/login` inside Sodapop. Authorize the displayed application and code in
your browser, and then wait for the terminal's connection result.

| Symptom | Next step |
| --- | --- |
| Device code expired or authorization was canceled | Start a new `/login` attempt; do not reuse or share the old code. |
| Authorization was denied | Review the application and account you intend to authorize before trying again. |
| Network failure | Restore access to the required GitHub services, then retry the account connection explicitly. |
| GitHub sign-in succeeded but models are unavailable | Confirm Copilot access and organization policy for that account. GitHub identity alone is insufficient. |
| Missing Sodapop public client ID | Use a properly configured distribution or follow the source-build configuration below. |
| Expired or revoked credential | Reauthorize through `/login`; do not paste tokens into a preferences file. |

Normal users do not need to create an OAuth app. Local source builds use the
project's existing public-client configuration as described in
[development](development.md#configure-source-build-sign-in).
Never borrow GitHub CLI or Copilot CLI credentials as a workaround.

Failed coding actions are not replayed automatically after account changes.
Inspect any completed work, then decide whether to send a new request.

## Secure credential store unavailable

Persistent sign-in needs macOS Keychain, Linux Secret Service, or Windows
Credential Manager. A headless or minimal desktop session may not have a usable
secure store.

Choose the explicit session-only option if that fits your environment, or cancel
and restore the operating system's credential-store service. Session-only
sign-in lasts for this process. Sodapop does not fall back to plaintext storage.

## Preferences, history, or display look wrong

Use `/theme` for supported appearance choices. Try `--ascii`, `--no-color`, or
`--reduced-motion` for terminal compatibility; see [customization](customization.md).
The terminal emulator, not Sodapop, selects fonts.

If startup reports invalid preferences, retain a backup of the named file before
repairing it. The schema is versioned and rejects unknown fields. Do not put a
credential in that file, and do not delete account state as a display fix.

`/resume` lists only the current account's conversations for the current canonical
project. Check the account and launch directory before treating another session
as lost. `/clear` starts fresh but does not delete prior history.

## Work appears busy or a diff is unexpected

Scrolling up pauses live-follow; Ctrl+End returns to current output. A reduced
motion setting keeps activity decoration still. Read tool details and pending
permission or question dialogs before assuming the application is stalled.

Ctrl+C cancels active work, but does not undo completed file changes. `/diff`
includes changes from before the conversation, and reports when its bounded
output is truncated. Review the actual worktree before retrying a request.

## Report a reproducible problem

Use the [canonical repository](https://github.com/VeVarunSharma/sodapop) to report
the operating system, architecture, installation channel, version output, exact
error, and minimal steps. Say whether the problem happens while signed out or
only after connecting.

Remove tokens, private device codes, personal paths, and confidential source or
conversation content. Do not attach a whole state directory or environment file.

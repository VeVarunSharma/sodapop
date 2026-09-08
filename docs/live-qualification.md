# Opt-in live qualification

`internal/integration` contains a credential-free policy suite and one explicitly
opt-in test, `TestLiveQualification`. Preparing this harness, or seeing its normal
`SKIP`, is **not live OAuth/Copilot qualification**. Local configuration exists;
its contents and registration/access behavior have not been inspected or exercised
as part of preparing the harness.

## Prerequisites and consent

Use an owner-approved account eligible for Copilot and permitted by the relevant
organization policies. The configured public OAuth client ID must belong to Sodapop,
have device authorization enabled, and match the existing Sodapop sign-in.

Start `sodapop`, run `/login`, and choose secure persistent storage beforehand.
The test restores only the currently signed-in Sodapop account from macOS Keychain,
Linux Secret Service, or Windows Credential Manager using `auth.New`, `Current`, and an account-bound
`TokenForAccount` source. Session-only sign-in in another process is not available
to the test. Keep that account unchanged until the test finishes.

The test never starts device authorization, opens a browser, registers an app,
borrows another application's identity, or uses `GH_TOKEN` / `GITHUB_TOKEN`.
Do not copy or paste tokens, client secrets, device codes, or OAuth payloads into
commands, files, logs, or this test. Only the **public** client ID is configuration.
The harness never reads `.sodapop.env`; the existing Make entry point loads it.

Running the opt-in command authorizes **one short synthetic coding prompt** and
normal GitHub/Copilot service traffic. It consumes normal Copilot usage, including
any model-specific premium usage or charges. There is no automatic retry or model
substitution. Obtain authorization for that usage before running it.

| Setting | Required behavior |
| --- | --- |
| `SODAPOP_LIVE_QUALIFY` | Must be exactly `1`; anything else skips before credential access. |
| `SODAPOP_GITHUB_CLIENT_ID` | Nonempty public Sodapop OAuth client ID, matching the saved sign-in. |
| `SODAPOP_LIVE_MODEL` | Exact available model ID or unique case-insensitive runtime catalog display name. No fuzzy match or default. |

An opted-in run with either prerequisite missing **fails**, rather than skipping.
Unavailable credentials/keyring, models, entitlement, runtime compatibility,
policy, or network also fail. Credential failures direct the operator to Sodapop
`/login`; the test never attempts interactive reauthorization. Backend error
payloads and actual account identity are deliberately suppressed.

## Commands

Normal, credential-free checks (the explicit `0` also overrides an inherited opt-in):

```sh
SODAPOP_LIVE_QUALIFY=0 go test ./internal/integration
SODAPOP_LIVE_QUALIFY=0 go test -race ./internal/integration
```

After explicit usage authorization, with the existing local public-client
configuration and an explicitly selected model:

```sh
make qualify SODAPOP_LIVE_MODEL='<exact-model-id-or-display-name>'
```

Model IDs are case-sensitive and take precedence. Otherwise the entire display
name is matched case-insensitively against `engine.Models` and must identify
exactly one entry. For example, `SODAPOP_LIVE_MODEL='GPT-5.6 Luna'` resolves to the
matching entry's actual ID; an absent or ambiguous name fails without guessing an
ID or selecting another model.

Alternatively, when the public client ID and model are already exported and the
existing pinned runtime bundle has been built:

```sh
SODAPOP_LIVE_QUALIFY=1 go test -count=1 -timeout=10m -run '^TestLiveQualification$' -v ./internal/integration
```

`-count=1` prevents cached test success from being mistaken for a new live run.
The total qualification context is seven minutes; each turn is bounded to two
minutes, other runtime/auth operations to 45 seconds, idle barriers to 20 seconds,
and each close/drain to 30 seconds. Native keyring APIs are synchronous and cannot
be interrupted by a Go context; an OS keyring prompt may require operator action.
The outer test timeout is a final bound, not a promise of cleanup after a forcibly
killed test process.

## Disposable scope and exact evidence

Two separate `t.TempDir` directories hold a synthetic project and runtime state
`Home`. Neither is the working repository or `~/.copilot`. The project contains
only `fixture.go`, a tiny `Answer` function returning `1`; the requested edit
changes precisely `return 1` to `return 2`. The engine is real `engine.New` /
`Start`, with the bundled runtime and explicit Sodapop token source, not mocked
stdout. The SDK may use its normal versioned binary extraction cache; that is
distinct from the disposable session/configuration state.

The single prompt requests `view`, the exact `edit`, and another `view`.
Approval is restricted to that existing regular file. The helper rejects aliases,
symlinks, directories, outside paths, shell commands, URLs, unknown tools/fields,
extra read/edit tool calls beyond the read/edit/read sequence, sandbox-bypass
warnings, and managed-approval requests.
Hook requests must match the adapter's exact hook envelope and validated JSON
arguments. Native `read`/`write` requests must correlate by tool ID to an already
identified fixture tool start or hook. Missing or changed metadata fails closed;
the harness does not guess from intention text. Unknown permission requests are
denied and questions canceled once. Sodapop's normal structured in-project reads may
be auto-approved by the engine before a permission event reaches the harness.
These checks are not an OS sandbox or permission for arbitrary project work.

A passing live run establishes all of the following for the selected model,
current account, platform, and pinned SDK/runtime combination:

- Existing Sodapop-issued credentials can start the runtime, list eligible models,
  and create a session using the exact selected catalog model.
- One prompt reaches explicit idle, with a matching user message, a nonempty
  assistant final, successful fixture reads before/after the edit, and a
  correlated successful edit following explicit approval.
- Actual file bytes match the expected edited code, with no extra project files.
  Assistant claims or tool text alone cannot make the test pass.
- Idle `Abort` returns successfully and produces the tagged `Name="aborted"`
  barrier before the turn, after it, and following cold resume.
- After closing/draining the first engine, a **new** engine using the same
  temporary state, canonical project, and bound account lists the created session
  and resumes its SDK history without sending another prompt. Known final-message
  and tool-completion source IDs, message/tool IDs, roles, and contents survive.
  The fixture remains unchanged by resume.

Events are subscribed before session creation/sends. Send acknowledgment alone
does not finish a turn; errors, failed tools, missing evidence, stream closure,
and deadlines fail explicitly. All engines are closed and drained on success or
failure; `Close` owns cancellation of unread/pending callbacks. No event payloads,
tokens, account identity, model responses, or environment contents are printed or
saved as qualification artifacts.

## What this does not establish

This deliberately small, one-prompt smoke test does **not** prove a denied edit
was attempted and left the file unchanged against the real backend. Denial and
callback policy are covered deterministically with synthetic fixtures, not
claimed as live rejection evidence. It also does not exercise new OAuth device
authorization, registration settings or minimum scopes, mid-turn revocation,
active-turn cancellation, shell execution, a second turn after resume, the TUI,
all models/platforms, or general release readiness.

Do not mark OAuth, live-experience, runtime-spike, or release blockers complete
merely because this harness exists or ordinary tests pass. Only an authorized,
uncached opted-in success establishes the narrow live behaviors above; any
remaining requirements still need their own qualification.

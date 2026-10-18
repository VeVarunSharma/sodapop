# Facilitator demo guide

**Status: preparation draft, not a live qualification or catalog acceptance.**
This guide demonstrates a small, reviewable Go change with Sodapop, an independent
terminal companion powered by the GitHub Copilot SDK. Rehearsal timings are
budgets, not service latency promises. No live OAuth or Copilot checks were run
to prepare this guide. Use the [engagement guide](engagement.md) for discovery and
pilot planning.

## Outcome and journey

| Stage | Facilitator action | Evidence to show |
| --- | --- | --- |
| Input | Supply the synthetic [orders sample](sample/README.md) and its acceptance criteria. | `order.go`, baseline tests, and a clean disposable Git baseline. |
| Context | Launch from the sample copy; ask Sodapop to inspect both files. | The assistant identifies that `TotalCents` ignores `Quantity`; baseline tests cover only quantity one and empty input. |
| Workflow | Request advisory planning, then explicitly approve a bounded edit and checks. | A proposed approach, permission decision, and tool results; Autopilot remains off. |
| Output | Inspect implementation and newly added regression tests. | Diff plus observed `go test ./...` output; a passing baseline alone is not correctness evidence. |
| Next action | Agree a representative, authorized pilot task and success measures. | Named owner, scope, review gate, and [scorecard](engagement.md#pilot-scorecard). |

The intended fix multiplies each line's unit price by its quantity before summing.
Require new tests for quantities greater than one, mixed lines, and zero quantity,
while preserving quantity-one and empty-input behavior. Do not broaden this demo
into tax, discounts, currency conversion, or new dependencies.

## Preflight: before attendees arrive

1. Install a currently available distribution using the
   [installation guide](../installation.md) and
   [download page](https://sodapop.sh/download/). For a local source deployment,
   follow [development](../development.md) and
   [root build/install instructions](../../README.md#build-and-run).
   This is a local terminal application, not a hosted service deployment.
2. Have Git and Go installed for the sample's tests; use the repository's required
   Go version or later for a source build. Native Sodapop alone does not need Go,
   but this coding exercise does. Use a readable terminal and an unshared desktop.
3. Run the local checks below against the installed executable. A runtime
   handshake is not authentication, entitlement, or a model-response check.

   ```sh
   sodapop --version
   sodapop --check-runtime
   go version
   git --version
   ```

4. Follow [authentication](../authentication.md) using the existing Sodapop OAuth
   application and an owner-approved account allowed by organization policy.
   If needed, export only its existing **public** `SODAPOP_GITHUB_CLIENT_ID`;
   installed executables do not load the source checkout's `.sodapop.env`.
   Do not register a replacement app or borrow ambient GitHub/Copilot credentials.
   Secure persistent storage or explicit session-only sign-in is required; no
   plaintext credential fallback exists.
5. Obtain **separate explicit authorization** for any live sign-in, model prompts,
   service traffic, and applicable usage/charges. Agree the model, recording
   consent, and synthetic data scope. Preparing this pack is not that consent.
   Without it, show files and the clearly labeled signed-out fallback only.
6. With authorization, use `/login`, verify the displayed account privately, then
   `/model` to select an actually available model. A model list is not proof that
   the next request will succeed. Do not record device codes or account details.
7. Keep `SODAPOP_LIVE_QUALIFY` and `SODAPOP_RUNTIME_SMOKE` unset or `0` during
   routine preparation. The separate [live qualification](../live-qualification.md)
   harness needs its own authorization and establishes only its documented scope;
   it is not a prerequisite command to run automatically for this demo.

### Create an isolated sample copy

These are **operator-only shell instructions**, not prompts for Sodapop.
Run from the Sodapop checkout in a POSIX shell on macOS/Linux. Windows presenters
should prepare an equivalent disposable directory with their supported shell,
not paste POSIX paths into PowerShell. The source sample must remain untouched.

```sh
SODAPOP_SOURCE="/home/runner/work/sodapop/sodapop"
DEMO_DIR="$(mktemp -d /tmp/sodapop-orders.XXXXXX)"
cp "$SODAPOP_SOURCE/docs/accelerator/sample/order.go" "$DEMO_DIR/"
cp "$SODAPOP_SOURCE/docs/accelerator/sample/order_test.go" "$DEMO_DIR/"
cp "$SODAPOP_SOURCE/docs/accelerator/sample/README.md" "$DEMO_DIR/"
cd "$DEMO_DIR"
go mod init example.com/sodapop-orders
go test ./...
git init
git add -- README.md go.mod order.go order_test.go
git -c user.name='Demo Operator' -c user.email='demo@example.invalid' \
  -c core.hooksPath=/dev/null -c commit.gpgsign=false \
  commit -m "Prepare synthetic orders demo baseline"
git status --short
sodapop
```

Check each command's result before proceeding; stop on any failure. Confirm the
printed workspace is the new disposable directory, not the Sodapop repository,
and `git status --short` is empty. The operator explicitly creates this baseline
commit before launching; Sodapop must **never automatically stage, commit, or
push**. The `-c` options affect that invocation only, not user Git configuration.
There is no remote and no need to publish this repository.

Baseline tests intentionally pass despite a known bug: they do not yet test
multi-quantity or zero-quantity lines. Say this aloud before showing the result.
The assistant must add regression tests rather than claiming the existing tests
prove the fix.

## Five-minute flow

Complete installation, authorization, sign-in, model selection, and disposable
setup beforehand. If those are incomplete, use the fallback rather than rushing
consent or approvals.

| Time | Action and narration |
| --- | --- |
| 0:00–0:40 | Explain the problem and clean baseline. “We have a passing but incomplete test suite. We want a small fix with new evidence, not just a plausible explanation.” |
| 0:40–1:30 | Run the safety commands below, then the context/plan prompt. Explain the project boundary and advisory nature of planning. |
| 1:30–3:10 | Send the implementation prompt; inspect each requested path and command before approval. Show the added regression tests and test outcome. |
| 3:10–4:20 | Open `/diff session`; run the validation prompt if time permits. Separate observed checks from unverified claims. |
| 4:20–5:00 | Summarize the actual outcome, including failures. Ask for one pilot candidate, owner, and baseline measurement. |

### Exact commands and prompts

Enter these local slash commands first:

```text
/autopilot off
/plan off
```

Then send this planning prompt:

```text
/plan Read README.md, order.go, and order_test.go in this disposable orders project.
Explain why the existing passing tests miss the quantity bug. Propose the smallest
fix and regression cases for multiple quantities, mixed lines, and zero quantity,
preserving quantity-one and empty-input behavior. Do not edit or run shell commands
yet. Do not stage, commit, push, install dependencies, or access outside this project.
```

The no-edit request is model guidance, **not a sandbox**. Decline unexpected edit,
shell, network, or outside-project requests even while planning.

After discussing the proposal, return to ordinary chat and send:

```text
/plan off
```

```text
Implement the agreed TotalCents quantity fix in order.go. Add regression tests in
order_test.go for quantity greater than one, mixed lines, and zero quantity, and
retain the existing cases. First add the tests and run go test ./... to demonstrate
the regression; then fix the implementation, run gofmt -w order.go order_test.go,
and rerun go test ./.... Request normal approval for edits and commands. Work only
in this disposable project. Do not stage, commit, push, install dependencies, or
perform cleanup. Report the actual failed/passed checks and anything unverified.
```

Approve only understood edits to the two Go files and the stated formatting/test
commands, in the confirmed directory. If the assistant combines commands or
requests broader access, inspect the complete request; deny and restate the scope
when unsure. A command's stated intent does not constrain its actual effects.

Inspect the result:

```text
/diff session
```

Optional additional live validation prompt, within the authorized usage budget:

```text
/taste-test Review the quantity fix and its regression coverage against README.md.
Run go test ./... only with normal approval. Do not edit files or fix anything.
Distinguish observed passing, failing, and unverified evidence; identify missing
multi-quantity or zero-quantity coverage. Do not stage, commit, push, or use network.
```

`/taste-test` consumes model usage; it is not a deterministic local test shortcut.
Its narrative is not proof of a successful command. If time runs out, omit this
optional turn and explicitly mark it not performed.

## Fifteen-minute flow

| Time | Action and checkpoint |
| --- | --- |
| 0:00–2:00 | Use the [opener](engagement.md#opener) and ask which review bottleneck matters. State synthetic scope, consent, expected outcome, and safety limits. |
| 2:00–4:00 | Show sample inputs, acceptance criteria, baseline tests, clean status, selected model, and local project context. Never show credentials. |
| 4:00–6:00 | Set Autopilot off; send the context/plan prompt. Have an attendee predict a multi-quantity result and identify missing coverage. |
| 6:00–10:00 | Send the implementation prompt. Show an explicit permission decision, the newly failing regression test, the fix, formatting, and passing rerun if observed. Deny unrelated work. |
| 10:00–12:00 | Inspect `/diff session` and new tests; use `/taste-test` within consent/budget. Compare implementation, tests, and command output rather than relying on the summary. |
| 12:00–13:30 | Note the current session, use `/clear` then explicitly `/resume` it in the same account/project. Verify history without replaying the task. Discuss the limits of this synthetic example; record deviations and unfinished checks. |
| 13:30–15:00 | Fill pilot scope and baseline owners in the scorecard; agree a date and next action. Exit and explain cleanup/history boundaries. |

Do not spend the whole meeting waiting for a backend response. Set a facilitator
cutoff during rehearsal, cancel stalled work, and move to the labeled fallback.
Do not silently substitute models or retry after a cancellation or access failure.

## Safety, failure recovery, and cleanup

- Keep Autopilot off throughout. Default approvals are not an OS sandbox.
  Structured in-project reads may proceed without a prompt; edits and other
  operations need an explicit decision. Approved shell commands are unsandboxed.
- Plan mode is advisory, not read-only enforcement. No mode promises rollback.
  Ctrl+C cancels active work but **does not undo edits**. Inspect the filesystem
  and diff after cancellation; never assume the project is unchanged.
- If access fails, preserve the visible failure and use `/login` recovery guidance
  privately. Sign-in is separate from Copilot eligibility, policy, quota, and
  network availability. No automatic replay or background qualification.
- After a failed or canceled run, exit with `/exit`. For a repeat, discard only
  the disposable copy after inspection and create a fresh one using the setup
  above. Do not repair the source sample or the user's worktree, and do not use
  Git reset/clean as a demo recovery step.
- Before deletion, retain only explicitly approved, redacted evidence outside the
  disposable copy. Never export tokens, private device codes, account identities,
  full provider payloads, or customer code into the pack.

Operator-only cleanup, in the same shell with `DEMO_DIR` still set:

```sh
cd "$SODAPOP_SOURCE"
printf 'Disposable directory to remove: %s\n' "$DEMO_DIR"
```

Inspect that exact path and its contents first. Only after confirming it is the
created copy, run:

```sh
case "$DEMO_DIR" in
  /tmp/sodapop-orders.??????)
    test -d "$DEMO_DIR" && test ! -L "$DEMO_DIR" && rm -r -- "$DEMO_DIR"
    ;;
  *) printf '%s\n' 'Unexpected path; no cleanup performed.' ;;
esac
unset DEMO_DIR
```

Deleting the copy does not delete Sodapop's account/project-scoped session history,
credentials, preferences, or caches. `/logout` removes the Sodapop credential and
disconnects the engine but preserves history; normal exit does not sign out.
Choose session-only sign-in for a shared demo machine when appropriate and follow
the organization's retention process rather than broadly deleting application state.

## Media and fallback

If live access, consent, or timing is unavailable, open the existing overview
GIF/PNG below and walk through the source and acceptance criteria. Say explicitly:
“This is a signed-out interface tour with prepared changes, not a completed AI
task.” Do not reconstruct an approval or substitute a prepared diff as live output.
Use an approved genuine live recording only after one has been captured and
reviewed; none is supplied by this preparation pack.

### Assets, accessibility, and evidence inventory

Existing assets below are **signed-out, prepared-change UI recordings**. They
contain no AI responses and cannot stand in for successful live task completion.
The overview/commands clips briefly show Autopilot mode; explicitly contrast that
UI tour with this demo's requirement to keep Autopilot off. Use PNG alternatives
for reduced motion. Recording reproduction is documented in [VHS](../vhs/README.md).

| Available asset | Caption | Alt text |
| --- | --- | --- |
| [Overview GIF](../assets/demos/overview.gif) / [PNG](../assets/demos/overview.png) | Signed-out product tour; prepared local changes, not AI output. | Sodapop mascot, command discovery, composer modes, and read-only Git diff; still shows resting mascot and sidebar. |
| [Commands GIF](../assets/demos/commands.gif) / [PNG](../assets/demos/commands.png) | Signed-out workflow discovery; no model execution. | Vending-machine launcher and an unsent draft retained across Chat, Plan, and Autopilot; still shows launcher. |
| [Themes GIF](../assets/demos/themes.gif) / [PNG](../assets/demos/themes.png) | Local appearance changes only. | Sodapop changes from Neon Arcade to Graphite and Daylight; still shows Daylight. |
| [Diff GIF](../assets/demos/diff.gif) / [PNG](../assets/demos/diff.png) | Prepared staged and unstaged changes, not assistant edits. | Read-only staged, unstaged, and combined Git diff views; still shows unstaged changes. |
| [Architecture](../architecture.md) | Implementation ownership and data-flow reference, not an observed live trace. | Use the diagram's accompanying textual architecture explanation when Mermaid is unavailable. |
| [Customer request slide](request.svg) | Synthetic maintenance request and required totals, not measured results. | Fix order quantities: repeated units, mixed lines, empty cart and zero quantity; deliver observed tests and a reviewed diff. |
| [Orders sample](sample/README.md) | Intentionally incomplete baseline for an evidence-led coding exercise. | Text-based Go source and tests; no image alternative needed. |

Required live assets are **pending**, not existing links or completed proof:

| Pending deliverable | Intended caption / alt text | Evidence required before publication |
| --- | --- | --- |
| Context/plan screenshot | “Authorized synthetic orders task in advisory Plan mode, Autopilot off.” Alt: task scope and proposed quantity regression cases. | Actual run, consent, version/platform/model metadata; no account details or device codes. |
| Permission screenshot | “Operator reviews an edit or test command before approval.” Alt: exact scoped permission request and operator decision. | Real request from that run; do not reconstruct a successful approval. |
| Diff/tests screenshot | “Quantity fix with added regressions and observed test result.” Alt: multiplication fix, multi/zero-quantity tests, and command outcome. | Actual file diff and test execution; clearly label a failure or unfinished run. |
| Short video plus transcript/stills | “Input → context → approval → edit → validation → next action.” Alt: narrated synthetic task with visible review checkpoints. | Authorized, redacted capture with captions/transcript and reduced-motion stills; disclose cuts, errors, and prepared segments. |
| Run evidence record | “Observed demo evidence, limited to this account/platform/model/run.” Text alternative is the record itself. | Date, operator consent reference, app version, OS/architecture, model ID, scope, commands/outcomes, diff, deviations, and unverified items; redact identities and secrets. |

Review every artifact for privacy and readable text before sharing. Keep failures
and evidence gaps visible. Asset preparation, deterministic tests, and signed-out
GIFs do not qualify live OAuth, runtime behavior, production readiness, or catalog
admission.

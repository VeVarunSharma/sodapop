# UI state and presentation

Scope: `internal/ui/`. Apply the [repository rules](../../AGENTS.md) first.

## Rules and reasons

- Mutate UI state in Bubble Tea `Update`; put auth, Git, SDK, persistence, and
  other blocking work in `tea.Cmd` functions so input/rendering remain responsive.
- Tag asynchronous results with the relevant generation, operation, session,
  and turn IDs. Late results must not change newer state, even after cancellation.
- Adopt engines, factory work, and responders into the lifecycle ledger before
  delivering their messages: shutdown must release resources the UI never receives.
- Drain account-scoped engines/factories before login/logout mutates authentication.
  Replacing an engine only after a new login succeeds is too late to protect identity.
- Resolve permission/question callbacks exactly once, including abort, session
  replacement, stream failure, and shutdown; dismissing a dialog is not cleanup.
- Wait for the engine's tagged abort/idle barrier before completing cancellation.
  A canceled context or send acknowledgment does not establish turn completion.
- Reconcile streamed and final assistant records by `MessageID`; preserve IDs
  during history replay so final responses do not create duplicate transcript entries.
- Keep preference saves asynchronous and serialized through `preferenceWriter`;
  an old slow write must not overwrite newer choices.
- Preserve narrow-terminal, reduced-motion, no-color, and ASCII behavior independently.
  Decorative animation must not block input or imply an authenticated connection.

## Start here

[model.go](model.go), [turn.go](turn.go), and [lifecycle.go](lifecycle.go) own state/lifetimes;
[account_lease.go](account_lease.go) owns account transitions;
[timeline.go](timeline.go), [view.go](view.go), and [theme.go](theme.go) own presentation.
Change slash-command grammar in [the command catalog](../commands/AGENTS.md) first.

## Checks

From the repository root: `go test ./internal/ui`; use `-race` for asynchronous changes.
Extend [abort](abort_test.go), [account lease](account_lease_test.go),
[turn](turn_test.go), [history](history_test.go), or [view](view_test.go) tests as affected.
Drive deterministic messages/fakes, not a live model or interactive terminal.

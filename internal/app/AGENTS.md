# Application wiring

Scope: `internal/app/`. Apply the [repository rules](../../AGENTS.md) first.

## Rules and reasons

- Handle help, version, and runtime-check flags before interactive startup so
  non-interactive commands do not require a terminal or authentication.
- Resolve the canonical project once and use account-scoped config paths so
  workspace inspection, engine sessions, and resume agree on identity.
- Capture `AccountID` in `TokenSource` and call `TokenForAccount`, never a mutable
  current-account token lookup: a later sign-in must not change an existing engine.
- Close an engine when startup fails and always shut down the model after the
  program exits; partially created resources still own processes and callbacks.
- Keep CLI/environment display overrides separate from saved preferences so a
  one-launch accessibility setting does not silently overwrite the user's choices.

## Start here

[run.go](run.go) owns wiring; [run_test.go](run_test.go) supplies injected dependencies.
Read [UI account leases](../ui/account_lease.go) when changing account transitions.

## Checks

From the repository root: `go test ./internal/app`.
For identity/lifecycle changes, also run `go test -race ./internal/auth ./internal/engine ./internal/ui`.

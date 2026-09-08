# Cross-package qualification

Scope: `internal/integration/`. Apply the [repository rules](../../AGENTS.md) first.

## Rules and reasons

- Keep ordinary integration tests credential-free with disposable fixtures.
  A live test must skip before credential access unless its gate is exactly `1`.
- Once opted in, missing prerequisites and failures must fail, not skip; otherwise
  a broken live setup can look like successful qualification.
- Live model traffic requires explicit usage authorization and an exact model
  selection. Never add automatic retry, model substitution, or interactive login.
- Keep fixture project/state separate from the repository and ordinary Copilot
  state. Restrict approvals to the precise synthetic operation, not broad allow-all.
- Assert actual file bytes, correlated events, idle barriers, and cold resume.
  Assistant claims, fake events, and a normal test skip are not live evidence.
- Do not print account identity, credentials, or model/backend payloads. A narrow
  live pass does not qualify unexercised OAuth flows, platforms, or release gates.

## Start here

[Live qualification](../../docs/live-qualification.md) owns prerequisites, consent,
commands, and evidence limits. [policy_test.go](policy_test.go) and
[lifecycle_test.go](lifecycle_test.go) cover deterministic cross-package behavior.

## Checks

From the repository root: `SODAPOP_LIVE_QUALIFY=0 go test -race ./internal/integration`.
Use the linked qualification procedure only after explicit authorization.

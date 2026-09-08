# Authentication

Scope: `internal/auth/`. Apply the [repository rules](../../AGENTS.md) first.

## Rules and reasons

- Use Sodapop's existing public OAuth client with device flow. Bind saved credentials
  to that client ID; a different application identity requires reauthorization.
- Store credentials only in the secure OS store or explicitly selected process
  memory. Session-only login must not access the store; plaintext fallback is not
  a substitute for an unavailable keyring.
- `Manager` follows the active account; separate `Current` and `Token` calls are
  not an account lease. Hosts must drain account-bound work before login/logout,
  and existing engines must use `TokenForAccount` to prevent account switching.
- Keep OAuth HTTP calls context-bound, bounded, closed, and non-redirecting so
  cancellation and credential handling do not depend on an untrusted response.
- Derive expiry/refresh behavior from issued provider data, not guessed lifetimes
  or embedded secrets. Report revocation, storage, and authorization failures
  distinctly without exposing tokens, device codes, or OAuth payloads.
- GitHub sign-in is not proof of Copilot entitlement; do not collapse those states.

## Start here

[doc.go](doc.go) explains the lifecycle contract; [manager.go](manager.go),
[oauth.go](oauth.go), and [store.go](store.go) implement it.
[Authentication setup](../../docs/authentication.md) owns registration and release details.

## Checks

From the repository root: `go test -race ./internal/auth`.
Use the injected HTTP/store/clock seams in the existing tests, not live credentials.

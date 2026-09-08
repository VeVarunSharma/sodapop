// Package auth implements Sodapop-owned, public-client GitHub.com device
// authorization. New takes the public client ID of a Sodapop-owned OAuth app with
// device flow enabled; it never discovers credentials or client IDs from another
// application. A successful GitHub login does not establish Copilot eligibility.
// Saved credentials are bound to the configured client ID: a different ID
// requires reauthorization, and an empty ID cannot restore a saved account.
//
// Manager follows the currently active account; it is not an account lease for
// an existing engine. Current and Token are separate operations, not an atomic
// account/token pair. Before Login or SignOut can change authentication, the host
// must close and drain account-scoped engines and serialize that transition
// against engine construction and token-consuming work. Replacing an engine only
// after Login succeeds is too late. Same-account refresh preserves the binding;
// a different account requires a new engine.
//
// Credentials are saved only in macOS Keychain or Linux Secret Service. Explicit
// session-only login does not access the store. SignOut clears process state and
// deletes only Sodapop's credential entry; a failed deletion is reported, not hidden.
// NewWithOptions supplies store, HTTP, scope, and clock seams for credential-free
// tests. Store operations are synchronous because go-keyring has no context API.
//
// Device polling is implemented here rather than using oauth2 v0.36.0's device
// helpers: their polling clock cannot be injected, DeviceAuth does not bind its
// HTTP request to the context, and its response body is not closed. Requests here
// are context-bound, bounded, closed, and never redirected. Protocol reference:
// https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow
//
// Expiry is taken only from provider responses. GitHub documents secretless
// refresh for tokens issued by device flow; it is used only when a refresh token
// was actually issued. Missing, expired, or rejected refresh credentials require
// another device authorization. No client secret or offline_access scope is
// supplied by default.
package auth

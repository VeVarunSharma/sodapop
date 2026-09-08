# Preferences and state paths

Scope: `internal/config/`. Apply the [repository rules](../../AGENTS.md) first.

## Rules and reasons

- Keep preferences a strict, bounded, versioned JSON schema. Reject unknown
  fields, unsupported versions, trailing objects, and invalid identifiers so
  corrupt or newer data is not silently interpreted as a valid configuration.
- Defaults are for a missing file, not unreadable or malformed data. Add explicit
  migrations and compatibility cases when changing the persisted schema.
- Preserve private permissions or Windows ACLs and atomic writes through a
  same-directory temporary file, sync, and rename; failed writes must not replace
  good preferences.
- Derive runtime homes from explicit account IDs and keep credentials out of
  preferences/state paths so account data cannot leak through shared defaults.
- Preference serialization belongs to the UI's writer, not independent goroutines:
  atomic replacement alone does not stop an older snapshot from winning a race.

## Start here

[store.go](store.go), [types.go](types.go), and [store_test.go](store_test.go).
The cross-package write ordering lives in [UI lifecycle](../ui/lifecycle.go).

## Checks

From the repository root: `go test ./internal/config`.
For persistence wiring/order changes: `go test -race ./internal/config ./internal/app ./internal/ui`.

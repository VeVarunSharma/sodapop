# Command grammar and catalog

Scope: `internal/commands/`. Apply the [repository rules](../../AGENTS.md) first.

## Rules and reasons

- Change the catalog before handlers: parsing, fuzzy matching, help, completion,
  palette metadata, and busy-state rules must describe the same command surface.
- Recognize commands only at byte zero. `//` removes exactly one leading slash;
  ordinary prompts and pasted code must otherwise remain unchanged.
- Preserve freeform spacing for `/plan` and `/compact` after the first separator;
  trimming their payload changes the user's instructions.
- Keep the registry pure: it describes and parses commands, while the UI executes
  them. Help and discovery must work without an account or model connection.
- Update affected UI actions and the README command table with catalog changes so
  a command is not documented but unreachable, or accepted but undiscoverable.

## Start here

[registry.go](registry.go), [registry_test.go](registry_test.go), and
[types.go](types.go); callers are [UI input](../ui/input.go) and [actions](../ui/actions.go).

## Checks

From the repository root: `go test ./internal/commands ./internal/ui ./scripts`.
The scripts suite checks that the README command list matches the catalog.

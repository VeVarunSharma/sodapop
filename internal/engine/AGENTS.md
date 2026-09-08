# Copilot boundary

Scope: `internal/engine/`. Apply the [repository rules](../../AGENTS.md) first.

## Rules and reasons

- Keep one bundled runtime process and at most one connected session per engine.
  Retain account-bound token sourcing and account/project-scoped indexing so
  refresh, resume, and replacement cannot adopt another identity's state.
- Keep session index locking native and cancellable on macOS, Linux, and Windows;
  persisted index and lock files must pass the platform privacy checks.
- Keep create and resume configuration equally isolated. Session/config/instruction
  discovery, host Git, MCP, plugins, skills, memory, telemetry, and extensions stay
  disabled; SDK defaults must not silently enable integrations. Retain managed settings.
- Load project instructions only through the bounded, in-project loader.
  Additional guides are read explicitly with file tools, not recursive runtime
  discovery; instruction text cannot grant permissions or enable integrations.
- Preserve conservative permission classification. Outside-project reads, writes,
  shell, URLs, MCP, unknown tools, sandbox bypasses, and enterprise-managed requests
  need explicit decisions; ambiguous metadata is not authority to auto-approve.
- Use tracked pending-request IDs and exactly-once responders. The legacy SDK
  permission callback loses request IDs, cancellation, and response errors.
  Resolve/drain pending decisions on abort, replacement, stream failure, and close.
- `Abort` is a protocol: cancel decisions, drain callbacks, then emit the tagged
  `Name="aborted"` idle barrier. Context cancellation alone cannot prove a turn is done.
- Preserve message/tool IDs through streaming, final events, and history replay
  so the UI can reconcile rather than duplicate output. Surface stream/send
  failures without automatically replaying the user's action.

## Start here

[session_config.go](session_config.go) and [policy.go](policy.go) define isolation;
[pending.go](pending.go) and [callback_lifecycle.go](callback_lifecycle.go) own decisions;
[stream.go](stream.go) and [index.go](index.go) own delivery and discovery boundaries.

## Checks

From the repository root: `go test ./internal/engine`; use `-race` for lifecycle changes.
Extend the relevant [configuration](copilot_test.go), [policy](policy_test.go),
[callback](callback_lifecycle_test.go), [stream](stream_test.go), or [resume](resume_test.go)
cases using the existing fakes. Native smoke/live access are separate gates.

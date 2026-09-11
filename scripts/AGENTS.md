# Build and repository tooling

Scope: `scripts/`. Apply the [repository rules](../AGENTS.md) first.
When changing root Make targets, also use this guide and the [Makefile](../Makefile).

## Rules and reasons

- Use the official SDK bundler with the explicit runtime pin and supported target;
  preserve checksum verification rather than trusting a download or local install.
- Validate target/version/public-client inputs before invoking build tools.
  Keep Makefile overrides and `start`/`run`/`install` output selection consistent.
- Package from fresh temporary staging with an explicit payload list and cleanup.
  Broad directory copies can ship local environment files or stale build artifacts.
- Preserve archive naming and basename-based checksum files; exercise the extracted
  archive, not a staging binary that packaging deliberately removes.
- Release packaging requires the public OAuth client ID and a SemVer without a
  `v` prefix. The release manifest describes all six native archives by default,
  with commit, SDK/runtime pins, platform, archive checksum, and binary checksum.
  Explicit phase subsets must be complete for their declared `--platforms` set.
- [releasectl](releasectl/main.go) and
  [distribution](../internal/distribution/manifest.go) own archive validation and
  safe extraction: Windows ZIP, Unix tar.gz, one package root, required license
  payload, strict metadata and checksums. Hash binding is not authentication;
  trusted transport and attestations remain separate.
- Installation may print PATH guidance but must not edit shell profiles; callers
  choose where the executable goes.
- Keep build/bundler script fixtures on the fake Go command. Reuse a native,
  stdlib-only `releasectl` build through `SODAPOP_RELEASECTL` for production
  archive tests; never download or overwrite shared runtime bundles for fixtures.
- Keep the coverage ratchet/floors intact and profiles temporary. Fix behavior or
  add meaningful tests rather than weakening gates or counting generated code.

## Start here

[build.sh](build.sh), [bundle.sh](bundle.sh), [package.sh](package.sh),
[scripts_test.go](scripts_test.go), and [package_test.go](package_test.go).
For demos, use the [isolated recording guide](../docs/vhs/README.md).
For instruction changes, use [the guidance design](../docs/agent-guidance.md).

## Checks

From the repository root: `go test ./scripts`.
Use `go test ./scripts -run '^TestAgentGuidance$'` for guide structure/link changes.
Native packaging/smoke checks are separate from these credential-free fixture tests.

# Repository automation

Scope: `.github/`. Apply the [repository rules](../AGENTS.md) first.

## Rules and reasons

- Keep toolchain selection tied to `go.mod` and preserve macOS/Linux CI coverage,
  race, vet, and formatting gates plus native Windows tests so one developer's
  platform is not the only evidence.
- Retain all five native release targets and checks on the extracted archive.
  Cross-compilation alone does not exercise the packaged runtime.
- Read `SODAPOP_GITHUB_CLIENT_ID` from the existing Actions variable, not a token
  or client secret. Preserve the OAuth registration and grants during branding changes.
- Tagged releases build the five native archives, verify their checksums and extracted
  executables, then create a draft GitHub Release. Never replace assets on an existing
  release; draft review is the publication boundary.
- Installation jobs must consume those same archives, not rebuild a replacement.
  Test-only client IDs and versions belong only in the native-candidate workflow.
- Public-download checks must use unauthenticated asset URLs. Package publication
  is a separate manual, protected-environment operation over an attested release;
  see [distribution gates](../docs/distribution.md).
- Keep `copilot-instructions.md` a short pointer to the root guide, not a second
  policy manual. Duplicated always-loaded instructions drift and waste context.

## Start here

[CI](workflows/ci.yml), [tagged releases](workflows/release.yml), and
[native install checks](workflows/install-tests.yml), with
[public downloads](workflows/public-downloads.yml) and
[approved publication](workflows/publish-channels.yml).
[Windows installer tests](workflows/windows-installers.yml) require an explicitly
provisioned non-elevated disposable runner and owner-reviewed WiX terms.
[The PR template](pull_request_template.md) and packaging contracts in
[scripts/AGENTS.md](../scripts/AGENTS.md).

## Checks

From the repository root: `go test ./scripts` for existing wiring/document contracts.
For workflow changes, also inspect matrix coverage and which actual commands run;
fixture checks alone do not execute GitHub Actions.

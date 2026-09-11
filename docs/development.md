# Development

This guide is for contributors and people building from a source checkout.
Ordinary users should start with [installation](installation.md) and sign in
inside the application; they do not need to register an OAuth app.

## Prerequisites

Use the Go version required by [go.mod](../go.mod) or later; the current
requirement is Go 1.27.1. You also need Git and network access to fetch the pinned
Go dependencies and Copilot runtime. Windows ARM64 and x64 source builds use Git Bash.

Native targets are macOS, glibc-based Linux, and Windows on ARM64/x64.
The npm distribution tooling separately requires Node.js 18 or later.
The website is a separate package with its own Node toolchain; it is not a
dependency of a native application build.

## Configure source-build sign-in

Use Sodapop's existing, device-flow-enabled public GitHub OAuth Client ID,
obtained from the project owner. Keep the existing registration and grants;
do not create a replacement application or copy another application's identity.

For Make-based local development, copy `.sodapop.env.example` to the ignored
`.sodapop.env` and set only `SODAPOP_GITHUB_CLIENT_ID` to that public identifier.
The Makefile loads this file. It must never contain a client secret or token.

Direct script invocations and installed executables do not load `.sodapop.env`.
For those, export the same public-client setting in the build environment or
use an executable built with it linked in. A missing identifier is a visible
sign-in blocker, not a successful mock connection.

This configuration supplies the application's public identity. Actual user
authorization still happens through `/login`. See the
[authentication reference](authentication.md) for credential storage and
owner-controlled qualification.

## Build and run

From the repository root on macOS or Linux:

```sh
make build
make start
```

`make build` verifies and embeds the pinned runtime, then writes `bin/sodapop`.
`make start` rebuilds and launches the interface; `make run` is an alias.
The installed executable does not need Go, Node.js, or an existing Copilot
installation.

On Windows ARM64 or x64, with the public-client setting exported when sign-in is needed:

```sh
bash scripts/build.sh
bin/sodapop.exe
```

For a Unix user-local installation, `make install` installs the command into the
configured user bin directory. It does not edit shell profiles. Follow its
printed PATH guidance rather than overwriting another channel's command.

`SODAPOP_TARGET`, `SODAPOP_OUTPUT`, and `SODAPOP_VERSION` control source builds.
Use [the Makefile](../Makefile) and `make help` for the supported targets.
Generated runtime bundles, executables, and archives are build outputs, not
source files to hand-edit.

## Credential-free checks

```sh
make test
make check
```

Normal tests use injected dependencies and do not require OAuth, Copilot access,
or an interactive terminal. `make check` enforces coverage floors and the
baseline, then runs race testing and vet. Add focused regression tests for
observable changes and keep account scoping, cancellation, and permission
decisions covered.

A local bundled-runtime smoke check is separate from real sign-in and model
qualification. Do not run live qualification as an incidental build or website
test; it requires owner-authorized accounts and normal Copilot usage.

### Issue forms

Templates live in `.github/ISSUE_TEMPLATE/`. With the existing website Node
toolchain and dependencies, run these checks from the repository root:

```sh
node --test site/test/unit/issue-forms.test.mjs site/test/unit/content.test.mjs
```

This reuses the existing YAML tooling without starting the application or
calling GitHub. Run it explicitly for template-only edits: the Pages workflow's
current path filters do not include issue templates. Before publishing, inspect
each form in GitHub's available preview/editor, including empty required inputs
and optional evidence, without creating public test issues. The live chooser
updates only after the files reach the repository's default branch.

## Release and package metadata

The [distribution contract](distribution.md#release-assets) defines schema
version 1. A manifest binds the release version and commit, both
`copilot_sdk_version` and `copilot_runtime_version`, and each platform's archive
and executable hashes. Windows portable artifacts use ZIP with `sodapop.exe`;
macOS/Linux use `.tar.gz`.

The default native release set contains all six supported targets. An explicit
`--platforms` subset is useful for local checks, but every declared artifact must
exist. Never silently drop a missing platform that the public channel advertises.
When the npm builder receives `--platforms`, it must exactly match the manifest.

The [npm packaging guide](../npm/README.md) describes manifest-derived,
exact-version optional dependencies. Qualified Windows ZIPs can supply
`@sodapop-sh/windows-arm64` and `@sodapop-sh/windows-amd64`; Node selects ARM64
on Windows ARM64 and x64 on Windows x64. `npm/packages/cli/package.json` is a
development template, not publication evidence or the published support matrix.

Core Windows ARM64 ZIP and npm delivery are part of the six-platform release
contract. ARM64 MSI, WinGet, and Scoop remain phase 2 until native installer
qualification is complete.

Generating packages and matching hashes do not establish registry availability,
publisher identity, verified attestations, or code signing. Public channels
need their separate publication and qualification evidence. The website catalog
can select an exact stable or prerelease tag, but exposes only channels whose
public metadata matches that release; prerelease npm uses `preview`, while
Homebrew remains stable-only.

## Developer references

- [Architecture](architecture.md): package ownership, UI/engine boundaries, and account-scoped state.
- [Authentication](authentication.md): the existing public client, secure stores, and release qualification.
- [Distribution](distribution.md): archive payloads, hashes, installation boundaries, and publication gates.

Application source is in the [canonical repository](https://github.com/VeVarunSharma/sodapop).
Sodapop's original code uses the [MIT license](../LICENSE); the bundled runtime
has separate terms summarized in [third-party notices](../THIRD_PARTY_NOTICES.md).

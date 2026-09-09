# Distribution contract

This document defines the credential-free checks shared by GitHub Releases and
downstream installers. Channel-specific tests may add stricter requirements, but
they must not weaken these boundaries or replace a published digest with one
computed from an untrusted download.

## Release assets

Each supported platform has one versioned archive, one adjacent checksum file,
and one entry in `sodapop-<version>-manifest.json`. Archive names use
`sodapop-<version>-<goos>-<goarch>` plus the platform's archive extension. Every
archive has a single same-named top-level directory and contains the native
`sodapop` command (`sodapop.exe` on Windows).

The release manifest binds bytes across channels; it is not by itself proof of
publisher identity. Obtain it through the approved release and verify release
attestations or signatures separately. Schema version 1 records the release
version, full Git commit, `copilot_sdk_version`, `copilot_runtime_version`, and
these fields for every artifact:

- `platform`
- `archive`
- `archive_sha256`
- `binary_sha256`

The release job must generate the manifest from already-built native archives,
after verifying each adjacent checksum. Missing, duplicate, malformed, or
unexpected platform entries fail the release rather than producing a partial
manifest.

The default release set contains all five supported targets. The manifest tool's
explicit `--platforms` option permits a declared subset for local tests; it must
not be used to hide a failed platform that a public channel still advertises.
Windows portable packages use ZIP, not a Unix-only extraction instruction.

## Checksum verification

The `.sha256` file contains exactly the archive SHA-256 and archive basename so
it works from the download directory with `shasum -a 256 -c` or
`sha256sum -c`. Installers verify `archive_sha256` before extraction and
`binary_sha256` before installing or executing the extracted command.

Verification failure is terminal. An installer must not launch, copy, cache as
valid, or report success for an asset whose archive or binary digest differs
from the manifest.

## Installed-command smoke checks

Every native release and installer exercises the installed command itself, not
a build-tree or staging executable:

```text
sodapop --help
sodapop --version
sodapop --check-runtime
```

These checks run without OAuth credentials, a saved login, a model request, or
an interactive terminal. `--check-runtime` is a local bundled-runtime handshake;
it is not a substitute for the separately authorized live qualification gate.

## Package contents

Native archives use an explicit allowlist. They contain the executable, project
README and license, third-party notices, the bundled runtime terms, generated
dependency notices, and the documentation/media intentionally named by the
packaging script. They do not contain environment files, credentials, caches,
repository metadata, unrelated build output, or stale staging content. Archive
entries must be regular files or directories under the single package root;
links and path traversal are rejected.

Package-manager metadata and launchers may be shipped separately from the native
archive. Their package tests must use an explicit file allowlist and prove that
installation selects the matching manifest platform. Repackaging may change a
channel package's own digest, but the installed executable must still match the
manifest's `binary_sha256`.

## Upgrade and uninstall boundaries

Install and upgrade operations may change only paths owned by that channel.
The local installer stages a new command beside the destination before replacing
the existing regular file, and refuses to replace another manager's symlink.
Verification and extraction failures must not replace a good installation.
Each package manager's actual upgrade and recovery behavior needs its own
installation tests, not an assumption that every manager offers atomic rollback.

Uninstall removes only package-owned commands, shims, and package metadata.
It preserves Sodapop preferences, session history, runtime caches, secure-store
credentials, shell profiles, and unrelated files. Destructive state removal, if
ever added, must be a separate explicit command and is not part of uninstall.

## Cross-channel hash consistency

Homebrew, npm, Windows packaging, direct downloads, and future channels resolve
the same platform entry from the published release manifest. A channel that
downloads the native archive must use the manifest's exact `archive_sha256`; all
channels must install an executable matching its exact `binary_sha256`.

Credential-free tests should use temporary archives, fake commands, and isolated
filesystem roots. The reusable helpers in
`scripts/distribution_contract_test.go` validate manifest/assets, archive
payloads, installed-command smoke checks, owned-path mutations, and channel hash
references without downloading a release or accessing account state.

## Test layers and evidence

| Layer | What runs | What it establishes |
| --- | --- | --- |
| `make distribution-test` | Go and Node regression suites, including production archive verification | Deterministic failure handling and packaging contracts; not public availability |
| `make native-install-test` | A verified archive and a copied portable executable | Real native command startup, exact versions/hashes, cold and cached runtime startup |
| Native installation candidates | Two real test-only releases, package-manager installations, and native Windows delivery checks | Installation/upgrade/removal mechanics without publishing or signing in |
| Tagged release installation gate | Installation checks consuming the exact candidate archives | Candidate byte identity and native delivery behavior before draft creation |
| `make public-download-test` / public-download workflow | Unauthenticated HTTPS downloads and native execution | Availability and integrity of a published archive, not merely an Actions artifact |

Native installation checks create isolated homes and state directories. Direct
archive checks deliberately remove Go, Node, and Copilot from the installed
command's PATH. npm tests additionally require Node and must not fetch a missing
native payload at first run. Local-tarball npm tests and published-registry tests
are separate evidence; only the latter establish that the public command works.

Use `make release-tools` to build `bin/releasectl` and `bin/installcheck`.
For a local single-platform candidate, generate its manifest explicitly:

```sh
bin/releasectl manifest --dir dist --version 0.0.2 \
  --commit "$(git rev-parse HEAD)" \
  --platforms "$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
make native-install-test SODAPOP_VERSION=0.0.2
```

An incomplete or inaccessible public release must fail, not turn into a skipped
success. Native Windows results require a Windows runner; cross-compilation and
ZIP/MSI manifest fixtures do not establish successful Windows installation.
MSI installation is per-user, and destructive installer tests require explicit
consent to use a disposable runner. Signed production distribution still needs
configured signing identities, macOS notarization where applicable, and
owner-reviewed publication. Never infer signing from an archive checksum.

OAuth device authorization, secure credential persistence, entitlement, and a
real Copilot session remain separately authorized qualification. None of the
credential-free installation commands grants permission for live model usage.

## Owner-controlled publication

`publish-channels.yml` starts automatically only after the release-triggered
public-download workflow succeeds. Prereleases select npm only; stable releases
select npm and Homebrew. Manual dispatch remains available for an owner-reviewed
recovery or retry. Manual public-download runs do not trigger publication. The
workflow verifies GitHub's release attestation, the downloaded manifest's
attestation, and the archive/binary hashes before generating channel packages.
It never rebuilds the application for another channel. Enable immutable releases
on the repository before using this workflow.

Configure protected `npm-publish` and `homebrew-publish` environments with required
reviewers. Creating a workflow that names an environment does not configure those
reviewers. Confirm ownership of every `@sodapop-sh` package, perform initial registry
bootstrap if necessary, and authorize this exact workflow/environment as a trusted
publisher. The pinned npm publishing CLI supports OIDC; no npm write token is
stored in source. `SODAPOP_NPM_PUBLISH_ENABLED=true` is an explicit owner switch,
not a substitute for registry permission.

Because npm trusted publishers and staged publishing require an existing package,
the first `@sodapop-sh` versions use the separate, manually dispatched
`bootstrap-npm.yml` workflow. It is restricted to a published immutable prerelease,
the protected `npm-publish` environment, the exact confirmation phrase, and the
temporary `SODAPOP_NPM_BOOTSTRAP_ENABLED=true` owner switch. Configure a short-lived
`NPM_BOOTSTRAP_TOKEN` environment secret with package write access and 2FA bypass,
run the workflow once, then immediately delete the secret and owner switch and
revoke the registry token. Configure all generated packages to trust
`publish-channels.yml` in the `npm-publish` environment before normal publication.
The bootstrap workflow is not a fallback for later releases.

The npm publisher creates actual tarballs and publishes native packages before
the launcher. A retry reuses an identical tarball immediately. If only container
metadata differs, it downloads the published package and requires identical
file paths, sizes, modes, and content before continuing; any payload difference
or ambiguous registry/network failure is fatal. Publisher tooling comes from
the reviewed default branch while package generation remains bound to the
immutable release tag. Prereleases use `preview`, never `latest`.

The Homebrew job uses a GitHub App limited to the existing `homebrew-sodapop`
repository with contents and pull-request write permissions. Configure the
`SODAPOP_HOMEBREW_APP_ID` variable and `SODAPOP_HOMEBREW_APP_PRIVATE_KEY` environment
secret in GitHub, not in this checkout or chat. It opens a version-update PR; it
does not create the tap, merge the PR, or imply that the public tap has updated.

Stable npm publication and stable tap updates require
`SODAPOP_STABLE_RELEASE_QUALIFIED=true`. This records owner sign-off after native
installation, signing/notarization, and sign-in qualification; it is not an
automated signature validator. Leave it unset until that evidence exists.
Registering WinGet/Scoop channels, configuring signing credentials, and obtaining
native ARM64 evidence remain separate external/platform gates.

## Windows installer execution

The five-platform install workflow exercises the native Windows portable path
and generates channel manifests. `windows-installers.yml` is a separate manual
gate for MSI and opt-in WinGet/Scoop installation. It consumes two published,
attested numeric releases and tests actual installation, repair, upgrade, and
removal. Those public releases must exist before that workflow can succeed.

Its runner labels must identify a provisioned, disposable, **non-elevated**
Windows x64 user. The default `sodapop-disposable` label is a requirement to
configure, not a runner created by this repository. An elevated token is rejected
so administrator rights cannot mask a per-user MSI defect. Go, PowerShell, .NET,
and any selected WinGet/Scoop prerequisites must be available to that user.
The workflow requires explicit WiX terms acceptance before building test
containers. It does not accept those terms or enable privileged WinGet settings
on the developer's behalf.

The MSI containers built by this test workflow are explicitly unsigned
candidates; only logs/evidence are uploaded. Production signing and a repeat of
native evidence against the final signed container are separate requirements.
The [Windows delivery guide](../packaging/windows/README.md) documents those
commands and the per-user state/registration boundaries.

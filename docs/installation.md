# Installation

Let's get Sodapop onto your machine. The [download page](/download/) is the
authority for current public availability, exact release versions, and enabled
installation methods. Sodapop's website can launch before every distribution
channel is published. An unavailable method is not a working installation
command.

The generated installation choices below come from the same public release
catalog as the download page. Stable npm releases use the registry's verified
`latest` dist-tag; prereleases use `preview`. Channels that have not passed
public verification are omitted. The [source-build guide](development.md)
remains available for developers. Local builds need the existing Sodapop
public-client configuration to sign in; they do not need a new OAuth app.

<!-- SODAPOP_INSTALLATION_REFERENCE -->

## Supported platforms

These are application build targets, not a claim that every channel is public.

| System | Architecture | Native target | Native archive |
| --- | --- | --- | --- |
| macOS | Apple silicon | `darwin/arm64` | `.tar.gz` |
| macOS | Intel x64 | `darwin/amd64` | `.tar.gz` |
| Linux with glibc | ARM64 | `linux/arm64` | `.tar.gz` |
| Linux with glibc | x64 | `linux/amd64` | `.tar.gz` |
| Windows | x64 | `windows/amd64` | `.zip` |

Homebrew is not currently published. npm's supported platform set comes from
the exact release manifest and matching package version, not from the
repository's development package template.

Windows x64 npm support is conditional: `@sodapop-sh/windows-amd64` is available only
when the release manifest includes the qualified Windows ZIP and the matching
npm package version is actually published. A four-Unix release does not
advertise a Windows dependency. Check the [download page](/download/) for
confirmed channel availability; package-generation code is not evidence of
publication.

Windows ARM64 and musl-based Linux are not supported native targets. Do not
substitute an archive or npm package for a different operating system or
architecture.

Native executables include the pinned Copilot runtime. Native archive users do
not need Go, Node.js, or a separately installed Copilot executable. The npm
launcher requires Node.js; it is an installation channel, not a different
application.

## Install a native archive when available

1. Select your operating system and architecture on the [download page](/download/).
   Use the exact release's archive, checksum, and manifest links.
2. Compare the downloaded archive's SHA-256 with the release checksum and
   manifest before extracting or running it. On macOS use `shasum`; on Linux
   use `sha256sum`; on Windows use PowerShell's `Get-FileHash`.
3. Extract the `.tar.gz` on macOS/Linux or the `.zip` on Windows into a directory
   you own. Keep the included license and notice files with the distribution.
4. Put the directory containing `sodapop` or `sodapop.exe` on your user `PATH`,
   or run the executable by its full path.

Unix checksum sidecars name the archive basename, so the checksum command
should run from the directory containing both files. A digest mismatch is a
reason to stop, not to bypass the check. A matching checksum verifies bytes
against that manifest; it does not itself establish publisher identity,
code signing, or macOS notarization. Matching hashes do not verify release
attestations; publisher-identity and attestation checks are separate.

Do not disable operating-system security protections to work around a blocked
download. Recheck the source and publication status instead.

## Check the installed command

Run these against the command you actually installed:

```sh
sodapop --help
sodapop --version
sodapop --check-runtime
```

`--check-runtime` performs a local bundled-runtime handshake. These commands do
not sign in, open a model session, or prove Copilot entitlement. If the command
is not found, check the extracted executable's directory and your `PATH`.

Next, launch from your project directory and use `/login` inside the
application. Follow [getting started](getting-started.md) for the full flow.

## Upgrade or remove an installation

Use the same channel that owns your installation. For a manually extracted
archive, keep the old executable until the new download has passed integrity
checks. Avoid overwriting a package manager's symlink with a manual copy.

Removing the installed command is not the same as signing out. Run `/logout`
if you also want to remove Sodapop's saved credential. Uninstallation is not a
request to erase preferences, account-scoped session history, or runtime caches.
See the [distribution contract](distribution.md#upgrade-and-uninstall-boundaries)
for installer ownership boundaries.

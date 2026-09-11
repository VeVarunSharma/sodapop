# Sodapop npm distribution

`@sodapop-sh/cli` supplies the `sodapop` launcher. Its exact-version optional
dependencies are generated from the release manifest's available platforms,
not from a hard-coded list of unpublished packages.

| Native platform | npm package | Payload |
| --- | --- | --- |
| darwin/arm64 | `@sodapop-sh/darwin-arm64` | `bin/sodapop` |
| darwin/amd64 | `@sodapop-sh/darwin-amd64` | `bin/sodapop` |
| linux/arm64 | `@sodapop-sh/linux-arm64` | `bin/sodapop` |
| linux/amd64 | `@sodapop-sh/linux-amd64` | `bin/sodapop` |
| windows/arm64 | `@sodapop-sh/windows-arm64` | `bin/sodapop.exe` |
| windows/amd64 | `@sodapop-sh/windows-amd64` | `bin/sodapop.exe` |

Linux packages declare `libc: ["glibc"]`. The launcher rejects musl and
unidentifiable/conflicting libc evidence rather than guessing. Windows arm64
and x64 are included only when the manifest declares each qualified ZIP
artifact. The launcher selects from `process.arch`, so ARM64 Node uses the
ARM64 package while x64 Node running on Windows ARM uses the x64 package.
Other unlisted targets are unsupported. A four-Unix release never advertises
a Windows dependency. Explicit host-only manifests are also useful for local
installation checks; they do not establish cross-platform readiness.

There are no install hooks, first-run downloads, registry lookups in the
launcher, or fallbacks to an installed `copilot`. Node 18 or later is required
to run the launcher; Go is **only a packaging/test dependency**. CLI and native
package versions, release bindings, platform metadata, and the installed binary
SHA-256 must agree before execution. The launcher inherits actual stdin,
stdout, stderr, cwd, environment, and argument boundaries, and preserves the
native exit status. On POSIX Node versions with `process.execve`, it replaces
Node for exact terminal job control. Older Node versions use signal-forwarding
subprocesses; Windows keeps the inherited console's Ctrl+C delivery.

## Build packages from verified releases

Run from the repository root:

```sh
go run ./scripts/releasectl manifest --dir /absolute/releases \
  --version 1.2.3 --commit FULL_40_CHARACTER_COMMIT \
  --platforms darwin/arm64,darwin/amd64,linux/arm64,linux/amd64
node npm/scripts/build-packages.mjs --version 1.2.3 \
  --release-dir /absolute/releases \
  --manifest /absolute/releases/sodapop-1.2.3-manifest.json \
  --platforms darwin/arm64,darwin/amd64,linux/arm64,linux/amd64 \
  --output /absolute/existing-parent/new-npm-output
```

The builder requires schema version 1, the exact version and full commit,
repository-pinned Copilot SDK/runtime versions, unique supported platforms,
canonical archive names, and archive/binary hashes. The npm channel retains
its existing 64-character version limit, narrower than the shared release
tool's 128-character limit; a release accepted by that tool is not necessarily
npm-eligible. SemVer build metadata
(`1.2.3+build`) is rejected because npm does not preserve it as an exact package
version. `--platforms`, when supplied, must exactly match the manifest. Without
it the manifest itself declares the available set. All declared assets must
exist; missing platforms are never silently dropped.

The shared Go release tool verifies each archive and binary and safely extracts
the standard single `sodapop-VERSION-goos-goarch` root. There is no private npm
tar/ZIP parser. Set `SODAPOP_RELEASECTL` to an **executable path**, not a shell
command, to use a compiled helper; otherwise the builder invokes
`go run ./scripts/releasectl` from the absolute repository root with network
module resolution disabled. This requires the repository's installed Go
version and does not install or change runtime pins.

Output is fresh staged content with explicit launcher/binary/document paths and
the verified upstream license tree. The output's parent must already exist.
The builder refuses existing outputs (including its own prior builds), links,
dangerous ancestors, and output beneath release inputs. It never recursively
deletes `--output`; only its uniquely created staging tree is cleaned up.
Choose a new output directory for every build.

Platform packages contain a composite bundle: original Sodapop code is MIT,
while dependencies and the Copilot runtime retain their separate terms in
`THIRD_PARTY_NOTICES.md` and `LICENSES/`. The composite payload is **not MIT-only**.
The launcher package includes the project license and third-party notices.

## Credential-free regression tests

```sh
node --test npm/test/*.test.mjs
```

Equivalent from `npm/`: `npm test`. Tests require existing Node, npm, and the
repository-required Go toolchain, but no new npm dependencies, network, model,
OAuth, keyring access, or interactive terminal. npm operations run through its
installed JS entry point with isolated home, user/global config, cache, work
directory, and global prefix, in offline mode and with `--ignore-scripts`.
They do not depend on public package names being published or on user npm
configuration.

Tests build distinct Go subprocess fixtures per architecture (including an
actual Windows PE `.exe`) and archive them using Go's standard library. These
are **fixture tests, not native Sodapop release evidence**. Coverage includes:
shared verifier failure handling; all six real `npm pack` file allowlists;
offline local-tarball global installation and generated shims; versions,
hashes, missing optional packages; argument spaces/stdin/cwd/nonzero exits;
fixture upgrade/reinstall/uninstall with preserved state/profile sentinels;
and real POSIX interrupt handling plus injected Windows signal behavior.
The POSIX signal test explicitly reports its Windows skip: actual console
Ctrl+C still needs a Windows native console runner.

## Native installation E2E (real archives required)

```sh
SODAPOP_RELEASECTL=/absolute/releasectl npm --prefix npm run test:install -- \
  --release-dir /absolute/current-release \
  --manifest /absolute/current-release/sodapop-1.2.3-manifest.json \
  --previous-release-dir /absolute/previous-release \
  --previous-manifest /absolute/previous-release/sodapop-1.2.2-manifest.json
```

`--release-dir` and `--manifest` are required. The two `--previous-*` arguments
are optional, but must be supplied together to exercise upgrade. Without them,
the command explicitly reports `Upgrade NOT RUN` before and after the run and
does not claim upgrade readiness. It still exercises installation, reinstall,
missing optional dependencies, and uninstall. Neither partial previous inputs
nor fabricated previous releases are accepted.

Supplied archive sets must contain real native Sodapop releases with manifest
pins matching this checkout; when supplied, the previous version must be older
than the current one. `SODAPOP_RELEASECTL` selects the existing compiled helper;
otherwise the build-time Go fallback applies.
Generate explicit host-only manifests with `releasectl manifest --platforms
darwin/arm64` (or the actual runner's Go platform) when only host archives are
available. If a supplied manifest declares more platforms, all its archives
must be present for package construction. No fake nonhost payload is needed for
a valid nonempty supported subset; all optional dependencies are generated
exactly from that subset. The command never relabels a binary as a previous
version or silently skips upgrade.

By default the command builds packages into a fresh temporary directory, performs real
`npm pack` and offline `npm install --global --prefix TEMP` from **local `.tgz`
files**, and runs the installed shim's `--help`, exact `--version`, and
`--check-runtime` twice. It verifies the installed binary against the manifest,
upgrades the previous release to current when provided, reinstalls current,
rejects a missing optional payload, and uninstalls while preserving
state/profile sentinels.
Smoke PATH contains only Node, not Go or an ambient Copilot. All commands use
isolated state and no live sign-in/model calls.

## Public-registry installation E2E (explicit network opt-in)

After downloading a trusted release manifest and its host archive, parent CI
can run:

```sh
SODAPOP_RELEASECTL=/absolute/releasectl npm --prefix npm run test:install -- \
  --registry-install \
  --release-dir /absolute/downloaded-release \
  --manifest /absolute/downloaded-release/sodapop-1.2.3-manifest.json
```

The required release arguments and optional complete pair of `--previous-*`
arguments are the same as local mode. Registry mode validates the manifest and
verifies the host archive/binary through `releasectl` **before any npm network
operation**. A complete manifest is accepted with only its host archive present
in this mode, since no nonhost package is built locally. Supplied previous
inputs are verified too, and both versions must exist in the public registry
to exercise a registry upgrade.

This mode performs the equivalent of:

```sh
npm install --global --prefix TEMP --include=optional \
  --ignore-scripts --registry https://registry.npmjs.org/ \
  @sodapop-sh/cli@EXACT_MANIFEST_VERSION
```

It does **not** pack local tarballs, preinstall a host package, seed npm's cache,
use a tag or version range, or fall back to local packages when the namespace
is absent. Both the default and `@sodapop-sh` scoped registry are forced to the
public npm registry. HOME, work directory, user/global npm configuration,
cache, and prefix are freshly isolated; ambient authentication tokens, proxy
settings, and `NODE_PATH` are not inherited. Lifecycle scripts remain disabled.
Network access is enabled only by the explicit `--registry-install` flag;
normal tests and the default local-tarball path remain offline.

Before running the downloaded global shim, the harness verifies the installed
CLI/package versions and checks that the native executable resolves **inside
the isolated prefix** and matches the verified manifest's binary SHA-256.
Missing, mismatched, or outside-prefix payloads stop the run without executing
that shim. Successful installations run real `--help`, exact `--version`, and
`--check-runtime` twice, then exercise reinstall and uninstall with preserved
state/profile sentinels. The omitted-optional-dependency case in registry mode
checks rejection at the pre-execution hash gate rather than launching an
unverified downloaded shim.

An unpublished namespace, missing version/optional package, npm failure, or
hash mismatch fails the command; there is no success-shaped fallback. Unit
tests inject the npm subprocess to exercise mode selection, exact public
arguments, error handling, and hash-gate failures without contacting the
registry. They are not evidence of a successful public installation.

Parent CI owns native runner matrices and channel publication. Passing these
local checks does **not** establish npm scope ownership, registry availability,
publisher authentication, provenance, signatures, or public install readiness.
`--registry-install` must actually succeed on the intended native runners before
claiming public-channel installation evidence. This command never publishes.

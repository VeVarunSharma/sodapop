# Homebrew tap packaging

This template is for a Sodapop-owned Homebrew tap, not `homebrew-core`. It
installs the `sodapop` executable and retains `LICENSE`,
`THIRD_PARTY_NOTICES.md`, and `LICENSES/` under `pkgshare` from the four
supported macOS/Linux release archives.

After publishing a tagged GitHub Release such as `v1.2.3` with the archives,
basename-based `.sha256` sidecars, and release manifest, generate the tap
formula:

```sh
bash scripts/generate-homebrew-formula.sh 1.2.3 ../homebrew-sodapop/Formula/sodapop.rb
```

The generator reads the exact tagged release manifest over HTTPS, cross-checks
the four Homebrew checksum sidecars against the manifest, fills
`packaging/homebrew/Formula/sodapop.rb.tmpl`, and writes the result atomically.
It does not publish a release or modify a tap repository unless that repository
is selected as the output path.

For local release preparation in CI before publication, use the verified release
directory and manifest produced by `releasectl`:

```sh
SODAPOP_HOMEBREW_RELEASE_DIR=dist \
SODAPOP_HOMEBREW_MANIFEST=dist/sodapop-1.2.3-manifest.json \
  bash scripts/generate-homebrew-formula.sh 1.2.3
```

This verifies the four UNIX artifacts with `SODAPOP_RELEASECTL` or
`go run ./scripts/releasectl` before generating the formula.
`SODAPOP_HOMEBREW_RELEASE_DIR` and `SODAPOP_HOMEBREW_MANIFEST` must be set
together. The default output is `dist/homebrew/Formula/sodapop.rb`. For older
local-only checksum workflows, `SODAPOP_HOMEBREW_CHECKSUM_DIR=dist` remains
available as a fallback.

A tap check can detect stale versions or checksums by regenerating from the
tagged release and comparing without changing the formula:

```sh
bash scripts/generate-homebrew-formula.sh --check 1.2.3 \
  ../homebrew-sodapop/Formula/sodapop.rb
```

Native Homebrew install coverage lives in `scripts/test-homebrew-install.sh`.
It generates a temporary formula from a verified local manifest, publishes it
through a temporary local tap, installs from local file URLs, checks
`brew test`, reinstall, optional upgrade, uninstall, manifest binary hashes,
and notice retention. The script refuses to mutate any existing Homebrew prefix
unless it is running inside its own disposable prefix or
`SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1` is set for an ephemeral runner.

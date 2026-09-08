# Windows delivery

These recipes consume the final `windows/amd64` release ZIP. They never rebuild,
strip, sign, or patch `sodapop.exe`. Windows ARM64 is deliberately not advertised.
CI/publication integration belongs to the parent release workflow.

## Inputs and trust boundary

Supply a trusted schema-1 `sodapop-VERSION-manifest.json`, the real
`sodapop-VERSION-windows-amd64.zip`, and its adjacent `.zip.sha256` file. The
manifest must describe the exact version, commit, SDK/runtime versions, archive
hash, and binary hash. The ZIP root must be
`sodapop-VERSION-windows-amd64/`. The shared `releasectl verify` and `extract`
commands check both manifest hashes, the checksum companion, archive paths,
entry types, payload allowlist, and root before consuming files. A checksum file
alone is not trusted. Obtain the manifest through the approved release/signing
gate; neither these generators nor a locally supplied hash establish publisher
identity.

Every output path must be new with an existing, non-symlink parent. A stale
output is an error, not permission to replace another installation. MSI staging
selects only `sodapop.exe`, `README.md`, `LICENSE`, `THIRD_PARTY_NOTICES.md`, the
runtime terms, and dependency notices under `LICENSES/` (both module-directory
notices and flat `dependency.license`/`dependency.notice` files). Verified documentation
and media remain in the portable ZIP but are not MSI payload. Unknown files,
executable notice extensions, missing terms, and stale DLLs fail staging.

## Portable generator commands (macOS/Linux/Windows)

From the repository root, using a fresh output directory name:

```sh
go test ./scripts/windows
go build -o /tmp/sodapop-releasectl ./scripts/releasectl
SODAPOP_RELEASECTL=/tmp/sodapop-releasectl go run ./scripts/windows manifests \
  --dir dist --manifest dist/sodapop-1.2.3-manifest.json \
  --output dist/windows-channels-1.2.3
```

The default WinGet ID is `VeVarunSharma.Sodapop`. `--package-id` accepts a
validated owner/application ID; `--tag`, if supplied, must equal `vVERSION`.
Generated paths are:

```text
windows-channels-1.2.3/
  winget/manifests/v/VeVarunSharma/Sodapop/1.2.3/
    VeVarunSharma.Sodapop.yaml
    VeVarunSharma.Sodapop.installer.yaml
    VeVarunSharma.Sodapop.locale.en-US.yaml
  scoop/bucket/sodapop.json
```

The proposed owned bucket is `VeVarunSharma/scoop-sodapop`, with
`bucket/sodapop.json`. This recipe does **not** create the repository or claim it
exists. URLs point to the canonical
`https://github.com/VeVarunSharma/sodapop/releases/download/vVERSION/ARCHIVE`.
WinGet uses ZIP/nested-portable, the prefixed executable path, the `sodapop`
alias, `x64`, user scope, and the archive SHA-256. Scoop uses `64bit`,
`extract_dir` with the same ZIP root, `bin: sodapop.exe`, and the same digest.
Both retain notices links and distinguish original MIT source from the separate
bundled runtime terms. There are no installation script hooks or automatic
checksum-only update hooks in the generated manifests.

ZIP channels accept SemVer (including prereleases/build metadata) up to 64
characters. MSI accepts only numeric `MAJOR.MINOR.PATCH`, bounded by
`255.255.65535`; prereleases, metadata, fourth fields, leading zeros, and
out-of-range versions fail explicitly. No lossy version mapping is performed.

## Native Windows prerequisites

Use native Windows x64, PowerShell 7.2 or later, Go from `go.mod`, and a .NET SDK
compatible with WiX 7.0.0. Tool availability is checked, not bootstrapped.
`SODAPOP_RELEASECTL` and `SODAPOP_WINDOWSCTL` are absolute paths to native tools:

```powershell
go build -o .\bin\releasectl.exe ./scripts/releasectl
go build -o .\bin\windowsctl.exe ./scripts/windows
$env:SODAPOP_RELEASECTL = (Resolve-Path .\bin\releasectl.exe).Path
$env:SODAPOP_WINDOWSCTL = (Resolve-Path .\bin\windowsctl.exe).Path
```

For a local portable installation, the destination itself must not exist:

```powershell
pwsh -NoProfile -File scripts/windows/Install-Portable.ps1 `
  -ReleaseDir dist -Manifest dist/sodapop-1.2.3-manifest.json `
  -Destination C:\work\sodapop-portable-1.2.3
```

This is an exact verified extraction, not a PATH/profile/registry mutation.
Execute `C:\work\sodapop-portable-1.2.3\sodapop-1.2.3-windows-amd64\sodapop.exe`.
Keep a previous portable directory intact during an upgrade, extract a new one,
and switch your own command reference only after it succeeds.

## Per-user MSI

`Sodapop.wxs` installs to `%LOCALAPPDATA%\Programs\Sodapop`, with no elevation
requirement. Standard MSI Environment-table operations append/remove only its
user PATH entry. Registry component keypaths are HKCU. There are no installer
custom actions, shell/profile edits, downloads, services, or machine PATH edits.
Uninstall removes MSI-owned files and empty installation directories only. It
does not target `%APPDATA%\sodapop`, `%LOCALAPPDATA%\sodapop`, sessions, runtime
caches, or credential storage.

The UpgradeCode is stable:
`972F78B3-B6A5-5422-AABD-CA10FE7E6241`. ProductCodes are deterministically
version-specific; component identities remain stable by relative payload path.
Major upgrades remove the old version inside the rollback transaction, and
downgrades are blocked. Do not reuse a version for different source bytes.

WiX is pinned to **7.0.0** in `.config/dotnet-tools.json`; `nuget.config` uses
only the official NuGet feed and requires signed packages. Nothing uses a global
tool install, wildcard tool version, implicit restore, or harvesting DLLs from a
build directory. Restore is a separate explicit CI step:

```powershell
pwsh -NoProfile -File scripts/windows/Restore-Wix.ps1
pwsh -NoProfile -File scripts/windows/Build-Msi.ps1 `
  -ReleaseDir dist -Manifest dist/sodapop-1.2.3-manifest.json `
  -OutputDir C:\work\msi-1.2.3 -UnsignedCandidate -WixEulaAccepted
```

Review the WiX OSMF EULA/maintenance terms before passing `-WixEulaAccepted`.
Acceptance is scoped to that WiX invocation; this script does not run
`wix eula accept` to persist a user setting.

Output includes the selected payload, `Payload.wxs`, `msi-input.json`,
`sodapop-1.2.3-windows-amd64-unsigned.msi`, and `msi-candidate.json`. Building
without explicit unsigned-candidate consent fails. Candidate metadata records
the actual MSI hash independently from the ZIP, and says `unsigned-candidate`.
Neither ZIP nor MSI candidates are represented as signed production releases.

The parent signing step may sign the **MSI container**, then rename/copy it to
the final filename. Do not sign or change its embedded source executable after
the final release manifest was made. Run completion after container signing:

```powershell
pwsh -NoProfile -File scripts/windows/Complete-Msi.ps1 `
  -Msi C:\work\signed\sodapop-1.2.3-windows-amd64.msi `
  -InputMetadata C:\work\msi-1.2.3\msi-input.json `
  -Output C:\work\signed\sodapop-1.2.3-msi.json `
  -SignerThumbprint EXPECTED_40_HEX_CERTIFICATE_THUMBPRINT
```

Completion requires valid Authenticode from the explicitly expected signer,
checks unchanged staging, and hashes the final MSI bytes. The recorded
`binary_sha256` is the expected final-release binary digest; the native install
test must confirm it from the actual installed MSI. Completion does not prove
installation behavior or grant publication approval. Rerun native MSI evidence
against the final signed container, not only an earlier unsigned candidate.

## Native evidence (not macOS fixture evidence)

Portable smoke runs `--help`, exact `--version`, and cold/cached
`--check-runtime` from the extracted executable with isolated state and a
system-only PATH. It passes no OAuth tokens or account state, and makes no model
requests:

```powershell
pwsh -NoProfile -File scripts/windows/Test-Native.ps1 `
  -ReleaseDir dist -Manifest dist/sodapop-1.2.3-manifest.json `
  -OutputDir C:\work\portable-evidence-1.2.3
```

Add `-PublicDownload` to download the exact canonical public ZIP/checksum into
the new evidence directory, still using the independently supplied trusted
manifest. Missing public assets and digest mismatches fail.

MSI lifecycle requires **two real, increasing numeric release versions** and
their separate MSI metadata. Run as a **non-elevated user** on a disposable
Windows runner. An elevated token is rejected so it cannot hide per-user defects:

```powershell
$env:SODAPOP_WINDOWS_DISPOSABLE = '1'
pwsh -NoProfile -File scripts/windows/Test-Native.ps1 `
  -ReleaseDir dist -Manifest dist/sodapop-1.2.3-manifest.json `
  -OutputDir C:\work\msi-evidence-1.2.3 -MsiLifecycle -AllowUnsignedCandidate `
  -Msi C:\work\msi-1.2.3\sodapop-1.2.3-windows-amd64-unsigned.msi `
  -MsiMetadata C:\work\msi-1.2.3\msi-candidate.json `
  -PreviousReleaseDir dist -PreviousManifest dist/sodapop-1.2.2-manifest.json `
  -PreviousMsi C:\work\msi-1.2.2\sodapop-1.2.2-windows-amd64-unsigned.msi `
  -PreviousMsiMetadata C:\work\msi-1.2.2\msi-candidate.json
```

`SODAPOP_WINDOWS_DISPOSABLE=1` is mandatory consent, not an inferred CI default.
Before changing installation state, the script refuses any registered or
advertised product with Sodapop's UpgradeCode, including older versions outside
the test pair whose installation directory is missing. A pre-existing
installation directory, test-product registration, or installation PATH entry
also fails. Use numeric test-only `0.0.1` and `0.0.2` releases when testing
unpublished candidates; prerelease labels are never stripped or mapped.

The script performs msiexec install, forced repair after deleting only the
installed command, upgrade, and uninstall. It checks actual installed hashes,
runtime startup, HKCU registered name/version, old registration removal, a
single user PATH entry and its exact removal, unchanged machine PATH, and
preserved configuration/state/cache sentinels. Exit codes 0 and 3010 are
recorded explicitly (3010 records reboot required); other codes/timeouts fail.
On failure, keep logs and discard the disposable runner. There is no speculative
cleanup of a user's existing install or recursive deletion of user directories.

## WinGet and Scoop opt-in tests

Manifest validation and channel installation are separate evidence. This
validates locally generated multi-YAML using an already installed WinGet:

```powershell
pwsh -NoProfile -File scripts/windows/Test-Channels.ps1 `
  -ReleaseDir dist -Manifest dist/sodapop-1.2.3-manifest.json `
  -OutputDir C:\work\winget-validation-1.2.3 -ValidateWinGet
```

Only on a disposable, non-elevated Windows user/runner:

```powershell
$env:SODAPOP_WINDOWS_DISPOSABLE = '1'
pwsh -NoProfile -File scripts/windows/Test-Channels.ps1 `
  -ReleaseDir dist -Manifest dist/sodapop-1.2.3-manifest.json `
  -OutputDir C:\work\channel-install-1.2.3 -InstallWinGet -TestScoop
```

These tests require WinGet and/or Scoop **already installed**. The runner owner
must have separately provisioned WinGet's local-manifest policy. The scripts
never enable that policy, install Scoop, or edit execution policy. They install
from the generated local manifests, exercise both the installed exact binary
and the manager's command link/shim, and uninstall only that package. Canonical
public download URLs must already serve the matching ZIP; a candidate not yet
public cannot pass these public-URL installation tests.

A successful `winget validate` is not community acceptance. A local-manifest
install is not evidence that `winget install VeVarunSharma.Sodapop` resolves in
the community registry or that an owned Scoop bucket is public. Those are
separate owner-reviewed publication/acceptance gates. Selected missing
prerequisites always fail, rather than silently skipping.

## Authoritative references

- [WiX tools, local SDK/tool usage and package signatures](https://docs.firegiant.com/wix/using-wix/)
- [WiX Package scope and identity](https://docs.firegiant.com/wix/schema/wxs/package/)
- [WiX Environment table authoring](https://docs.firegiant.com/wix/schema/wxs/environment/)
- [WiX 7 release and EULA changes](https://docs.firegiant.com/wix/whatsnew/)
- [Pinned WiX 7 command-line EULA handling](https://github.com/wixtoolset/wix/blob/v7.0.0/src/wix/WixToolset.Core/CommandLine/CommandLine.cs)
- [Windows Installer ProductVersion restrictions](https://learn.microsoft.com/en-us/windows/win32/msi/productversion)
- [Windows Installer related-product enumeration](https://learn.microsoft.com/en-us/windows/win32/msi/installer-relatedproducts)
- [Official WinGet v1.9.0 installer schema](https://github.com/microsoft/winget-cli/blob/master/schemas/JSON/manifests/v1.9.0/manifest.installer.1.9.0.json)
- [Official Scoop manifest schema](https://github.com/ScoopInstaller/Scoop/blob/master/schema.json)

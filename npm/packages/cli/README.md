# `@sodapop/cli`

The npm launcher for [Sodapop](https://sodapop.sh), a terminal
application backed by the official GitHub Copilot SDK.

After the corresponding packages are published, install with
`npm install --global @sodapop/cli` and run `sodapop`.

The package installs an exact-version native payload through an
OS/architecture-specific optional dependency. It does not download executables
from a release URL at installation or first run. Available targets are declared
by this package's release metadata:

- macOS on Apple silicon or Intel
- glibc-based Linux on arm64 or amd64
- Windows x64, only in releases with a qualified Windows payload

Other operating system or CPU combinations, musl Linux, and ambiguous Linux
libc detection are not supported.
Installing with optional dependencies disabled leaves no native payload and
causes the launcher to report a corrective error. There is no installed Copilot
fallback. The launcher checks exact versions, platform metadata, and the native
binary SHA-256 before launch. Node 18 or later is needed; Go is not required.

The launcher preserves terminal input/output, working directory, arguments, and
native exit status. Uninstalling the npm packages does not remove Sodapop state,
runtime caches, credentials, or shell profiles.

Sodapop's original code is MIT-licensed. Native optional dependencies are
composite bundles whose upstream licenses and runtime terms are included in
their `LICENSES/` and `THIRD_PARTY_NOTICES.md`; they are not licensed only as MIT.
Sodapop is not an official GitHub product.

Source, documentation, and license information are available in the
[Sodapop repository](https://github.com/VeVarunSharma/sodapop).

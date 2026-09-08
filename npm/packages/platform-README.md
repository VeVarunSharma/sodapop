# Sodapop native payload

This package contains the native Sodapop executable for one supported platform.
It is installed automatically by `@sodapop-sh/cli` and is not intended to be used
directly.

The executable is copied from an exact-version Sodapop release archive only
after the shared release verifier confirms the manifest's version, platform,
archive SHA-256, and binary SHA-256. The launcher also verifies the installed
binary hash and exact-version metadata before running it.

The package includes the project license, third-party notices, dependency
licenses, and bundled runtime license material. This is a composite bundle:
Sodapop's original code is MIT, while upstream dependencies and the Copilot
runtime retain their own terms. The entire payload is **not MIT-only**.
See `THIRD_PARTY_NOTICES.md`, `LICENSE`, and `LICENSES/`.

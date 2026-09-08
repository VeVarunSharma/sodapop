#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
target="${SODAPOP_TARGET:-$(go env GOHOSTOS)/$(go env GOHOSTARCH)}"
case "$target" in
  darwin/arm64|darwin/amd64|linux/arm64|linux/amd64|windows/amd64) ;;
  *) printf 'Unsupported Sodapop target: %s\n' "$target" >&2; exit 1 ;;
esac
version="$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/runtimebundle/version.go)"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'Invalid pinned runtime version\n' >&2
  exit 1
fi
GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" \
  go tool bundler --cli-version "$version" --platform "$target" --output internal/runtimebundle

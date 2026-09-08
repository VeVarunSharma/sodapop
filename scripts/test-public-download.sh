#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
version="${1:-}"
repository="${SODAPOP_RELEASE_REPOSITORY:-VeVarunSharma/sodapop}"
host="$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
target="${SODAPOP_TARGET:-$host}"
if [[ $# != 1 || ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9]+([.-][A-Za-z0-9]+)*)?$ ]]; then
  printf 'Usage: bash scripts/test-public-download.sh <version without v prefix>\n' >&2
  exit 2
fi
if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  printf 'SODAPOP_RELEASE_REPOSITORY must be an owner/repository name.\n' >&2
  exit 2
fi
case "$target" in
  darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) extension=tar.gz ;;
  windows/amd64) extension=zip ;;
  *) printf 'Unsupported native download target: %s\n' "$target" >&2; exit 2 ;;
esac
if [[ "$target" != "$host" ]]; then
  printf 'A native download check must run on %s, not %s.\n' "$target" "$host" >&2
  exit 2
fi
suffix=""
if [[ "${host%/*}" == windows ]]; then suffix=".exe"; fi
helper="${SODAPOP_RELEASECTL:-$PWD/bin/releasectl$suffix}"
checker="${SODAPOP_INSTALLCHECK:-$PWD/bin/installcheck$suffix}"
if [[ ! -f "$helper" || ! -f "$checker" ]]; then
  printf 'Build release tools with make release-tools before testing a public download.\n' >&2
  exit 2
fi

temporary="$(mktemp -d "${TMPDIR:-/tmp}/sodapop-public-download.XXXXXX")"
trap 'rm -rf -- "$temporary"' EXIT
name="sodapop-${version}-${target%/*}-${target#*/}.$extension"
manifest="sodapop-${version}-manifest.json"
base="https://github.com/$repository/releases/download/v$version"
for asset in "$manifest" "$name" "$name.sha256"; do
  # Ignore user curl configuration; exercise the public URL without a GitHub token.
  curl -q --fail --show-error --silent --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 15 --max-time 300 --output "$temporary/$asset" "$base/$asset"
done
"$helper" verify --dir "$temporary" --manifest "$temporary/$manifest" --platform "$target"
"$checker" --release-dir "$temporary" --manifest "$temporary/$manifest" --tool "$helper"

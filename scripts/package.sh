#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
target="${SODAPOP_TARGET:-$(go env GOHOSTOS)/$(go env GOHOSTARCH)}"
version="${SODAPOP_VERSION:-dev}"
client_id="${SODAPOP_GITHUB_CLIENT_ID:-}"
case "$target" in
  darwin/arm64|darwin/amd64|linux/arm64|linux/amd64|windows/amd64) ;;
  *) printf 'Unsupported Sodapop target: %s\n' "$target" >&2; exit 1 ;;
esac
if [[ ! "$version" =~ ^[A-Za-z0-9._+-]+$ ]]; then
  printf 'Invalid SODAPOP_VERSION\n' >&2
  exit 1
fi
if [[ -z "$client_id" ]]; then
  printf 'Release packaging requires SODAPOP_GITHUB_CLIENT_ID\n' >&2
  exit 1
fi
if [[ ! "$client_id" =~ ^[A-Za-z0-9_.-]+$ ]]; then
  printf 'SODAPOP_GITHUB_CLIENT_ID must be a public client ID, not a secret or token\n' >&2
  exit 1
fi
helper=(go run ./scripts/releasectl)
if [[ -n "${SODAPOP_RELEASECTL:-}" ]]; then
  helper=("$SODAPOP_RELEASECTL")
fi
"${helper[@]}" validate --version "$version" --platform "$target"
name="sodapop-${version}-${target%/*}-${target#*/}"
extension="tar.gz"
if [[ "${target%/*}" == "windows" ]]; then
  extension="zip"
fi
for output in "dist/$name.$extension" "dist/$name.$extension.sha256"; do
  if [[ -e "$output" || -L "$output" ]]; then
    printf 'Refusing to overwrite existing package output: %s\n' "$output" >&2
    exit 1
  fi
done
mkdir -p dist
staging="$(mktemp -d "dist/.sodapop-package.XXXXXX")"
trap 'rm -rf -- "$staging"' EXIT
stage="$staging/$name"
mkdir -p "$stage"
binary="sodapop"
if [[ "${target%/*}" == "windows" ]]; then
  binary="sodapop.exe"
fi
SODAPOP_TARGET="$target" SODAPOP_OUTPUT="$stage/$binary" bash scripts/build.sh
COPYFILE_DISABLE=1 cp README.md THIRD_PARTY_NOTICES.md LICENSE "$stage/"
mkdir -p "$stage/docs"
COPYFILE_DISABLE=1 cp docs/authentication.md docs/architecture.md docs/live-qualification.md docs/branding.md "$stage/docs/"
mkdir -p "$stage/images"
COPYFILE_DISABLE=1 cp images/sodapop-title.gif images/sodapop-title.png "$stage/images/"
mkdir -p "$stage/docs/vhs" "$stage/docs/assets/demos"
COPYFILE_DISABLE=1 cp docs/vhs/README.md "$stage/docs/vhs/"
for clip in overview commands themes diff; do
  COPYFILE_DISABLE=1 cp "docs/assets/demos/$clip.gif" "docs/assets/demos/$clip.png" "$stage/docs/assets/demos/"
done
go run ./scripts/notices.go "$stage/LICENSES"
runtime_version="$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/runtimebundle/version.go)"
runtime_license="internal/runtimebundle/zcopilot_${runtime_version}_${target%/*}_${target#*/}.license"
if [[ "${target%/*}" == "windows" ]]; then
  runtime_license="internal/runtimebundle/zcopilot_${runtime_version}_${target%/*}_${target#*/}.exe.license"
fi
cp "$runtime_license" "$stage/LICENSES/copilot-runtime.license"
"${helper[@]}" archive --stage "$stage" --dir dist --version "$version" --platform "$target"

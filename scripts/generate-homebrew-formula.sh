#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
source scripts/homebrew/common.sh

usage() {
  printf 'Usage: %s [--check] <version> [output]\n' "$0" >&2
  exit 2
}

mode=generate
if [[ "${1:-}" == "--check" ]]; then
  mode=check
  shift
fi
[[ $# -ge 1 && $# -le 2 ]] || usage

version="$1"
output="${2-}"
if [[ -z "$output" ]]; then
  output="dist/homebrew/Formula/sodapop.rb"
fi
template="packaging/homebrew/Formula/sodapop.rb.tmpl"
release_base="${SODAPOP_HOMEBREW_URL_BASE:-https://github.com/VeVarunSharma/sodapop/releases/download/v${version}}"
checksum_dir="${SODAPOP_HOMEBREW_CHECKSUM_DIR:-}"
release_dir="${SODAPOP_HOMEBREW_RELEASE_DIR:-}"
manifest="${SODAPOP_HOMEBREW_MANIFEST:-}"
downloaded_manifest=
temporary=
release_base_ruby=

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
  printf 'Invalid Homebrew release version %q; use a version such as 1.2.3 or 1.2.3-rc.1\n' "$version" >&2
  exit 1
fi
if [[ "$(homebrew_canonical_path "$output")" == "$(homebrew_canonical_path "$template")" ]]; then
  printf 'Refusing to overwrite the Homebrew formula template\n' >&2
  exit 1
fi
release_base="$(homebrew_normalize_release_base "$release_base" "$version")"
if [[ -n "$checksum_dir" && ! -d "$checksum_dir" ]]; then
  printf 'Homebrew checksum directory does not exist: %s\n' "$checksum_dir" >&2
  exit 1
fi
if [[ -n "$manifest" || -n "$release_dir" ]]; then
  if [[ -z "$manifest" || -z "$release_dir" ]]; then
    printf 'SODAPOP_HOMEBREW_MANIFEST and SODAPOP_HOMEBREW_RELEASE_DIR must be provided together\n' >&2
    exit 1
  fi
  if [[ ! -f "$manifest" || ! -d "$release_dir" ]]; then
    printf 'Homebrew manifest mode needs an existing manifest file and release directory\n' >&2
    exit 1
  fi
fi
if [[ -z "$checksum_dir" && -z "$manifest" ]] && ! command -v curl >/dev/null 2>&1; then
  printf 'curl is required to read tagged GitHub Release checksums\n' >&2
  exit 1
fi

read_checksum() {
  local target="$1"
  local archive="sodapop-${version}-${target}.tar.gz"
  local sidecar="${archive}.sha256"
  local content
  if [[ -n "$checksum_dir" ]]; then
    if [[ ! -f "$checksum_dir/$sidecar" ]]; then
      printf 'Missing Homebrew checksum sidecar: %s\n' "$checksum_dir/$sidecar" >&2
      return 1
    fi
    content="$(<"$checksum_dir/$sidecar")"
  else
    content="$(curl -fsSL "${release_base}/${sidecar}")"
  fi
  if [[ "$content" == *$'\n'* ]]; then
    printf 'Checksum sidecar must contain exactly one entry: %s\n' "$sidecar" >&2
    return 1
  fi

  local digest filename extra
  read -r digest filename extra <<<"$content"
  if [[ -n "${extra:-}" || "$filename" != "$archive" || ! "$digest" =~ ^[0-9a-f]{64}$ ]]; then
    printf 'Invalid checksum sidecar for %s; expected "<sha256>  %s"\n' "$archive" "$archive" >&2
    return 1
  fi
  printf '%s' "$digest"
}

manifest_cleanup() {
  rm -f -- "$downloaded_manifest"
  rm -f -- "$temporary"
}
trap manifest_cleanup EXIT

sdk_version=
runtime_version=
manifest_remote=0
if [[ -n "$manifest" ]]; then
  manifest="$(homebrew_canonical_path "$manifest")"
  release_dir="$(homebrew_canonical_path "$release_dir")"
  for platform in "${homebrew_formula_platforms[@]}"; do
    homebrew_releasectl verify --dir "$release_dir" --manifest "$manifest" --platform "$platform" >/dev/null
  done
  IFS=$'\t' read -r manifest_version sdk_version runtime_version <<<"$(homebrew_manifest_header "$manifest")"
  if [[ "$manifest_version" != "$version" ]]; then
    printf 'Homebrew manifest version %q does not match requested version %q\n' "$manifest_version" "$version" >&2
    exit 1
  fi
elif [[ -n "$checksum_dir" ]]; then
  IFS=$'\t' read -r sdk_version runtime_version <<<"$(homebrew_source_pins)"
else
  downloaded_manifest="$(mktemp)"
  curl -fsSL "${release_base}/sodapop-${version}-manifest.json" >"$downloaded_manifest"
  manifest="$downloaded_manifest"
  manifest_remote=1
  IFS=$'\t' read -r manifest_version sdk_version runtime_version <<<"$(homebrew_manifest_header "$manifest")"
  if [[ "$manifest_version" != "$version" ]]; then
    printf 'Tagged release manifest version %q does not match requested version %q\n' "$manifest_version" "$version" >&2
    exit 1
  fi
fi

if [[ ! "$sdk_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]] ||
  [[ ! "$runtime_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
  printf 'Homebrew formula metadata needs valid Copilot SDK/runtime versions\n' >&2
  exit 1
fi

darwin_arm64_sha256=
darwin_amd64_sha256=
linux_arm64_sha256=
linux_amd64_sha256=
release_base_ruby="$(homebrew_escape_ruby_string "$release_base")"
for platform in "${homebrew_formula_platforms[@]}"; do
  target="${platform/\//-}"
  archive="$(homebrew_expected_archive "$version" "$platform")"
  checksum=
  if [[ -n "$manifest" ]]; then
    IFS=$'\t' read -r manifest_archive manifest_archive_sha _ <<<"$(homebrew_manifest_artifact "$manifest" "$platform")"
    if [[ "$manifest_archive" != "$archive" ]]; then
      printf 'Homebrew manifest archive %q does not match %q for %s\n' "$manifest_archive" "$archive" "$platform" >&2
      exit 1
    fi
    checksum="$manifest_archive_sha"
  fi
  if (( manifest_remote == 1 )); then
    checksum_sidecar="$(read_checksum "$target")"
    if [[ "$checksum_sidecar" != "$manifest_archive_sha" ]]; then
      printf 'Tagged release checksum for %s does not match the manifest\n' "$platform" >&2
      exit 1
    fi
    checksum="$checksum_sidecar"
  elif [[ -z "$checksum" ]]; then
    checksum="$(read_checksum "$target")"
  fi
  if [[ -n "$manifest" && "$checksum" != "$manifest_archive_sha" ]]; then
    printf 'Tagged release checksum for %s does not match the manifest\n' "$platform" >&2
    exit 1
  fi
  case "$target" in
    darwin-arm64) darwin_arm64_sha256="$checksum" ;;
    darwin-amd64) darwin_amd64_sha256="$checksum" ;;
    linux-arm64) linux_arm64_sha256="$checksum" ;;
    linux-amd64) linux_amd64_sha256="$checksum" ;;
  esac
done

release_base_sed="$(homebrew_escape_sed_replacement "$release_base_ruby")"
version_sed="$(homebrew_escape_sed_replacement "$version")"
sdk_version_sed="$(homebrew_escape_sed_replacement "$sdk_version")"
runtime_version_sed="$(homebrew_escape_sed_replacement "$runtime_version")"
darwin_arm64_sed="$(homebrew_escape_sed_replacement "$darwin_arm64_sha256")"
darwin_amd64_sed="$(homebrew_escape_sed_replacement "$darwin_amd64_sha256")"
linux_arm64_sed="$(homebrew_escape_sed_replacement "$linux_arm64_sha256")"
linux_amd64_sed="$(homebrew_escape_sed_replacement "$linux_amd64_sha256")"

for placeholder in \
  '@RELEASE_BASE@' \
  '@VERSION@' \
  '@COPILOT_SDK_VERSION@' \
  '@COPILOT_RUNTIME_VERSION@' \
  '@DARWIN_ARM64_SHA256@' \
  '@DARWIN_AMD64_SHA256@' \
  '@LINUX_ARM64_SHA256@' \
  '@LINUX_AMD64_SHA256@'
do
  if ! grep -Fq "$placeholder" "$template"; then
    printf 'Homebrew formula template is missing %s\n' "$placeholder" >&2
    exit 1
  fi
done

mkdir -p "$(dirname "$output")"
temporary="$(mktemp "${output}.tmp.XXXXXX")"

sed \
  -e "s|@RELEASE_BASE@|$release_base_sed|g" \
  -e "s|@VERSION@|$version_sed|g" \
  -e "s|@COPILOT_SDK_VERSION@|$sdk_version_sed|g" \
  -e "s|@COPILOT_RUNTIME_VERSION@|$runtime_version_sed|g" \
  -e "s|@DARWIN_ARM64_SHA256@|$darwin_arm64_sed|g" \
  -e "s|@DARWIN_AMD64_SHA256@|$darwin_amd64_sed|g" \
  -e "s|@LINUX_ARM64_SHA256@|$linux_arm64_sed|g" \
  -e "s|@LINUX_AMD64_SHA256@|$linux_amd64_sed|g" \
  "$template" >"$temporary"

if grep -Eq '@[A-Z0-9_]+@' "$temporary"; then
  printf 'Homebrew formula template contains an unresolved placeholder\n' >&2
  exit 1
fi

if [[ "$mode" == check ]]; then
  if [[ ! -f "$output" ]] || ! cmp -s "$temporary" "$output"; then
    printf 'Homebrew formula is stale for v%s: %s\n' "$version" "$output" >&2
    exit 1
  fi
  printf 'Homebrew formula is current for v%s: %s\n' "$version" "$output"
else
  chmod 0644 "$temporary"
  mv -f -- "$temporary" "$output"
  temporary=
  printf 'Generated Homebrew formula for v%s: %s\n' "$version" "$output"
fi

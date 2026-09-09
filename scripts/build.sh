#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
target="${SODAPOP_TARGET:-$(go env GOHOSTOS)/$(go env GOHOSTARCH)}"
version="${SODAPOP_VERSION:-dev}"
client_id="${SODAPOP_GITHUB_CLIENT_ID:-}"
output="${SODAPOP_OUTPUT:-}"
prepared_runtime="${SODAPOP_PREPARED_RUNTIME:-0}"
if [[ -z "$output" ]]; then
  if [[ "${target%/*}" == "windows" ]]; then
    output="bin/sodapop.exe"
  else
    output="bin/sodapop"
  fi
fi
if [[ ! "$version" =~ ^[A-Za-z0-9._+-]+$ ]]; then
  printf 'SODAPOP_VERSION contains unsupported characters\n' >&2
  exit 1
fi
if [[ ! "$client_id" =~ ^[A-Za-z0-9_.-]*$ ]]; then
  printf 'SODAPOP_GITHUB_CLIENT_ID must be a public client ID, not a secret or token\n' >&2
  exit 1
fi
if [[ "$prepared_runtime" != 0 && "$prepared_runtime" != 1 ]]; then
  printf 'SODAPOP_PREPARED_RUNTIME must be 0 or 1\n' >&2
  exit 1
fi
if [[ "$prepared_runtime" == 1 ]]; then
  runtime_version="$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/runtimebundle/version.go)"
  target_key="${target/\//_}"
  for source in \
    "internal/runtimebundle/zcopilot_${target_key}.go" \
    "internal/runtimebundle/zcopilot_inprocess_${target_key}.go"; do
    if [[ ! -s "$source" ]] || ! grep -Fq "Version: \"$runtime_version\"" "$source"; then
      printf 'Prepared runtime source is missing or does not match %s for %s: %s\n' "$runtime_version" "$target" "$source" >&2
      exit 1
    fi
    while IFS= read -r asset; do
      if [[ -z "$asset" || ! -s "internal/runtimebundle/$asset" ]]; then
        printf 'Prepared runtime asset is missing for %s: %s\n' "$target" "${asset:-<empty>}" >&2
        exit 1
      fi
    done < <(sed -n 's|^//go:embed[[:space:]][[:space:]]*||p' "$source")
  done
else
  SODAPOP_TARGET="$target" bash scripts/bundle.sh
fi
mkdir -p "$(dirname "$output")"
link_flags="-s -w -X github.com/VeVarunSharma/sodapop/internal/app.Version=$version"
link_flags="$link_flags -X github.com/VeVarunSharma/sodapop/internal/app.OAuthClientID=$client_id"
CGO_ENABLED=0 GOOS="${target%/*}" GOARCH="${target#*/}" \
  go build -trimpath -ldflags "$link_flags" -o "$output" ./cmd/sodapop
printf 'Built %s for %s (Copilot runtime bundled)\n' "$output" "$target"
if [[ -z "$client_id" ]]; then
  printf 'Native sign-in needs a Sodapop-owned client ID: set SODAPOP_GITHUB_CLIENT_ID at launch or build time.\n'
fi

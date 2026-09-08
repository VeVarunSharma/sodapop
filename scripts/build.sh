#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
target="${SODAPOP_TARGET:-$(go env GOHOSTOS)/$(go env GOHOSTARCH)}"
version="${SODAPOP_VERSION:-dev}"
client_id="${SODAPOP_GITHUB_CLIENT_ID:-}"
output="${SODAPOP_OUTPUT:-}"
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
SODAPOP_TARGET="$target" bash scripts/bundle.sh
mkdir -p "$(dirname "$output")"
link_flags="-s -w -X github.com/VeVarunSharma/sodapop/internal/app.Version=$version"
link_flags="$link_flags -X github.com/VeVarunSharma/sodapop/internal/app.OAuthClientID=$client_id"
CGO_ENABLED=0 GOOS="${target%/*}" GOARCH="${target#*/}" \
  go build -trimpath -ldflags "$link_flags" -o "$output" ./cmd/sodapop
printf 'Built %s for %s (Copilot runtime bundled)\n' "$output" "$target"
if [[ -z "$client_id" ]]; then
  printf 'Native sign-in needs a Sodapop-owned client ID: set SODAPOP_GITHUB_CLIENT_ID at launch or build time.\n'
fi

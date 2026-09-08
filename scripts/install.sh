#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
destination="${SODAPOP_INSTALL_DIR:-${HOME}/.local/bin}"
source="${SODAPOP_OUTPUT:-bin/sodapop}"
host="$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
case "$host" in
  darwin/arm64|darwin/amd64|linux/arm64|linux/amd64) ;;
  *) printf 'make install supports macOS and Linux; use the native Windows ZIP or installer instead.\n' >&2; exit 1 ;;
esac
if [[ "${SODAPOP_TARGET:-$host}" != "$host" ]]; then
  printf 'Refusing to install a cross-target binary on this host; use make package instead.\n' >&2
  exit 1
fi
if [[ ! -f "$source" || ! -x "$source" ]]; then
  printf 'Build the Sodapop executable before installing it.\n' >&2
  exit 1
fi
installed="$destination/sodapop"
if [[ -L "$installed" || ( -e "$installed" && ! -f "$installed" ) ]]; then
  printf 'Refusing to replace a link or non-regular installation path: %s\n' "$installed" >&2
  exit 1
fi
mkdir -p "$destination"
temporary="$(mktemp "$destination/.sodapop-install.XXXXXX")"
trap 'rm -f -- "$temporary"' EXIT
install -m 0755 "$source" "$temporary"
mv -f -- "$temporary" "$installed"
printf 'Installed %s/sodapop\n' "$destination"
case ":${PATH}:" in
  *":${destination}:"*) printf 'Run sodapop from your project directory.\n' ;;
  *) printf 'Add %s to PATH, then run sodapop from your project directory.\n' "$destination" ;;
esac

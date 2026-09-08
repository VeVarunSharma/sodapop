#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
root="$(pwd -P)"
case "${SODAPOP_DEMO:-all}" in
  all) clips=(overview commands themes diff) ;;
  overview|commands|themes|diff) clips=("$SODAPOP_DEMO") ;;
  *) printf 'SODAPOP_DEMO must be all, overview, commands, themes, or diff\n' >&2; exit 1 ;;
esac
if (( $# != 0 )); then
  printf 'Use SODAPOP_DEMO to select a recording; positional arguments are not supported\n' >&2
  exit 1
fi
for tool in go git vhs ttyd ffmpeg; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    printf 'Missing recording dependency: %s (see docs/vhs/README.md)\n' "$tool" >&2
    exit 1
  fi
done
read -r vhs_version < docs/vhs/vhs-version
if [[ "$(vhs --version)" != "vhs version $vhs_version" ]]; then
  printf 'Recordings require VHS %s; see docs/vhs/README.md\n' "$vhs_version" >&2
  exit 1
fi

temp_root="${SODAPOP_DEMO_TMPDIR:-/tmp}"
if [[ "$temp_root" != /* || ! -d "$temp_root" ]]; then
  printf 'SODAPOP_DEMO_TMPDIR must be an existing absolute directory\n' >&2
  exit 1
fi
work="$(mktemp -d "$temp_root/sodapop-demos.XXXXXX")"
work="$(cd "$work" && pwd -P)"
trap 'rm -rf -- "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -p "$work/bin" "$work/output" "$work/tmp" "$work/empty-template"
CGO_ENABLED=0 GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" \
  go build -trimpath -o "$work/bin/sodapop-recording" ./scripts/recording

isolated() {
  env -i \
    "PATH=$work/bin:$PATH" "HOME=$home" \
    "XDG_CONFIG_HOME=$home/.config" "XDG_STATE_HOME=$home/.local/state" \
    "XDG_CACHE_HOME=$home/.cache" "TMPDIR=$work/tmp" \
    "USER=demo" "LOGNAME=demo" "SHELL=/bin/bash" \
    "LANG=en_US.UTF-8" "LC_ALL=en_US.UTF-8" "TZ=UTC" \
    "TERM=xterm-256color" "COLORTERM=truecolor" \
    "GIT_CONFIG_NOSYSTEM=1" "GIT_CONFIG_GLOBAL=/dev/null" "GIT_TERMINAL_PROMPT=0" \
    "GIT_AUTHOR_NAME=Sodapop Demo" "GIT_AUTHOR_EMAIL=demo@example.invalid" \
    "GIT_COMMITTER_NAME=Sodapop Demo" "GIT_COMMITTER_EMAIL=demo@example.invalid" \
    "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z" "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z" \
    "$@"
}

for clip in "${clips[@]}"; do
  home="$work/$clip"
  project="$home/soda-shop"
  mkdir -p "$project" "$home/output" "$home/.config" "$home/.local/state" "$home/.cache"
  cp "docs/vhs/$clip.tape" docs/vhs/common.tape "$home/"
  cp docs/vhs/fixtures/main.go.initial "$project/main.go"
  isolated git -C "$project" init --quiet --initial-branch=demo --template="$work/empty-template"
  isolated git -C "$project" add main.go
  isolated git -C "$project" -c core.hooksPath=/dev/null -c commit.gpgsign=false \
    commit --quiet -m 'Prepare the local recording fixture' \
    -m 'Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>'
  cp docs/vhs/fixtures/main.go.staged "$project/main.go"
  isolated git -C "$project" add main.go
  cp docs/vhs/fixtures/main.go.working "$project/main.go"
  printf 'Recording %s...\n' "$clip"
  (
    cd "$home"
    isolated vhs validate "$clip.tape"
    isolated vhs --quiet "$clip.tape"
  )
  for extension in gif png; do
    artifact="$home/output/$clip.$extension"
    if [[ ! -s "$artifact" ]]; then
      printf 'Recording did not produce %s.%s; existing assets are unchanged\n' "$clip" "$extension" >&2
      exit 1
    fi
    if [[ "$extension" == gif ]] && (( $(wc -c < "$artifact") > 3 * 1024 * 1024 )); then
      printf '%s.gif exceeds the 3 MiB README budget; existing assets are unchanged\n' "$clip" >&2
      exit 1
    fi
    cp "$artifact" "$work/output/"
  done
done

mkdir -p "$root/docs/assets/demos"
for clip in "${clips[@]}"; do
  for extension in gif png; do
    cp "$work/output/$clip.$extension" "$root/docs/assets/demos/"
  done
done
printf 'Recorded %s demo(s) in docs/assets/demos\n' "${#clips[@]}"

#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
source scripts/homebrew/common.sh

usage() {
  printf 'Usage: %s --release-dir DIR --manifest FILE [--previous-release-dir DIR --previous-manifest FILE]\n' "$0" >&2
  exit 2
}

release_dir=
manifest=
previous_release_dir=
previous_manifest=
while (( $# )); do
  case "$1" in
    --release-dir)
      [[ $# -ge 2 ]] || usage
      release_dir="$2"
      shift 2
      ;;
    --manifest)
      [[ $# -ge 2 ]] || usage
      manifest="$2"
      shift 2
      ;;
    --previous-release-dir)
      [[ $# -ge 2 ]] || usage
      previous_release_dir="$2"
      shift 2
      ;;
    --previous-manifest)
      [[ $# -ge 2 ]] || usage
      previous_manifest="$2"
      shift 2
      ;;
    *)
      usage
      ;;
  esac
done

if [[ -z "$release_dir" || -z "$manifest" ]]; then
  usage
fi
if [[ -n "$previous_release_dir" || -n "$previous_manifest" ]]; then
  if [[ -z "$previous_release_dir" || -z "$previous_manifest" ]]; then
    printf 'Provide both --previous-release-dir and --previous-manifest for an upgrade check\n' >&2
    exit 2
  fi
fi

brew_cmd="${SODAPOP_HOMEBREW_BREW:-brew}"
if ! command -v "$brew_cmd" >/dev/null 2>&1; then
  printf 'Homebrew is required for native install testing\n' >&2
  exit 2
fi

temporary="$(mktemp -d "${TMPDIR:-/tmp}/sodapop-homebrew.XXXXXX")"
installed_formula=0
install_attempted=0
tapped_formula=0
owned_collision_sentinel=0
collision_sentinel_sha=
tap_name="sodapop/test"
tap_formula_ref="$tap_name/sodapop"
tap_repo="$temporary/homebrew-sodapop-test"
tap_checkout=
formula_name="sodapop"

run_brew() {
  "$brew_cmd" "$@"
}

brew_formula_installed() {
  local formulae
  if ! formulae="$(run_brew list --formula)"; then
    printf 'Could not query installed Homebrew formulae\n' >&2
    return 2
  fi
  grep -Fxq "$formula_name" <<<"$formulae"
}

cleanup() {
  local status=$?
  local cleanup_failed=0
  set +e
  if [[ "$install_attempted" == 1 ]]; then
    if brew_formula_installed; then
      if ! "$brew_cmd" uninstall --formula --force "$tap_formula_ref" >/dev/null 2>&1; then
        printf 'Homebrew cleanup failed to uninstall %s\n' "$tap_formula_ref" >&2
        cleanup_failed=1
      fi
    elif [[ $? -eq 2 ]]; then
      cleanup_failed=1
    fi
  fi
  if [[ "${tapped_formula:-0}" == 1 ]]; then
    if ! "$brew_cmd" untap --force "$tap_name" >/dev/null 2>&1; then
      printf 'Homebrew cleanup failed to untap %s\n' "$tap_name" >&2
      cleanup_failed=1
    fi
  fi
  if [[ "${owned_collision_sentinel:-0}" == 1 && -f "${collision_path:-}" && ! -L "${collision_path:-}" ]]; then
    if [[ "$(homebrew_sha256_file "$collision_path" 2>/dev/null || true)" == "$collision_sentinel_sha" ]]; then
      rm -f -- "$collision_path"
    fi
  fi
  rm -rf -- "$temporary"
  trap - EXIT
  if (( cleanup_failed != 0 && status == 0 )); then
    exit 1
  fi
  exit "$status"
}
trap cleanup EXIT

release_dir="$(homebrew_canonical_path "$release_dir")"
manifest="$(homebrew_canonical_path "$manifest")"
if [[ ! -d "$release_dir" || ! -f "$manifest" ]]; then
  printf 'Current release inputs must exist on disk\n' >&2
  exit 2
fi
if [[ -n "$previous_release_dir" ]]; then
  previous_release_dir="$(homebrew_canonical_path "$previous_release_dir")"
  previous_manifest="$(homebrew_canonical_path "$previous_manifest")"
  if [[ ! -d "$previous_release_dir" || ! -f "$previous_manifest" ]]; then
    printf 'Previous release inputs must exist on disk\n' >&2
    exit 2
  fi
fi

host="$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
case "$host" in
  darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) ;;
  *)
    printf 'Homebrew native install checks support only the four UNIX release platforms, not %s\n' "$host" >&2
    exit 2
    ;;
esac

prefix="$("$brew_cmd" --prefix)"
if [[ -z "$prefix" ]]; then
  printf 'Could not determine the Homebrew prefix\n' >&2
  exit 2
fi
prefix="$(homebrew_canonical_path "$prefix")"
if [[ "$prefix" != "$temporary" && "$prefix" != "$temporary/"* && "${SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX:-0}" != "1" ]]; then
  printf 'Native Homebrew install testing would mutate the Homebrew prefix %s; use an isolated disposable prefix or set SODAPOP_HOMEBREW_ALLOW_ORDINARY_PREFIX=1 on an ephemeral runner\n' "$prefix" >&2
  exit 2
fi
if brew_formula_installed; then
  printf 'Refusing to test over an existing Homebrew sodapop installation\n' >&2
  exit 2
elif [[ $? -eq 2 ]]; then
  exit 2
fi
if ! existing_taps="$(run_brew tap)"; then
  printf 'Could not query existing Homebrew taps\n' >&2
  exit 2
fi
if grep -Fxq "$tap_name" <<<"$existing_taps"; then
  printf 'Refusing to reuse an existing Homebrew test tap: %s\n' "$tap_name" >&2
  exit 2
fi
home="$temporary/home"
config_home="$temporary/config"
state_home="$temporary/state"
cache_home="$temporary/cache"
mkdir -p "$home" "$config_home" "$state_home" "$cache_home" "$temporary/generated" "$temporary/previous/Formula"
for name in .bashrc .zshrc .profile; do
  printf 'keep %s\n' "$name" >"$home/$name"
done
for path in "$config_home/preferences.json" "$state_home/session.json" "$cache_home/runtime.marker"; do
  mkdir -p "$(dirname "$path")"
  printf 'keep %s\n' "$(basename "$path")" >"$path"
done

export HOME="$home"
export XDG_CONFIG_HOME="$config_home"
export XDG_STATE_HOME="$state_home"
export XDG_CACHE_HOME="$cache_home"
export HOMEBREW_CACHE="$temporary/homebrew-cache"
export HOMEBREW_LOGS="$temporary/homebrew-logs"
export HOMEBREW_NO_ANALYTICS=1
export HOMEBREW_NO_AUTO_UPDATE=1
export HOMEBREW_NO_ENV_HINTS=1
export HOMEBREW_NO_INSTALL_CLEANUP=1
unset HOMEBREW_NO_INSTALL_FROM_API
export HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK=1
export PATH="$prefix/bin:$PATH"

generate_formula() {
  local manifest_path="$1"
  local assets_dir="$2"
  local formula="$3"
  local version sdk runtime
  IFS=$'\t' read -r version sdk runtime <<<"$(homebrew_manifest_header "$manifest_path")"
  SODAPOP_HOMEBREW_MANIFEST="$manifest_path" \
    SODAPOP_HOMEBREW_RELEASE_DIR="$assets_dir" \
    SODAPOP_HOMEBREW_URL_BASE="$(homebrew_file_uri "$assets_dir")" \
    bash scripts/generate-homebrew-formula.sh "$version" "$formula" >/dev/null
}

current_formula="$temporary/tap/Formula/sodapop.rb"
generate_formula "$manifest" "$release_dir" "$current_formula"

previous_formula=
if [[ -n "$previous_manifest" ]]; then
  previous_formula="$temporary/previous/Formula/sodapop.rb"
  generate_formula "$previous_manifest" "$previous_release_dir" "$previous_formula"
fi

tap_commit() {
  local message="$1"
  git -C "$tap_repo" add Formula/sodapop.rb
  if git -C "$tap_repo" rev-parse --verify HEAD >/dev/null 2>&1; then
    if git -C "$tap_repo" diff --cached --quiet; then
      return 0
    fi
  fi
  git -C "$tap_repo" commit -q -m "$message"
}

tap_write_formula() {
  local source_formula="$1"
  mkdir -p "$tap_repo/Formula"
  cp "$source_formula" "$tap_repo/Formula/sodapop.rb"
}

tap_initialize() {
  local source_formula="$1"
  if [[ -e "$tap_repo" || -L "$tap_repo" ]]; then
    printf 'Refusing an existing temporary tap path\n' >&2
    exit 1
  fi
  mkdir -p "$tap_repo/Formula"
  git init -q "$tap_repo"
  git -C "$tap_repo" config user.name "Sodapop Test"
  git -C "$tap_repo" config user.email "sodapop@example.invalid"
  tap_write_formula "$source_formula"
  tap_commit "Initial temporary Homebrew formula"
  run_brew tap --custom-remote "$tap_name" "$tap_repo"
  tap_checkout="$(run_brew --repository "$tap_name")"
  if [[ -z "$tap_checkout" || ! -d "$tap_checkout/.git" ]]; then
    printf 'Could not resolve the tapped Homebrew repository for %s\n' "$tap_name" >&2
    exit 1
  fi
  tapped_formula=1
}

tap_update_formula() {
  local source_formula="$1"
  local branch
  tap_write_formula "$source_formula"
  tap_commit "Update temporary Homebrew formula"
  if [[ -z "$tap_checkout" || ! -d "$tap_checkout/.git" ]]; then
    printf 'Could not update the tapped Homebrew repository for %s\n' "$tap_name" >&2
    exit 1
  fi
  branch="$(git -C "$tap_checkout" symbolic-ref --quiet --short HEAD)"
  if [[ -z "$branch" ]]; then
    printf 'Could not determine the tapped Homebrew branch for %s\n' "$tap_name" >&2
    exit 1
  fi
  git -C "$tap_checkout" fetch --quiet origin "$branch"
  git -C "$tap_checkout" merge --ff-only --quiet "origin/$branch"
}

assert_sentinels() {
  for name in .bashrc .zshrc .profile; do
    if [[ "$(<"$home/$name")" != "keep $name" ]]; then
      printf 'Homebrew install test changed %s\n' "$home/$name" >&2
      exit 1
    fi
  done
  for path in "$config_home/preferences.json" "$state_home/session.json" "$cache_home/runtime.marker"; do
    if [[ "$(<"$path")" != "keep $(basename "$path")" ]]; then
      printf 'Homebrew install test changed %s\n' "$path" >&2
      exit 1
    fi
  done
}

validate_installation() {
  local manifest_path="$1"
  local binary="$prefix/bin/sodapop"
  local installed_share="$prefix/share/sodapop"
  local archive archive_sha binary_sha installed_sha

  IFS=$'\t' read -r archive archive_sha binary_sha <<<"$(homebrew_manifest_artifact "$manifest_path" "$host")"
  if [[ ! -x "$binary" ]]; then
    printf 'Homebrew did not install %s\n' "$binary" >&2
    exit 1
  fi
  installed_sha="$(homebrew_sha256_file "$binary")"
  if [[ "$installed_sha" != "$binary_sha" ]]; then
    printf 'Installed binary hash %s does not match the release manifest %s\n' "$installed_sha" "$binary_sha" >&2
    exit 1
  fi
  if [[ "$(command -v sodapop)" != "$binary" ]]; then
    printf 'The installed command is shadowed on the test PATH\n' >&2
    exit 1
  fi
  local version sdk runtime expected_version
  IFS=$'\t' read -r version sdk runtime <<<"$(homebrew_manifest_header "$manifest_path")"
  expected_version="$(printf 'sodapop %s\nCopilot SDK %s / runtime %s' "$version" "$sdk" "$runtime")"
  if [[ "$(command sodapop --version)" != "$expected_version" ]]; then
    printf 'The command resolved from PATH does not match the installed release\n' >&2
    exit 1
  fi
  for path in "$installed_share/LICENSE" "$installed_share/THIRD_PARTY_NOTICES.md" "$installed_share/LICENSES/copilot-runtime.license"; do
    if [[ ! -e "$path" ]]; then
      printf 'Homebrew did not retain %s\n' "$path" >&2
      exit 1
    fi
  done
  if ! find "$installed_share/LICENSES" -type f ! -name 'copilot-runtime.license' -print -quit | grep -q .; then
    printf 'Homebrew did not retain any dependency notice alongside the Copilot runtime license\n' >&2
    exit 1
  fi
  assert_sentinels
}

collision_path="$prefix/bin/sodapop"
if [[ -e "$collision_path" || -L "$collision_path" ]]; then
  printf 'Homebrew prefix already contains %s; cannot run a clean collision check\n' "$collision_path" >&2
  exit 2
fi
tap_seed_formula="$current_formula"
if [[ -n "$previous_formula" ]]; then
  tap_seed_formula="$previous_formula"
fi
tap_initialize "$tap_seed_formula"
mkdir -p "$(dirname "$collision_path")"
printf '#!/usr/bin/env bash\nprintf "collision sentinel\\n"\n' >"$collision_path"
chmod 0755 "$collision_path"
collision_before="$(homebrew_sha256_file "$collision_path")"
collision_sentinel_sha="$collision_before"
owned_collision_sentinel=1
install_attempted=1
if run_brew install --formula "$tap_formula_ref" >/dev/null 2>&1; then
  printf 'Homebrew unexpectedly installed over an existing PATH owner collision\n' >&2
  exit 1
fi
if [[ ! -f "$collision_path" || -L "$collision_path" ]]; then
  printf 'Homebrew collision check replaced the sentinel at %s\n' "$collision_path" >&2
  exit 1
fi
collision_after="$(homebrew_sha256_file "$collision_path")"
if [[ "$collision_before" != "$collision_after" ]]; then
  printf 'Homebrew collision check changed the pre-existing PATH owner at %s\n' "$collision_path" >&2
  exit 1
fi
rm -f -- "$collision_path"
owned_collision_sentinel=0
if brew_formula_installed; then
  run_brew uninstall --formula --force "$tap_formula_ref" >/dev/null
elif [[ $? -eq 2 ]]; then
  exit 2
fi

if [[ -n "$previous_formula" ]]; then
  run_brew install --formula "$tap_formula_ref"
  installed_formula=1
  run_brew test sodapop
  validate_installation "$previous_manifest"

  tap_update_formula "$current_formula"
  run_brew upgrade --formula "$tap_formula_ref"
  run_brew test sodapop
  validate_installation "$manifest"
else
  run_brew install --formula "$tap_formula_ref"
  installed_formula=1
  run_brew test sodapop
  validate_installation "$manifest"
fi

run_brew reinstall --formula "$tap_formula_ref"
run_brew test sodapop
validate_installation "$manifest"

run_brew uninstall --formula --force "$tap_formula_ref"
installed_formula=0
if brew_formula_installed; then
  printf 'Homebrew uninstall left an installed Sodapop version behind\n' >&2
  exit 1
elif [[ $? -eq 2 ]]; then
  exit 2
fi
install_attempted=0
if [[ -e "$prefix/bin/sodapop" || -e "$prefix/share/sodapop" ]]; then
  printf 'Homebrew uninstall left owned Sodapop files behind\n' >&2
  exit 1
fi
assert_sentinels

if [[ -n "$previous_formula" ]]; then
  printf 'Homebrew install, upgrade, reinstall, and uninstall checks passed for %s using %s\n' "$host" "$brew_cmd"
else
  printf 'Homebrew install, reinstall, and uninstall checks passed for %s using %s\n' "$host" "$brew_cmd"
fi

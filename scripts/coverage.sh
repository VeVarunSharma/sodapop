#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

baseline_file="${SODAPOP_COVERAGE_BASELINE_FILE:-.github/coverage-baseline.txt}"
targets_file="${SODAPOP_COVERAGE_TARGETS_FILE:-.github/coverage-targets.txt}"
go_command="${SODAPOP_GO_COMMAND:-go}"
update_baseline=false

case "${1:-}" in
  "") ;;
  --update-baseline) update_baseline=true ;;
  *)
    printf 'usage: bash scripts/coverage.sh [--update-baseline]\n' >&2
    exit 2
    ;;
esac

profile="$(mktemp "${TMPDIR:-/tmp}/sodapop-coverage.XXXXXX")"
trap 'rm -f "$profile"' EXIT

"$go_command" test -covermode=atomic -coverprofile="$profile" ./...
coverage_output="$("$go_command" tool cover -func="$profile")"
current="$(awk '/^total:/ { value=$3 } END { sub(/%$/, "", value); print value }' <<<"$coverage_output")"

if [[ ! "$current" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  printf 'Could not parse total statement coverage from go tool cover output.\n' >&2
  exit 1
fi
current="$(awk -v value="$current" 'BEGIN { printf "%.1f", value + 0 }')"

if [[ ! -f "$targets_file" ]]; then
  printf 'Package coverage targets are missing: %s\n' "$targets_file" >&2
  exit 1
fi

while read -r package target extra; do
  if [[ -z "${package:-}" || "$package" == \#* ]]; then
    continue
  fi
  if [[ -n "${extra:-}" || ! "$target" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    printf 'Invalid package coverage target in %s: %s %s %s\n' "$targets_file" "$package" "${target:-}" "${extra:-}" >&2
    exit 1
  fi
  package_current="$(awk -v package="$package" '
    /^mode:/ { next }
    index($1, "/" package "/") {
      if (package == "internal/runtimebundle" && index($1, "/zcopilot") > 0) {
        next
      }
      statements += $2
      if ($3 > 0) {
        covered += $2
      }
    }
    END {
      if (statements == 0) {
        exit 2
      }
      printf "%.1f", covered * 100 / statements
    }
  ' "$profile")" || {
    printf 'Coverage profile contained no handwritten statements for %s.\n' "$package" >&2
    exit 1
  }
  target="$(awk -v value="$target" 'BEGIN { printf "%.1f", value + 0 }')"
  if ! awk -v current="$package_current" -v target="$target" 'BEGIN { exit !(current >= target) }'; then
    printf 'Package coverage below target: %s is %s%%, required %s%%.\n' "$package" "$package_current" "$target" >&2
    exit 1
  fi
  printf 'Package coverage passed: %s is %s%%, required %s%%.\n' "$package" "$package_current" "$target"
done <"$targets_file"

if [[ "$update_baseline" == true && ! -e "$baseline_file" ]]; then
  mkdir -p "$(dirname "$baseline_file")"
  printf '%s\n' "$current" >"$baseline_file"
  printf 'Created coverage baseline at %s%%.\n' "$current"
  exit 0
fi

if [[ ! -f "$baseline_file" ]]; then
  printf 'Coverage baseline is missing: %s\n' "$baseline_file" >&2
  printf 'Run make coverage-baseline to establish it from a passing full suite.\n' >&2
  exit 1
fi

baseline="$(tr -d '[:space:]' <"$baseline_file")"
if [[ ! "$baseline" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  printf 'Coverage baseline must contain one numeric percentage: %s\n' "$baseline_file" >&2
  exit 1
fi
baseline="$(awk -v value="$baseline" 'BEGIN { printf "%.1f", value + 0 }')"

if [[ "$update_baseline" == true ]]; then
  if ! awk -v current="$current" -v baseline="$baseline" 'BEGIN { exit !(current > baseline) }'; then
    printf 'Coverage baseline can only increase (current %s%%, baseline %s%%).\n' "$current" "$baseline" >&2
    exit 1
  fi
  printf '%s\n' "$current" >"$baseline_file"
  printf 'Raised coverage baseline from %s%% to %s%%.\n' "$baseline" "$current"
  exit 0
fi

if ! awk -v current="$current" -v baseline="$baseline" 'BEGIN { exit !(current >= baseline) }'; then
  printf 'Coverage decreased: current %s%%, required %s%%.\n' "$current" "$baseline" >&2
  printf 'Add or strengthen tests; do not lower the committed baseline.\n' >&2
  exit 1
fi

printf 'Coverage ratchet passed: current %s%%, required %s%%.\n' "$current" "$baseline"

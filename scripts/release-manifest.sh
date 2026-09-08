#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
helper=(go run ./scripts/releasectl)
if [[ -n "${SODAPOP_RELEASECTL:-}" ]]; then
  helper=("$SODAPOP_RELEASECTL")
fi
args=(manifest --dir "${SODAPOP_RELEASE_DIR:-dist}" --version "${SODAPOP_VERSION:-}" --commit "${SODAPOP_COMMIT:-}")
if [[ -n "${SODAPOP_PLATFORMS+x}" ]]; then
  args+=(--platforms "$SODAPOP_PLATFORMS")
fi
"${helper[@]}" "${args[@]}"

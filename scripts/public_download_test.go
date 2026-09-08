package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publicDownloadFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source, err := os.ReadFile("test-public-download.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "scripts/test-public-download.sh", string(source), 0600)
	writeFixtureFile(t, root, "fake-bin/go", `#!/usr/bin/env bash
case "$*" in
  "env GOHOSTOS") printf 'darwin\n' ;;
  "env GOHOSTARCH") printf 'arm64\n' ;;
  *) exit 8 ;;
esac
`, 0755)
	writeFixtureFile(t, root, "fake-bin/curl", `#!/usr/bin/env bash
set -euo pipefail
printf 'download %s\n' "${@: -1}" >> calls
if [[ "${SODAPOP_TEST_FAILURE:-}" == download ]]; then exit 22; fi
while (( $# )); do
  if [[ "$1" == --output ]]; then
    shift
    printf 'fixture' > "$1"
    exit 0
  fi
  shift
done
exit 2
`, 0755)
	writeFixtureFile(t, root, "bin/releasectl", `#!/usr/bin/env bash
set -euo pipefail
printf 'verify\n' >> calls
if [[ "${SODAPOP_TEST_FAILURE:-}" == verify ]]; then exit 1; fi
`, 0755)
	writeFixtureFile(t, root, "bin/installcheck", `#!/usr/bin/env bash
set -euo pipefail
printf 'installed-command\n' >> calls
if [[ "${SODAPOP_TEST_FAILURE:-}" == installed ]]; then exit 1; fi
`, 0755)
	return root
}

func TestPublicDownloadStopsBeforeExecutionOnErrors(t *testing.T) {
	for _, failure := range []string{"", "download", "verify", "installed"} {
		t.Run("failure-"+failure, func(t *testing.T) {
			root := publicDownloadFixture(t)
			output, err := runScriptFixture(t, root, []string{"bash", "scripts/test-public-download.sh", "1.2.3"},
				"SODAPOP_TARGET=darwin/arm64", "SODAPOP_TEST_FAILURE="+failure)
			if (err != nil) != (failure != "") {
				t.Fatalf("public download result = %v: %s", err, output)
			}
			calls, err := os.ReadFile(filepath.Join(root, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			if failure == "download" && strings.Contains(string(calls), "verify\n") {
				t.Fatal("verification ran despite failed download")
			}
			if (failure == "download" || failure == "verify") && strings.Contains(string(calls), "installed-command") {
				t.Fatal("unverified downloaded command was executed")
			}
			if failure == "" && !strings.HasSuffix(string(calls), "verify\ninstalled-command\n") {
				t.Fatalf("verification was not performed before execution: %s", calls)
			}
		})
	}
}

func TestPublicDownloadRejectsUntrustedInputsBeforeNetwork(t *testing.T) {
	for _, tc := range []struct {
		version string
		env     string
	}{
		{"../version", "SODAPOP_TARGET=darwin/arm64"},
		{"1.2.3", "SODAPOP_RELEASE_REPOSITORY=owner/repo/../../other"},
		{"1.2.3", "SODAPOP_TARGET=windows/arm64"},
		{"1.2.3", "SODAPOP_TARGET=linux/amd64"},
	} {
		t.Run(tc.version+tc.env, func(t *testing.T) {
			root := publicDownloadFixture(t)
			_, err := runScriptFixture(t, root, []string{"bash", "scripts/test-public-download.sh", tc.version},
				"SODAPOP_TARGET=darwin/arm64", tc.env)
			if err == nil {
				t.Fatal("invalid public-download inputs accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "calls")); !os.IsNotExist(err) {
				t.Fatalf("invalid inputs initiated network access: %v", err)
			}
		})
	}
}

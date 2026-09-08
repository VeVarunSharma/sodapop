ifneq ($(wildcard .sodapop.env),)
include .sodapop.env
export SODAPOP_GITHUB_CLIENT_ID
endif

export SODAPOP_LIVE_MODEL SODAPOP_DEMO SODAPOP_VERSION SODAPOP_RELEASE_DIR

.PHONY: help build start run bundle install test distribution-test npm-test release-tools native-install-test public-download-test coverage coverage-baseline runtime-smoke check qualify package homebrew-formula demos brand

help:
	@printf '%s\n' \
		'make build          Build ./bin/sodapop with the bundled Copilot runtime' \
		'make start          Build and start Sodapop' \
		'make run            Alias for make start' \
		'make install        Install Sodapop into the user bin directory' \
		'make test           Run the test suite' \
		'make distribution-test  Test release and package-manager contracts' \
		'make npm-test       Test npm packaging and launcher behavior' \
		'make release-tools  Build native archive verification tools' \
		'make native-install-test  Exercise a verified local release archive' \
		'make public-download-test  Exercise an unauthenticated published download' \
		'make coverage       Run tests and enforce the coverage ratchet' \
		'make coverage-baseline  Raise the coverage baseline after an improvement' \
		'make runtime-smoke  Exercise the bundled native runtime' \
		'make check          Run coverage, race tests, and go vet' \
		'make demos          Render the local-only README GIFs with VHS' \
		'make brand          Render the animated Sodapop title from existing artwork' \
		'make qualify        Opt in to live Copilot qualification in a disposable workspace' \
		'make package        Build release archives' \
		'make homebrew-formula  Generate an owned-tap formula from release checksums'

build:
	bash scripts/build.sh

start: build
	@output="$${SODAPOP_OUTPUT:-}"; \
	if [ -z "$$output" ]; then \
		if [ "$$(go env GOHOSTOS)" = windows ]; then output="bin/sodapop.exe"; else output="bin/sodapop"; fi; \
	fi; \
	case "$$output" in /*) ;; *) output="./$$output" ;; esac; \
	exec "$$output"

run: start

bundle:
	bash scripts/bundle.sh

install: build
	bash scripts/install.sh

test:
	go test ./...

distribution-test:
	go test ./scripts/...
	npm test --prefix npm
	node --test scripts/publish-npm.test.mjs

npm-test:
	npm test --prefix npm
	node --test scripts/publish-npm.test.mjs

release-tools:
	@set -e; mkdir -p bin; \
	hostos="$$(go env GOHOSTOS)"; hostarch="$$(go env GOHOSTARCH)"; suffix=""; \
	if [ "$$hostos" = windows ]; then suffix=".exe"; fi; \
	GOOS="$$hostos" GOARCH="$$hostarch" CGO_ENABLED=0 go build -o "bin/releasectl$$suffix" ./scripts/releasectl; \
	GOOS="$$hostos" GOARCH="$$hostarch" CGO_ENABLED=0 go build -o "bin/installcheck$$suffix" ./scripts/installcheck

native-install-test: release-tools
	@set -e; version="$${SODAPOP_VERSION:?Set SODAPOP_VERSION to the archive version}"; \
	directory="$${SODAPOP_RELEASE_DIR:-dist}"; suffix=""; \
	if [ "$$(go env GOHOSTOS)" = windows ]; then suffix=".exe"; fi; \
	"./bin/installcheck$$suffix" --release-dir "$$directory" \
		--manifest "$$directory/sodapop-$$version-manifest.json" \
		--tool "$$PWD/bin/releasectl$$suffix"

public-download-test: release-tools
	@bash scripts/test-public-download.sh "$${SODAPOP_VERSION:?Set SODAPOP_VERSION to a published version}"

coverage:
	bash scripts/coverage.sh

coverage-baseline:
	bash scripts/coverage.sh --update-baseline

runtime-smoke:
	SODAPOP_RUNTIME_SMOKE=1 go test ./internal/runtimebundle -run '^TestBundledRuntimeSmoke$$'

check: coverage
	go test -race ./...
	go vet ./...

qualify: bundle
	SODAPOP_LIVE_QUALIFY=1 go test -count=1 -timeout=10m -run '^TestLiveQualification$$' -v ./internal/integration

demos:
	bash scripts/record-demos.sh

brand:
	python3 -B -m unittest discover -s images/source -p 'test_animate.py'
	python3 -B images/source/animate.py

package:
	bash scripts/package.sh

homebrew-formula:
	@if [ -n "$(SODAPOP_HOMEBREW_FORMULA_OUTPUT)" ]; then \
		bash scripts/generate-homebrew-formula.sh "$(SODAPOP_VERSION)" "$(SODAPOP_HOMEBREW_FORMULA_OUTPUT)"; \
	else \
		bash scripts/generate-homebrew-formula.sh "$(SODAPOP_VERSION)"; \
	fi

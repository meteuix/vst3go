set shell := ["zsh", "-cu"]

default:
  @just --list

fmt:
  env XDG_CACHE_HOME=/tmp/treefmt-cache treefmt --allow-missing-formatter

fmt-check:
  env XDG_CACHE_HOME=/tmp/treefmt-cache treefmt --allow-missing-formatter --fail-on-change

check-formatted: fmt-check

lint:
  @if ! command -v golangci-lint >/dev/null 2>&1; then echo "golangci-lint not found. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; exit 1; fi
  env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache golangci-lint run ./pkg/...

lint-fix:
  @if ! command -v golangci-lint >/dev/null 2>&1; then echo "golangci-lint not found. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; exit 1; fi
  env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache golangci-lint run --fix ./pkg/...

test-go:
  env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache go test ./pkg/...

test: fmt-check test-go

# Build a macOS/Linux VST3 bundle from a main package, e.g. `just vst3 ./myplugin MyPlugin`.
vst3 package name out='dist':
  bash scripts/build_vst3.sh "{{package}}" "{{name}}" "{{out}}"

# Build the validation plugins and run the Steinberg validator on each.
# validator: path to the VST3 SDK `validator` binary.
validate-vst3 validator out='dist/validation':
  bash scripts/validate_vst3.sh "{{validator}}" "{{out}}"

# Build a macOS/Linux CLAP plugin from a main package, e.g. `just clap ./myplugin MyPlugin`.
clap package name out='dist':
  bash scripts/build_clap.sh "{{package}}" "{{name}}" "{{out}}"

# Build the validation plugins as CLAP plugins and run clap-validator and clapcheck on each.
# validator: path to the clap-validator binary.
validate-clap validator out='dist/validation-clap':
  bash scripts/validate_clap.sh "{{validator}}" "{{out}}"

# Go tests, VST3 and CLAP validation for every validation plugin on Linux in Docker.
# sdk: VST3 SDK checkout; platform: e.g. linux/amd64 (default: native).
docker-validate-linux sdk platform='':
  bash scripts/docker_validate_linux.sh "{{sdk}}" {{platform}}

# Build the gain plugin with the WebKitGTK editor and open it under Xvfb in Docker.
docker-check-linux-editor platform='':
  bash scripts/docker_check_linux_editor.sh {{platform}}

# Cross-compile every validation plugin for Windows with mingw-w64 in Docker.
docker-build-windows out='dist/windows-cross':
  bash scripts/docker_build_windows.sh "{{out}}"

windows-package plugin_dll bundle_root='dist/windows' plugin_name='vst3go':
  bash scripts/package_windows_vst3.sh "{{plugin_dll}}" "{{bundle_root}}" "{{plugin_name}}"

windows-build plugin_dll bundle_root='dist/windows' plugin_name='vst3go':
  just windows-package "{{plugin_dll}}" "{{bundle_root}}" "{{plugin_name}}"

windows-check-bundle bundle_root='dist/windows' plugin_name='vst3go':
  bash scripts/check_windows_vst3.sh "{{bundle_root}}" "{{plugin_name}}"

windows-build-dll bundle_root='dist/windows' plugin_name='vst3go':
  bash scripts/build_windows_vst3.sh "{{bundle_root}}" "{{plugin_name}}"

windows-preflight:
  bash scripts/preflight_windows_vst3.sh

windows-smoke:
  bash scripts/windows_toolchain_smoke.sh

windows-init-report report_path='':
  bash scripts/init_windows_validation_report.sh "{{report_path}}"

windows-validate report_path='' bundle_root='dist/windows' plugin_name='vst3go':
  bash scripts/windows_validation_run.sh "{{report_path}}" "{{bundle_root}}" "{{plugin_name}}"

windows-release bundle_root='dist/windows' plugin_name='vst3go':
  just windows-preflight
  VST3GO_WINDOWS_SKIP_PREFLIGHT=1 just windows-build-dll "{{bundle_root}}" "{{plugin_name}}"
  just windows-check-bundle "{{bundle_root}}" "{{plugin_name}}"

fix:
    just lint-fix
    just fmt

#!/usr/bin/env bash
# Checks the web editor SDK without a plugin host: runs the SDK in a fake
# page (internal/validation/hostcheck/sdkcheck.mjs, node), checks that the
# synth's typed bindings are current and type-checks them, the SDK
# declarations and typical editor code with tsc (internal/validation/synth/ui).
# The real web views run the SDK in scripts/check_macos_editor.sh and
# scripts/docker_check_linux_editor.sh.
#
# usage: scripts/check_editor_sdk.sh        (TSC=/path/to/tsc to skip npx)
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
cd "$repo_root"

node --check pkg/plugin/editor_assets/vst3go.js
node --check pkg/plugin/editor_assets/editor.js
node internal/validation/hostcheck/sdkcheck.mjs
go test -count=1 -run 'TestSynthEditor' ./internal/validation/synth
tsc="${TSC:-}"
if [[ -z "$tsc" ]]; then
  tsc="npx --yes -p typescript@5 tsc"
fi
$tsc -p internal/validation/synth/ui
echo "editor sdk ok"

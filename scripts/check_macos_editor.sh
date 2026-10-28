#!/usr/bin/env bash
# Builds the gain validation plugin as a VST3 and a CLAP and opens its editor
# in an NSWindow with internal/validation/hostcheck/editorcheck_darwin.m,
# which edits a slider through the page and checks the JS <-> Go bridge in
# both directions. Needs a logged-in window server session.
#
# usage: scripts/check_macos_editor.sh [output-dir]
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
out="${1:-${repo_root}/dist/editorcheck}"
mkdir -p "$out"

bash "${script_dir}/build_vst3.sh" internal/validation/gain Gain "$out" >/dev/null
bash "${script_dir}/build_clap.sh" internal/validation/gain Gain "$out" >/dev/null
clang -fobjc-arc -Wall -I"${repo_root}/include" "${repo_root}/internal/validation/hostcheck/editorcheck_darwin.m" \
  -o "${out}/editorcheck_darwin" -framework Cocoa -framework WebKit
"${out}/editorcheck_darwin" "${out}/Gain.vst3/Contents/MacOS/Gain"
"${out}/editorcheck_darwin" --clap "${out}/Gain.clap/Contents/MacOS/Gain"

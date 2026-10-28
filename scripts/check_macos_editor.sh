#!/usr/bin/env bash
# Builds the gain (built-in editor) and synth (custom editor page) validation
# plugins as VST3 and CLAP and opens their editors in an NSWindow with
# internal/validation/hostcheck/editorcheck_darwin.m, which drives the pages
# and the vst3go SDK and checks the JS <-> Go bridge in both directions,
# resizing and custom messages. Needs a logged-in window server session.
#
# usage: scripts/check_macos_editor.sh [output-dir]
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
out="${1:-${repo_root}/dist/editorcheck}"
mkdir -p "$out"

for name in Gain Synth; do
  package="internal/validation/$(echo "$name" | tr '[:upper:]' '[:lower:]')"
  bash "${script_dir}/build_vst3.sh" "$package" "$name" "$out" >/dev/null
  bash "${script_dir}/build_clap.sh" "$package" "$name" "$out" >/dev/null
done
clang -fobjc-arc -Wall -I"${repo_root}/include" "${repo_root}/internal/validation/hostcheck/editorcheck_darwin.m" \
  -o "${out}/editorcheck_darwin" -framework Cocoa -framework WebKit
"${out}/editorcheck_darwin" "${out}/Gain.vst3/Contents/MacOS/Gain"
"${out}/editorcheck_darwin" --clap "${out}/Gain.clap/Contents/MacOS/Gain"
"${out}/editorcheck_darwin" --custom "${out}/Synth.vst3/Contents/MacOS/Synth"
"${out}/editorcheck_darwin" --clap --custom "${out}/Synth.clap/Contents/MacOS/Synth"

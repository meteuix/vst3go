#!/usr/bin/env bash
# Builds every validation plugin as an Audio Unit, installs the bundles into
# ~/Library/Audio/Plug-Ins/Components (as vst3go-check-<name>.component,
# removed again on exit), and runs Apple's auval in strict mode twice over
# plus internal/validation/hostcheck/aucheck_darwin.m (parameter text,
# ClassInfo, MIDI in and out, factory presets, latency, side chain, and the
# Cocoa editor). macOS with a logged-in window server session.
#
# usage: scripts/validate_au.sh [output-dir]
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
out="${1:-${repo_root}/dist/au}"
mkdir -p "$out"
if [[ "$out" != /* ]]; then
  out="$(pwd)/${out}"
fi
components="${HOME}/Library/Audio/Plug-Ins/Components"
mkdir -p "$components"

installed=()
cleanup() {
  for bundle in ${installed[@]+"${installed[@]}"}; do
    rm -rf "$bundle"
  done
  killall -9 AudioComponentRegistrar >/dev/null 2>&1 || true
}
trap cleanup EXIT

clang -fobjc-arc -Wall "${repo_root}/internal/validation/hostcheck/aucheck_darwin.m" -o "${out}/aucheck" \
  -framework Cocoa -framework WebKit -framework AudioToolbox -framework CoreAudio -framework CoreMIDI

names=(gain synth midifx ducker stall)
kinds=(effect instrument midifx sidechain stall)
for name in "${names[@]}"; do
  bash "${script_dir}/build_au.sh" "internal/validation/${name}" "$name" "$out" >/dev/null
  rm -rf "${components}/vst3go-check-${name}.component"
  cp -R "${out}/${name}.component" "${components}/vst3go-check-${name}.component"
  installed+=("${components}/vst3go-check-${name}.component")
done
# The registrar caches the component list; restart it to pick up the copies.
killall -9 AudioComponentRegistrar >/dev/null 2>&1 || true
sleep 2

status=0
for i in "${!names[@]}"; do
  name="${names[$i]}"
  plist="${out}/${name}.component/Contents/Info.plist"
  codes=()
  for key in type subtype manufacturer; do
    codes+=("$(/usr/libexec/PlistBuddy -c "Print :AudioComponents:0:${key}" "$plist")")
  done
  log="${out}/${name}.auval.txt"
  if auval -strict -r 2 -v "${codes[@]}" > "$log" 2>&1 && grep -q "AU VALIDATION SUCCEEDED" "$log"; then
    warnings="$(grep -c WARNING "$log" || true)"
    if "${out}/aucheck" "${codes[@]}" "${kinds[$i]}" > "${out}/${name}.aucheck.txt" 2>&1; then
      echo "PASS ${name} (${codes[*]}): auval strict x2, ${warnings} warnings; aucheck ${kinds[$i]} ok"
    else
      echo "FAIL ${name}: aucheck ($(tail -1 "${out}/${name}.aucheck.txt"))"
      status=1
    fi
  else
    echo "FAIL ${name}: auval (see ${log})"
    status=1
  fi
done
exit "$status"

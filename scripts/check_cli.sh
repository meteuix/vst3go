#!/usr/bin/env bash
# Checks the vst3go command the way a plugin author uses it: vst3go new
# creates an effect, an instrument and a MIDI effect module against this
# checkout, their generated tests pass, and go tool vst3go build writes every
# format of the host (VST3, CLAP, Audio Unit on macOS, standalone app). Set
# VST3_VALIDATOR and CLAP_VALIDATOR to also run the Steinberg validator and
# clap-validator on the bundles; on macOS the Audio Units go through auval
# (installed into ~/Library/Audio/Plug-Ins/Components for the run).
#
# usage: scripts/check_cli.sh [output-dir]
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
out="${1:-${repo_root}/dist/cli}"
if [[ "$out" != /* ]]; then
  out="$(pwd)/${out}"
fi
rm -rf "$out"
mkdir -p "$out"
host_os="$(go env GOOS)"
components="${HOME}/Library/Audio/Plug-Ins/Components"

installed=()
cleanup() {
  for bundle in ${installed[@]+"${installed[@]}"}; do
    rm -rf "$bundle"
  done
  if [[ ${#installed[@]} -gt 0 ]]; then
    killall -9 AudioComponentRegistrar >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

formats=vst3,clap,app
if [[ "$host_os" == "darwin" ]]; then
  formats=vst3,clap,au,app
fi

for kind in effect instrument midi; do
  dir="${out}/${kind}"
  name="Check ${kind}"
  (cd "$repo_root" && go run ./cmd/vst3go new -type "$kind" -vendor "Check Vendor" -replace "$repo_root" "$dir" "$name") >/dev/null
  (cd "$dir" && go vet ./... && go test ./...) > "${out}/${kind}.test.txt" 2>&1 || {
    cat "${out}/${kind}.test.txt"
    echo "FAIL ${kind}: generated tests" >&2
    exit 1
  }
  (cd "$dir" && go tool vst3go build -format "$formats" . "$name") > "${out}/${kind}.build.txt" 2>&1 || {
    cat "${out}/${kind}.build.txt"
    echo "FAIL ${kind}: go tool vst3go build" >&2
    exit 1
  }
  dist="${dir}/dist"
  checks="tests, build ${formats}"

  if [[ "$host_os" == "darwin" ]]; then
    app="${dist}/${name}.app/Contents/MacOS/${name}"
  else
    app="${dist}/${name}"
  fi
  "$app" -headless -backend null -duration 300ms -midi-in none -virtual-midi=false -config "${out}/${kind}-config" > "${out}/${kind}.app.txt" 2>&1
  checks="${checks}, app ran"

  if [[ -n "${VST3_VALIDATOR:-}" ]]; then
    "$VST3_VALIDATOR" -e "${dist}/${name}.vst3" > "${out}/${kind}.vst3.txt" 2>&1 || {
      tail -20 "${out}/${kind}.vst3.txt"
      echo "FAIL ${kind}: VST3 validator" >&2
      exit 1
    }
    checks="${checks}, $(grep -Eo '[0-9]+ tests passed, [0-9]+ tests failed' "${out}/${kind}.vst3.txt" | tail -1)"
  fi
  if [[ -n "${CLAP_VALIDATOR:-}" ]]; then
    clap_path="${dist}/${name}.clap"
    HOME="${out}/clap-home" "$CLAP_VALIDATOR" validate "$clap_path" > "${out}/${kind}.clap.txt" 2>&1 || {
      tail -20 "${out}/${kind}.clap.txt"
      echo "FAIL ${kind}: clap-validator" >&2
      exit 1
    }
    checks="${checks}, clap-validator: $(grep 'tests run' "${out}/${kind}.clap.txt" | tail -1 | sed $'s/\033\\[[0-9;]*m//g')"
  fi
  if [[ "$host_os" == "darwin" ]]; then
    target="${components}/vst3go-cli-check-${kind}.component"
    mkdir -p "$components"
    rm -rf "$target"
    cp -R "${dist}/${name}.component" "$target"
    installed+=("$target")
    killall -9 AudioComponentRegistrar >/dev/null 2>&1 || true
    sleep 2
    plist="${dist}/${name}.component/Contents/Info.plist"
    codes=()
    for key in type subtype manufacturer; do
      codes+=("$(/usr/libexec/PlistBuddy -c "Print :AudioComponents:0:${key}" "$plist")")
    done
    auval -strict -v "${codes[@]}" > "${out}/${kind}.auval.txt" 2>&1 && grep -q "AU VALIDATION SUCCEEDED" "${out}/${kind}.auval.txt" || {
      tail -20 "${out}/${kind}.auval.txt"
      echo "FAIL ${kind}: auval" >&2
      exit 1
    }
    checks="${checks}, auval ${codes[*]}"
  fi
  echo "PASS ${kind}: ${checks}"
done

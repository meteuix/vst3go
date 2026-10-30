#!/usr/bin/env bash
# Builds examples/araclip as a VST3 and a CLAP plugin and checks its ARA
# runtime in both with internal/validation/hostcheck/aracheck.c, a minimal
# ARA 2 host (factory, document controller and host interfaces, note
# analysis, archive, binding, region rendering, teardown in both orders).
# On macOS it also builds the Audio Unit, installs it into
# ~/Library/Audio/Plug-Ins/Components (as vst3go-check-araclip.component,
# removed again on exit), runs auval in strict mode twice over and checks it
# through kAudioUnitProperty_ARAFactory and
# kAudioUnitProperty_ARAPlugInExtensionBindingWithRoles. On Apple
# silicon with Rosetta it also checks an x86_64 build through VST3 and CLAP:
# ARAInterface.h packs its structs to 1 byte on x86, unlike on ARM.
#
# usage: scripts/validate_ara.sh [output-dir]
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
output_root="${1:-${repo_root}/dist/validation-ara}"
mkdir -p "$output_root"
output_root="$(cd "$output_root" && pwd)"

aracheck="${output_root}/aracheck"
frameworks=()
[[ "$(go env GOOS)" == darwin ]] && frameworks=(-framework AudioToolbox -framework CoreFoundation)
cc -O1 -Wall -Wextra -I"${repo_root}/include" "${repo_root}/internal/validation/hostcheck/aracheck.c" -o "$aracheck" -ldl -lm "${frameworks[@]}"

case "$(go env GOOS)/$(go env GOARCH)" in
  darwin/*)
    vst3="${output_root}/araclip.vst3/Contents/MacOS/araclip"
    clap="${output_root}/araclip.clap/Contents/MacOS/araclip" ;;
  linux/amd64)
    vst3="${output_root}/araclip.vst3/Contents/x86_64-linux/araclip.so"
    clap="${output_root}/araclip.clap" ;;
  linux/arm64)
    vst3="${output_root}/araclip.vst3/Contents/aarch64-linux/araclip.so"
    clap="${output_root}/araclip.clap" ;;
  *) echo "unsupported host" >&2; exit 1 ;;
esac

bash "${script_dir}/build_vst3.sh" examples/araclip araclip "$output_root" >/dev/null
bash "${script_dir}/build_clap.sh" examples/araclip araclip "$output_root" >/dev/null

check() {
  local format="$1"
  shift
  local log="${output_root}/${format}.log"
  if "$aracheck" "$format" "$@" >"$log" 2>&1; then
    echo "PASS araclip ${format}: $(tail -1 "$log")"
  else
    echo "FAIL araclip ${format}: see ${log}"
    failed=1
  fi
}

failed=0
check vst3 "$vst3"
check clap "$clap"

if [[ "$(go env GOOS)" == darwin ]]; then
  bash "${script_dir}/build_au.sh" examples/araclip araclip "$output_root" >/dev/null
  installed="${HOME}/Library/Audio/Plug-Ins/Components/vst3go-check-araclip.component"
  cleanup() {
    rm -rf "$installed"
    killall -9 AudioComponentRegistrar >/dev/null 2>&1 || true
  }
  trap cleanup EXIT
  mkdir -p "$(dirname "$installed")"
  rm -rf "$installed"
  cp -R "${output_root}/araclip.component" "$installed"
  # The registrar caches the component list; restart it to pick up the copy.
  killall -9 AudioComponentRegistrar >/dev/null 2>&1 || true
  sleep 2
  codes=()
  for key in type subtype manufacturer; do
    codes+=("$(/usr/libexec/PlistBuddy -c "Print :AudioComponents:0:${key}" "${output_root}/araclip.component/Contents/Info.plist")")
  done
  if auval -strict -r 2 -v "${codes[@]}" >"${output_root}/auval.log" 2>&1 &&
    grep -q "AU VALIDATION SUCCEEDED" "${output_root}/auval.log"; then
    echo "PASS araclip auval: strict x2, $(grep -c WARNING "${output_root}/auval.log" || true) warnings"
  else
    echo "FAIL araclip auval: see ${output_root}/auval.log"
    failed=1
  fi
  check au "${codes[@]}"

  if [[ "$(go env GOARCH)" == arm64 ]] && arch -x86_64 /usr/bin/true 2>/dev/null; then
    x86="${output_root}/araclip-x86_64.dylib"
    GOARCH=amd64 CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o "$x86" "${repo_root}/examples/araclip"
    cc -arch x86_64 -O1 -Wall -Wextra -I"${repo_root}/include" "${repo_root}/internal/validation/hostcheck/aracheck.c" \
      -o "${aracheck}-x86_64" -ldl -lm "${frameworks[@]}"
    for format in vst3 clap; do
      log="${output_root}/${format}-x86_64.log"
      if arch -x86_64 "${aracheck}-x86_64" "$format" "$x86" >"$log" 2>&1; then
        echo "PASS araclip ${format} x86_64: $(tail -1 "$log")"
      else
        echo "FAIL araclip ${format} x86_64: see ${log}"
        failed=1
      fi
    done
  fi
fi
exit "$failed"

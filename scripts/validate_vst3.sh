#!/usr/bin/env bash
# Builds every plugin under internal/validation, runs the Steinberg VST3
# validator (extensive mode) on it, then checks its processing behavior with
# internal/validation/hostcheck. Finally it checks that .vstpreset files from
# pkg/preset load with the SDK's PresetFile and vice versa
# (hostcheck/presetcheck.cpp), using the SDK checkout the validator was built
# in (or VST3_SDK, with its CMake build in VST3_SDK_BUILD).
#
# usage: scripts/validate_vst3.sh <path-to-validator> [output-dir]
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <path-to-validator> [output-dir]" >&2
  exit 2
fi

validator="$1"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
output_root="${2:-${repo_root}/dist/validation}"
mkdir -p "$output_root"

hostcheck="${output_root}/hostcheck"
cc -O1 -Wall -I"${repo_root}/include" "${repo_root}/internal/validation/hostcheck/hostcheck.c" -o "$hostcheck" -ldl -lm

case "$(go env GOOS)/$(go env GOARCH)" in
  darwin/*) binary_path() { echo "${output_root}/$1.vst3/Contents/MacOS/$1"; } ;;
  linux/amd64) binary_path() { echo "${output_root}/$1.vst3/Contents/x86_64-linux/$1.so"; } ;;
  linux/arm64) binary_path() { echo "${output_root}/$1.vst3/Contents/aarch64-linux/$1.so"; } ;;
  *) echo "unsupported host" >&2; exit 1 ;;
esac

failed=0
for main in "${repo_root}"/internal/validation/*/main.go; do
  name="$(basename "$(dirname "$main")")"
  bash "${script_dir}/build_vst3.sh" "internal/validation/${name}" "$name" "$output_root" >/dev/null
  log="${output_root}/${name}.log"
  if ! "$validator" -e "${output_root}/${name}.vst3" >"$log" 2>&1; then
    echo "FAIL ${name}: validator, see ${log}"
    failed=1
    continue
  fi
  if ! "$hostcheck" "$name" "$(binary_path "$name")" >>"$log" 2>&1; then
    echo "FAIL ${name}: hostcheck, see ${log}"
    failed=1
    continue
  fi
  echo "PASS ${name}: $(grep 'Result:' "$log" | tail -1 | sed 's/^.*Result: //'), hostcheck ok"
done

# Preset files: pkg/preset <-> Steinberg PresetFile, on the gain plugin.
sdk="${VST3_SDK:-$(cd "$(dirname "$validator")/../../.." && pwd)}"
if [[ ! -f "${sdk}/public.sdk/source/vst/vstpresetfile.cpp" ]]; then
  echo "SKIP presets: no VST3 SDK sources at ${sdk} (set VST3_SDK)"
  exit "$failed"
fi
libs=(-L"${VST3_SDK_BUILD:-${sdk}/build}/lib/Release" -lbase -lpluginterfaces)
case "$(go env GOOS)" in
  darwin) libs+=(-framework CoreFoundation -framework Foundation) ;;
  *) libs+=(-ldl -lpthread) ;;
esac
presetcheck="${output_root}/presetcheck"
log="${output_root}/presets.log"
c++ -std=c++17 -O1 -w -I"$sdk" "${repo_root}/internal/validation/hostcheck/presetcheck.cpp" \
  "${sdk}/public.sdk/source/vst/vstpresetfile.cpp" "${sdk}/public.sdk/source/vst/vstinitiids.cpp" \
  "${libs[@]}" -o "$presetcheck"
gain="$(binary_path gain)"
if cid="$("$presetcheck" "$gain" state "${output_root}/preset-state.bin" 2>"$log")" &&
  go run "${repo_root}/internal/validation/hostcheck/presettool" write "${output_root}/go.vstpreset" "$cid" "${output_root}/preset-state.bin" >>"$log" 2>&1 &&
  "$presetcheck" "$gain" load "${output_root}/go.vstpreset" "${output_root}/sdk.vstpreset" >>"$log" 2>&1 &&
  go run "${repo_root}/internal/validation/hostcheck/presettool" read "${output_root}/sdk.vstpreset" "$cid" >>"$log" 2>&1; then
  echo "PASS presets: pkg/preset files load with the SDK PresetFile and vice versa"
else
  echo "FAIL presets, see ${log}"
  failed=1
fi
exit "$failed"

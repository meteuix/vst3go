#!/usr/bin/env bash
# Builds every plugin under internal/validation, runs the Steinberg VST3
# validator (extensive mode) on it, then checks its processing behavior with
# internal/validation/hostcheck.
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
exit "$failed"

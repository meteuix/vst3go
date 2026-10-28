#!/usr/bin/env bash
# Builds every plugin under internal/validation as a CLAP plugin, checks its
# processing behavior and preset discovery with
# internal/validation/hostcheck/clapcheck.c, then runs clap-validator
# (https://github.com/free-audio/clap-validator) on it. Both run with HOME
# set to a scratch folder: clapcheck writes a preset into the plugin's user
# preset folder there, which clap-validator then crawls and loads.
#
# usage: scripts/validate_clap.sh <path-to-clap-validator> [output-dir]
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <path-to-clap-validator> [output-dir]" >&2
  exit 2
fi

validator="$1"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
output_root="${2:-${repo_root}/dist/validation-clap}"
mkdir -p "$output_root"
output_root="$(cd "$output_root" && pwd)"

clapcheck="${output_root}/clapcheck"
cc -O1 -Wall -I"${repo_root}/include" "${repo_root}/internal/validation/hostcheck/clapcheck.c" -o "$clapcheck" -ldl -lm

case "$(go env GOOS)" in
  darwin) binary_path() { echo "${output_root}/$1.clap/Contents/MacOS/$1"; } ;;
  linux) binary_path() { echo "${output_root}/$1.clap"; } ;;
  *) echo "unsupported host" >&2; exit 1 ;;
esac

scratch_home="${output_root}/clapcheck-home"
rm -rf "$scratch_home"
mkdir -p "$scratch_home"

failed=0
for main in "${repo_root}"/internal/validation/*/main.go; do
  name="$(basename "$(dirname "$main")")"
  bash "${script_dir}/build_clap.sh" "internal/validation/${name}" "$name" "$output_root" >/dev/null
  log="${output_root}/${name}.log"
  if ! HOME="$scratch_home" "$clapcheck" "$name" "$(binary_path "$name")" >"$log" 2>&1; then
    echo "FAIL ${name}: clapcheck, see ${log}"
    failed=1
    continue
  fi
  if ! HOME="$scratch_home" "$validator" validate "${output_root}/${name}.clap" >>"$log" 2>&1; then
    echo "FAIL ${name}: clap-validator, see ${log}"
    failed=1
    continue
  fi
  echo "PASS ${name}: $(grep 'tests run' "$log" | tail -1 | sed 's/^ *//'), clapcheck ok"
done
exit "$failed"

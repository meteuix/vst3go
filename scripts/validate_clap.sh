#!/usr/bin/env bash
# Builds every plugin under internal/validation as a CLAP plugin, runs
# clap-validator (https://github.com/free-audio/clap-validator) on it, then
# checks its processing behavior with internal/validation/hostcheck/clapcheck.c.
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

failed=0
for main in "${repo_root}"/internal/validation/*/main.go; do
  name="$(basename "$(dirname "$main")")"
  bash "${script_dir}/build_clap.sh" "internal/validation/${name}" "$name" "$output_root" >/dev/null
  log="${output_root}/${name}.log"
  if ! "$validator" validate "${output_root}/${name}.clap" >"$log" 2>&1; then
    echo "FAIL ${name}: clap-validator, see ${log}"
    failed=1
    continue
  fi
  if ! "$clapcheck" "$name" "$(binary_path "$name")" >>"$log" 2>&1; then
    echo "FAIL ${name}: clapcheck, see ${log}"
    failed=1
    continue
  fi
  echo "PASS ${name}: $(grep 'tests run' "$log" | tail -1 | sed 's/^ *//'), clapcheck ok"
done
exit "$failed"

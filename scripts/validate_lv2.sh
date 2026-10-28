#!/usr/bin/env bash
# Builds every validation plugin (internal/validation/*) as an LV2 bundle on
# Linux and checks it: lv2_validate (the LV2 schemas, from lv2-dev) on the
# generated Turtle, lv2lint when it is installed, and
# internal/validation/hostcheck/lv2check.c, which loads the bundle with lilv
# and runs audio, MIDI, state and preset checks.
#
# usage: scripts/validate_lv2.sh [output-dir]
# Needs lv2-dev (lv2_validate, sord_validate) and liblilv-dev.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
output_root="${1:-${repo_root}/dist/validation-lv2}"
mkdir -p "$output_root"
output_root="$(cd "$output_root" && pwd)"

if [[ "$(go env GOOS)" != "linux" ]]; then
  echo "LV2 plugins build on Linux only" >&2
  exit 1
fi

lv2check="${output_root}/lv2check"
# shellcheck disable=SC2046
cc -O1 -Wall -I"${repo_root}/include" "${repo_root}/internal/validation/hostcheck/lv2check.c" -o "$lv2check" \
  $(pkg-config --cflags --libs lilv-0) -lm

cd "$repo_root"
failed=0
for main in "${repo_root}"/internal/validation/*/main.go; do
  name="$(basename "$(dirname "$main")")"
  log="${output_root}/${name}.log"
  bundle="${output_root}/${name}.lv2"
  if ! go run ./cmd/vst3go build -format lv2 -o "$output_root" "./internal/validation/${name}" "$name" >"$log" 2>&1; then
    echo "FAIL ${name}: build, see ${log}"
    failed=1
    continue
  fi
  if ! lv2_validate "$bundle"/*.ttl >>"$log" 2>&1 || grep -q "^error" "$log"; then
    echo "FAIL ${name}: lv2_validate, see ${log}"
    failed=1
    continue
  fi
  checks="lv2_validate ok"
  if command -v lv2lint >/dev/null; then
    uri="$(sed -n 's/^<\(urn:vst3go:[^>]*\)>$/\1/p' "${bundle}/manifest.ttl" | grep -v ':ui$' | grep -v ':preset:' | head -1)"
    if ! lv2lint -I "${bundle}/" "$uri" >>"$log" 2>&1; then
      echo "FAIL ${name}: lv2lint, see ${log}"
      failed=1
      continue
    fi
    checks="${checks}, lv2lint ok"
  fi
  if ! LV2_PATH="$output_root" "$lv2check" "$name" "$bundle" >>"$log" 2>&1; then
    echo "FAIL ${name}: lv2check, see ${log}"
    failed=1
    continue
  fi
  echo "PASS ${name}: ${checks}, lv2check ok"
done
exit "$failed"

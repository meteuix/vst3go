#!/usr/bin/env bash
# Builds a VST3 bundle (macOS Contents/MacOS, Linux Contents/<arch>-linux,
# with Contents/Resources/moduleinfo.json) from a Go main package that
# registers plugins.
# A wrapper around `go run ./cmd/vst3go build -format vst3` for packages in
# this repository; plugin modules run `go tool vst3go build` themselves.
#
# usage: scripts/build_vst3.sh <main-package-dir> <Name> [output-dir]
#   scripts/build_vst3.sh internal/validation/gain Gain dist
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: $0 <main-package-dir> <Name> [output-dir]" >&2
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
output_root="${3:-${repo_root}/dist}"
if [[ "$output_root" != /* ]]; then
  output_root="$(pwd)/${output_root}"
fi

cd "$repo_root"
go run ./cmd/vst3go build -format vst3 -o "$output_root" "./${1#./}" "$2"

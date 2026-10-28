#!/usr/bin/env bash
# Builds a CLAP plugin (macOS bundle, Linux .clap shared object) from a Go
# main package that registers plugins; the binary is the same c-shared
# library as the VST3 build.
# A wrapper around `go run ./cmd/vst3go build -format clap` for packages in
# this repository; plugin modules run `go tool vst3go build` themselves.
#
# usage: scripts/build_clap.sh <main-package-dir> <Name> [output-dir]
#   scripts/build_clap.sh internal/validation/gain Gain dist
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
go run ./cmd/vst3go build -format clap -o "$output_root" "./${1#./}" "$2"

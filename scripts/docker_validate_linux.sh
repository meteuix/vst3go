#!/usr/bin/env bash
# Runs the Go tests, scripts/validate_vst3.sh and scripts/validate_clap.sh on
# Linux inside a Docker container: builds the Steinberg validator from a VST3
# SDK checkout and clap-validator from source, builds every validation plugin
# as a VST3 bundle and a .clap file, and runs the validators and host checks.
#
# usage: scripts/docker_validate_linux.sh <vst3sdk-dir> [docker-platform]
#   scripts/docker_validate_linux.sh ~/vst3sdk               # native arch
#   scripts/docker_validate_linux.sh ~/vst3sdk linux/amd64   # emulated x86_64
#
# The validator builds are cached in a Docker volume per platform.
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <vst3sdk-dir> [docker-platform]" >&2
  exit 2
fi

sdk="$(cd "$1" && pwd)"
platform="${2:-}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
go_version="$(awk '/^go /{print $2}' "${repo_root}/go.mod")"
image="golang:${go_version%.*}-bookworm"
cache="vst3go-linux-${platform//\//-}"
cache="${cache%-}"

# clap-validator has no release binaries for every Linux architecture.
clap_validator_rev=b2f1d9b79b1d264a5747f46707d72b1aa40a02ef

platform_args=()
if [[ -n "$platform" ]]; then
  platform_args=(--platform "$platform")
fi

docker run --rm ${platform_args[@]+"${platform_args[@]}"} \
  -v "${repo_root}:/src:ro" \
  -v "${sdk}:/sdk:ro" \
  -v "${cache}:/cache" \
  -e GOFLAGS=-buildvcs=false \
  -e CLAP_VALIDATOR_REV="$clap_validator_rev" \
  "$image" bash -euo pipefail -c '
    if [[ ! -x /cache/validator ]]; then
      # The SDK builds the validator next to editorhost, which needs X11 and
      # gtkmm. The golang image already has the C compiler cgo needs.
      apt-get update -qq
      apt-get install -y -qq --no-install-recommends cmake g++ pkg-config libx11-dev libgtkmm-3.0-dev >/dev/null
      cmake -S /sdk -B /cache/sdk-build -DCMAKE_BUILD_TYPE=Release \
        -DSMTG_ENABLE_VSTGUI_SUPPORT=OFF \
        -DSMTG_ENABLE_VST3_PLUGIN_EXAMPLES=OFF \
        -DSMTG_ENABLE_VST3_HOSTING_EXAMPLES=ON \
        -DSMTG_RUN_VST_VALIDATOR=OFF >/dev/null
      cmake --build /cache/sdk-build --config Release --target validator -j"$(nproc)" >/dev/null
      cp /cache/sdk-build/bin/Release/validator /cache/validator
    fi
    if [[ ! -x /cache/clap-validator-${CLAP_VALIDATOR_REV}/bin/clap-validator ]]; then
      export RUSTUP_HOME=/cache/rustup CARGO_HOME=/cache/cargo
      if [[ ! -x /cache/cargo/bin/cargo ]]; then
        curl -fsSL https://sh.rustup.rs | sh -s -- -y -q --profile minimal --no-modify-path >/dev/null
      fi
      /cache/cargo/bin/cargo install -q --locked --git https://github.com/free-audio/clap-validator \
        --rev "$CLAP_VALIDATOR_REV" --root "/cache/clap-validator-${CLAP_VALIDATOR_REV}" clap-validator
    fi
    if ! command -v g++ >/dev/null; then
      # presetcheck links the SDK PresetFile (C++).
      apt-get update -qq
      apt-get install -y -qq --no-install-recommends g++ >/dev/null
    fi
    mkdir -p /work
    cp -a /src/. /work/
    cd /work
    go vet ./...
    go test -count=1 ./...
    rm -rf /cache/out /cache/out-clap
    VST3_SDK=/sdk VST3_SDK_BUILD=/cache/sdk-build scripts/validate_vst3.sh /cache/validator /cache/out
    scripts/validate_clap.sh "/cache/clap-validator-${CLAP_VALIDATOR_REV}/bin/clap-validator" /cache/out-clap
  '

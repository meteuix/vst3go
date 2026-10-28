#!/usr/bin/env bash
# Builds the gain validation plugin with the Linux WebKitGTK editor (build
# tag webkitgtk) in a Docker container and opens its editor under Xvfb with
# internal/validation/hostcheck/editorcheck.c, which also drives the page
# with XTest keyboard input to check the JS -> Go parameter bridge, once as
# a VST3 and once through the CLAP gui extension.
#
# usage: scripts/docker_check_linux_editor.sh [docker-platform]
set -euo pipefail

platform="${1:-}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
go_version="$(awk '/^go /{print $2}' "${repo_root}/go.mod")"
image="golang:${go_version%.*}-bookworm"

platform_args=()
if [[ -n "$platform" ]]; then
  platform_args=(--platform "$platform")
fi

docker run --rm ${platform_args[@]+"${platform_args[@]}"} \
  -v "${repo_root}:/src:ro" \
  -e GOFLAGS="-buildvcs=false -tags=webkitgtk" \
  "$image" bash -euo pipefail -c '
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends pkg-config libwebkit2gtk-4.1-dev libx11-dev libxtst-dev xvfb xauth >/dev/null
    mkdir -p /work
    cp -a /src/. /work/
    cd /work
    go vet ./pkg/plugin/
    scripts/build_vst3.sh internal/validation/gain Gain /tmp/out
    scripts/build_clap.sh internal/validation/gain Gain /tmp/out
    cc -O1 -Wall -Iinclude internal/validation/hostcheck/editorcheck.c -o /tmp/editorcheck -ldl -lX11 -lXtst
    binary="$(ls /tmp/out/Gain.vst3/Contents/*-linux/Gain.so)"
    # WebKit needs a writable runtime dir; it runs without a sandbox in containers.
    export XDG_RUNTIME_DIR=/tmp/xdg WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1
    mkdir -p "$XDG_RUNTIME_DIR" && chmod 700 "$XDG_RUNTIME_DIR"
    xvfb-run -a /tmp/editorcheck "$binary"
    xvfb-run -a /tmp/editorcheck --clap /tmp/out/Gain.clap
  '

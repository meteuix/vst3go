#!/usr/bin/env bash
# Builds the gain (built-in editor) and synth (custom editor page)
# validation plugins with the Linux WebKitGTK editor (build tag webkitgtk) in
# a Docker container and opens their editors under Xvfb with
# internal/validation/hostcheck/editorcheck.c, which also drives the page
# with XTest keyboard input to check the JS -> Go parameter bridge: as a
# VST3, through the CLAP gui extension, and as the X11 UI of the LV2 bundle
# (hostcheck/lv2check.c --ui, loaded with lilv); then opens a standalone
# application window with its toolbar and settings panels (pkg/standalone
# gui_test.go).
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
    apt-get install -y -qq --no-install-recommends pkg-config libwebkit2gtk-4.1-dev libx11-dev libxtst-dev xvfb xauth liblilv-dev libasound2-dev >/dev/null
    mkdir -p /work
    cp -a /src/. /work/
    cd /work
    go vet ./pkg/plugin/
    scripts/build_vst3.sh internal/validation/gain Gain /tmp/out
    scripts/build_clap.sh internal/validation/gain Gain /tmp/out
    scripts/build_vst3.sh internal/validation/synth Synth /tmp/out
    scripts/build_clap.sh internal/validation/synth Synth /tmp/out
    go run ./cmd/vst3go build -format lv2 -o /tmp/out internal/validation/gain Gain >/dev/null
    go run ./cmd/vst3go build -format lv2 -o /tmp/out internal/validation/synth Synth >/dev/null
    cc -O1 -Wall -Iinclude internal/validation/hostcheck/editorcheck.c -o /tmp/editorcheck -ldl -lX11 -lXtst
    cc -O1 -Wall -DLV2CHECK_UI -Iinclude internal/validation/hostcheck/lv2check.c -o /tmp/lv2check $(pkg-config --cflags --libs lilv-0) -ldl -lX11 -lXtst -lm
    binary="$(ls /tmp/out/Gain.vst3/Contents/*-linux/Gain.so)"
    # WebKit needs a writable runtime dir; it runs without a sandbox in containers.
    export XDG_RUNTIME_DIR=/tmp/xdg WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1
    mkdir -p "$XDG_RUNTIME_DIR" && chmod 700 "$XDG_RUNTIME_DIR"
    xvfb-run -a /tmp/editorcheck "$binary"
    xvfb-run -a /tmp/editorcheck --clap /tmp/out/Gain.clap
    synth="$(ls /tmp/out/Synth.vst3/Contents/*-linux/Synth.so)"
    xvfb-run -a /tmp/editorcheck --custom "$synth"
    xvfb-run -a /tmp/editorcheck --clap --custom /tmp/out/Synth.clap
    xvfb-run -a /tmp/lv2check --ui /tmp/out/Gain.lv2
    xvfb-run -a /tmp/lv2check --ui --custom /tmp/out/Synth.lv2
    VST3GO_STANDALONE_GUI_CHECK=1 xvfb-run -a go test -v -count=1 -tags standalone,webkitgtk ./pkg/standalone
  '

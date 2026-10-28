#!/usr/bin/env bash
# Checks the standalone applications on Linux in a Docker container: the
# pkg/standalone tests, every validation plugin as a headless application on
# the null audio device, and the synth with the WebKitGTK editor under Xvfb,
# whose window must follow the editor's startup vst3go.resize to 720x420 and
# which must quit cleanly on SIGTERM. Containers have no sound card or ALSA
# sequencer, so MIDI ports are not exercised here
# (internal/validation/hostcheck/standalonecheck covers MIDI on macOS);
# with SOUND=1 the container gets the Docker VM's /dev/snd (OrbStack has a
# virtio sound card) and the synth also plays through ALSA.
#
# usage: [SOUND=1] scripts/docker_check_linux_standalone.sh [docker-platform]
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
if [[ "${SOUND:-}" == 1 ]]; then
  platform_args+=(--device /dev/snd)
fi

docker run --rm ${platform_args[@]+"${platform_args[@]}"} \
  -v "${repo_root}:/src:ro" \
  -e GOFLAGS="-buildvcs=false" \
  "$image" bash -euo pipefail -c '
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends pkg-config libasound2-dev libwebkit2gtk-4.1-dev libx11-dev xvfb xauth x11-utils >/dev/null
    mkdir -p /work
    cp -a /src/. /work/
    cd /work
    go vet -tags standalone ./pkg/standalone/ ./internal/...
    go test -count=1 -tags standalone ./internal/midiport/
    go test -race -count=1 -tags standalone ./pkg/standalone/
    for dir in gain synth midifx ducker; do
      scripts/build_standalone.sh "internal/validation/$dir" "$dir" /tmp/headless >/dev/null
      /tmp/headless/$dir -headless -backend null -config "/tmp/cfg-$dir" -duration 500ms 2>&1 | tee "/tmp/$dir.log"
      grep -q "audio null" "/tmp/$dir.log"
      test -s "/tmp/cfg-$dir/standalone.state"
    done

    if [[ -e /dev/snd ]]; then
      /tmp/headless/synth -headless -backend alsa -config /tmp/cfg-alsa -duration 1s -midi-in none 2>&1 | tee /tmp/alsa.log
      grep -q "audio alsa" /tmp/alsa.log
    fi

    GOFLAGS="$GOFLAGS -tags=webkitgtk" go vet -tags standalone,webkitgtk ./pkg/standalone/
    GOFLAGS="$GOFLAGS -tags=webkitgtk" scripts/build_standalone.sh internal/validation/synth Synth /tmp/gui
    export XDG_RUNTIME_DIR=/tmp/xdg WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1
    mkdir -p "$XDG_RUNTIME_DIR" && chmod 700 "$XDG_RUNTIME_DIR"
    xvfb-run -a bash -euo pipefail -c "
      /tmp/gui/Synth -backend null -config /tmp/cfg-gui > /tmp/gui.log 2>&1 &
      app=\$!
      size=
      for i in \$(seq 1 100); do
        sleep 0.1
        size=\$(xwininfo -name \"VST3Go Synth\" 2>/dev/null | awk \"/Width:/{w=\\\$2} /Height:/{h=\\\$2} END{if (w) print w \\\"x\\\" h}\") || true
        [[ \"\$size\" == 720x420 ]] && break
      done
      kill -TERM \$app
      wait \$app
      cat /tmp/gui.log
      grep -q \"editor 640x400\" /tmp/gui.log
      [[ \"\$size\" == 720x420 ]] || { echo \"window size \$size, want 720x420\"; exit 1; }
      test -s /tmp/cfg-gui/standalone.state
      echo \"window \$size\"
    "
    echo "linux standalone ok"
  '

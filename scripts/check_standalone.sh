#!/usr/bin/env bash
# Checks the standalone applications on the host (macOS or Linux): the
# pkg/standalone tests, every validation plugin as an application on the
# null audio device (it must save its state on quit), the MIDI path through
# a running application (internal/validation/hostcheck/standalonecheck,
# virtual MIDI ports), and on macOS the synth's editor window, which must
# follow the page's startup vst3go.resize from 640x400 to 720x420.
# scripts/docker_check_linux_standalone.sh covers Linux in a container.
#
# usage: scripts/check_standalone.sh [output-dir]
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
out="${1:-${repo_root}/dist/standalone}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
cd "$repo_root"
host_os="$(go env GOOS)"

go test -race -count=1 -tags standalone ./pkg/standalone/ ./internal/midiport/ >"${out}/test.log" 2>&1 || { cat "${out}/test.log"; exit 1; }
echo "PASS pkg/standalone and internal/midiport tests"

binary() {
  if [[ "$host_os" == darwin ]]; then
    echo "${out}/$1.app/Contents/MacOS/$1"
  else
    echo "${out}/$1"
  fi
}

for dir in gain synth midifx ducker stall; do
  bash "${script_dir}/build_standalone.sh" "internal/validation/${dir}" "$dir" "$out" >/dev/null 2>"${out}/${dir}.build.log" ||
    { cat "${out}/${dir}.build.log"; exit 1; }
  rm -rf "${out}/cfg-${dir}"
  "$(binary "$dir")" -headless -backend null -config "${out}/cfg-${dir}" -duration 500ms >"${out}/${dir}.log" 2>&1
  grep -q "audio null" "${out}/${dir}.log"
  test -s "${out}/cfg-${dir}/standalone.state"
  echo "PASS ${dir}: $(head -1 "${out}/${dir}.log")"
done

go build -tags standalone -o "${out}/standalonecheck" ./internal/validation/hostcheck/standalonecheck
"${out}/standalonecheck" "$(binary midifx)"

if [[ "$host_os" == darwin ]]; then
  # The window server may report window bounds in a scaled space, so the
  # check reads the aspect ratio. The window holds the title bar and the
  # 36-pixel toolbar above the editor: the 640x400 editor opens in a window
  # of about 1.37:1, the 720x420 one makes it about 1.47:1.
  clang -fobjc-arc -Wall internal/validation/hostcheck/windowsize_darwin.m -o "${out}/windowsize_darwin" -framework Cocoa
  rm -rf "${out}/cfg-gui"
  "$(binary synth)" -backend null -config "${out}/cfg-gui" -midi-in none -virtual-midi=false >"${out}/gui.log" 2>&1 &
  app=$!
  resized() { [[ -n "$1" ]] && (( ${1%x*} * 100 >= ${1#*x} * 142 )); }
  size=
  for _ in $(seq 1 100); do
    sleep 0.1
    size="$("${out}/windowsize_darwin" "$app" | head -1)"
    if resized "$size"; then break; fi
  done
  kill -TERM "$app"
  wait "$app"
  grep -q "editor 640x400" "${out}/gui.log" || { cat "${out}/gui.log"; exit 1; }
  resized "$size" || { echo "FAIL window ${size} did not follow the editor's resize to 720x420"; exit 1; }
  test -s "${out}/cfg-gui/standalone.state"
  echo "PASS synth editor window resized by the page (window server bounds ${size})"
fi
echo "standalone ok"

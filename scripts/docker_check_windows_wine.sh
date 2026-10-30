#!/usr/bin/env bash
# Runs the Windows builds of the validation plugins under Wine in a Docker
# container (linux/amd64): internal/validation/hostcheck/hostcheck.c and
# clapcheck.c, cross-compiled with mingw-w64, load every plugin through its
# VST3 and CLAP entry points (the stall plugin checks the deadline guard),
# every standalone application runs on the null audio device, and
# aracheck.c checks the ARA example through VST3 and CLAP.
#
# usage: scripts/docker_check_windows_wine.sh [build-dir]
# build-dir holds the output of scripts/docker_build_windows.sh (default
# dist/windows-cross). Wine is not Windows: this catches loading, export,
# threading and timing faults, not host- or driver-specific ones.
#
# Debian bookworm's Wine 8 is used because newer Wine builds need 32-bit
# segment setup that Rosetta-backed amd64 containers (Docker on Apple
# silicon) cannot emulate. Wine 8 lacks bcryptprimitives.dll, whose
# ProcessPrng the Go runtime requires at start-up, so the container installs
# one built from advapi32's RtlGenRandom, the generator ProcessPrng wraps on
# Windows.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
build_root="${1:-${repo_root}/dist/windows-cross}"
if [[ ! -d "$build_root" ]]; then
  echo "no Windows build in ${build_root}; run scripts/docker_build_windows.sh first" >&2
  exit 1
fi
build_root="$(cd "$build_root" && pwd)"
go_version="$(awk '/^go /{print $2}' "${repo_root}/go.mod")"

run_checks='
set -euo pipefail
export PATH="/usr/lib/wine:${PATH}" WINEDEBUG=-all WINEPREFIX="${HOME}/wine" XDG_RUNTIME_DIR="${HOME}"
wine64 wineboot -i >/dev/null 2>&1
cp /tmp/bcryptprimitives.dll "${WINEPREFIX}/drive_c/windows/system32/"
status=0
for main in /src/internal/validation/*/main.go; do
  name="$(basename "$(dirname "$main")")"
  [[ "$name" == hostcheck ]] && continue
  # Emulated amd64 (Rosetta) runs Go code tens of times slower than
  # native and a block of cold code takes over a millisecond, past the
  # deadline of the checks'"'"' 64-sample blocks. Only the stall plugin, whose
  # check is about the deadline, keeps the guard; the others check
  # behaviour with it off.
  deadline=0
  [[ "$name" == stall ]] && deadline=
  fail=0
  vst3="Z:\\build\\${name}.vst3\\Contents\\x86_64-win\\${name}.vst3"
  if ! out="$(VST3GO_PROCESS_DEADLINE="$deadline" wine64 /tmp/hostcheck.exe "$name" "$vst3" 2>&1)"; then
    echo "FAIL ${name}: hostcheck"; echo "$out"; fail=1
  fi
  # clapcheck writes presets into %USERPROFILE% (C:\users\clapcheck).
  if ! out="$(VST3GO_PROCESS_DEADLINE="$deadline" wine64 /tmp/clapcheck.exe "$name" "Z:\\build\\${name}.clap" 2>&1)"; then
    echo "FAIL ${name}: clapcheck"; echo "$out"; fail=1
  fi
  late="$(grep -o "late block returned after [0-9.]* ms" <<<"$out" || true)"
  if ! out="$(wine64 "/build/${name}.exe" -backend null -headless -duration 300ms -config "C:\\${name}-app" -midi-in none -virtual-midi=false 2>&1)"; then
    echo "FAIL ${name}: standalone"; echo "$out"; fail=1
  fi
  if ((fail)); then status=1; else echo "PASS ${name}: hostcheck, clapcheck, standalone${late:+ (${late})}"; fi
done
for format in vst3 clap; do
  binary="Z:\\build\\araclip.clap"
  [[ "$format" == vst3 ]] && binary="Z:\\build\\araclip.vst3\\Contents\\x86_64-win\\araclip.vst3"
  if out="$(VST3GO_PROCESS_DEADLINE=0 wine64 /tmp/aracheck.exe "$format" "$binary" 2>&1)"; then
    echo "PASS araclip ${format}: $(tail -1 <<<"$out")"
  else
    echo "FAIL araclip ${format}: aracheck"; echo "$out"; status=1
  fi
done
exit "$status"
'

docker run --rm --platform linux/amd64 \
  -v "${repo_root}:/src:ro" \
  -v "${build_root}:/build:ro" \
  -e RUN_CHECKS="$run_checks" \
  "golang:${go_version%.*}-bookworm" bash -euo pipefail -c '
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends gcc-mingw-w64-x86-64 wine64 >/dev/null
    cc=x86_64-w64-mingw32-gcc
    for check in hostcheck clapcheck aracheck; do
      "$cc" -O1 -Wall -Wextra -I/src/include "/src/internal/validation/hostcheck/${check}.c" -o "/tmp/${check}.exe" -static -lpthread -lm
    done
    printf "%s\n" "#include <windows.h>" \
      "BOOLEAN WINAPI SystemFunction036(PVOID, ULONG);" \
      "__declspec(dllexport) BOOL WINAPI ProcessPrng(PBYTE data, SIZE_T size) {" \
      "    while (size > 0) {" \
      "        ULONG n = size > 0x40000000 ? 0x40000000 : (ULONG)size;" \
      "        if (!SystemFunction036(data, n)) return FALSE;" \
      "        data += n; size -= n;" \
      "    }" \
      "    return TRUE;" \
      "}" > /tmp/bcryptprimitives.c
    "$cc" -shared -O1 /tmp/bcryptprimitives.c -o /tmp/bcryptprimitives.dll -ladvapi32
    useradd -m clapcheck
    runuser -u clapcheck -- bash -c "$RUN_CHECKS"
  '

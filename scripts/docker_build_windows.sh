#!/usr/bin/env bash
# Cross-compiles every validation plugin (internal/validation) as a Windows
# x86_64 VST3 DLL inside a Docker container with mingw-w64 and the WebView2
# SDK headers, so Windows build breakage shows up on any host. It checks
# compilation and the exported entry points; it does not run the plugins.
#
# usage: scripts/docker_build_windows.sh [output-dir]
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
output_root="${1:-${repo_root}/dist/windows-cross}"
mkdir -p "$output_root"
output_root="$(cd "$output_root" && pwd)"
go_version="$(awk '/^go /{print $2}' "${repo_root}/go.mod")"
image="golang:${go_version%.*}-bookworm"
webview2_version="${WEBVIEW2_VERSION:-1.0.2903.40}"

docker run --rm \
  -v "${repo_root}:/src:ro" \
  -v "${output_root}:/out" \
  -v vst3go-windows-cross:/cache \
  -e GOFLAGS=-buildvcs=false \
  -e WEBVIEW2_VERSION="$webview2_version" \
  "$image" bash -euo pipefail -c '
    if ! command -v x86_64-w64-mingw32-gcc >/dev/null; then
      apt-get update -qq
      apt-get install -y -qq --no-install-recommends gcc-mingw-w64-x86-64 binutils-mingw-w64-x86-64 unzip >/dev/null
    fi
    sdk=/cache/webview2-${WEBVIEW2_VERSION}
    if [[ ! -f "$sdk/build/native/include/WebView2.h" ]]; then
      mkdir -p "$sdk"
      curl -fsSL -o /tmp/webview2.zip "https://www.nuget.org/api/v2/package/Microsoft.Web.WebView2/${WEBVIEW2_VERSION}"
      unzip -q -o /tmp/webview2.zip -d "$sdk"
    fi
    mkdir -p /work
    cp -a /src/. /work/
    cd /work
    export CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 CGO_ENABLED=1
    # WebView2.h includes the Windows SDK header "EventToken.h"; mingw-w64
    # ships it in lower case (or not at all in old releases).
    shim=/tmp/webview2-shim
    mkdir -p "$shim"
    if [[ -f /usr/share/mingw-w64/include/eventtoken.h ]]; then
      echo "#include <eventtoken.h>" > "$shim/EventToken.h"
    else
      printf "#pragma once\ntypedef struct EventRegistrationToken { __int64 value; } EventRegistrationToken;\n" > "$shim/EventToken.h"
    fi
    export CGO_CFLAGS="-I$sdk/build/native/include -I$shim"
    export CGO_LDFLAGS="-L$sdk/build/native/x64"
    go vet ./...
    status=0
    for main in internal/validation/*/main.go; do
      dir="$(dirname "$main")"
      name="$(basename "$dir")"
      [[ "$name" == hostcheck ]] && continue
      dll="/out/${name}.vst3/Contents/x86_64-win/${name}.vst3"
      mkdir -p "$(dirname "$dll")"
      if go build -buildmode=c-shared -trimpath -o "$dll" "./$dir"; then
        rm -f "${dll%.vst3}.h"
        exports="$(x86_64-w64-mingw32-objdump -p "$dll" | grep -E "\\b(GetPluginFactory|InitDll|ExitDll)\\b" | wc -l)"
        if [[ "$exports" -ge 3 ]]; then
          echo "PASS $name: built $(basename "$dll"), exports GetPluginFactory/InitDll/ExitDll"
        else
          echo "FAIL $name: missing VST3 entry points"
          status=1
        fi
      else
        echo "FAIL $name: build failed"
        status=1
      fi
    done
    exit "$status"
  '

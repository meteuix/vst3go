#!/usr/bin/env bash
# Builds a VST3 bundle for the host platform (macOS or Linux) from a Go main
# package that registers a plugin.
#
# usage: scripts/build_vst3.sh <main-package-dir> <PluginName> [output-dir]
#   scripts/build_vst3.sh examples/gain Gain dist
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: $0 <main-package-dir> <PluginName> [output-dir]" >&2
  exit 2
fi

package_dir="$1"
plugin_name="$2"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
output_root="${3:-${repo_root}/dist}"
if [[ "$output_root" != /* ]]; then
  output_root="$(pwd)/${output_root}"
fi
bundle="${output_root}/${plugin_name}.vst3"

host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"

rm -rf "$bundle"
case "$host_os" in
  darwin)
    binary="${bundle}/Contents/MacOS/${plugin_name}"
    mkdir -p "$(dirname "$binary")"
    bundle_id="$(printf '%s' "com.vst3go.${plugin_name}" | tr -c 'A-Za-z0-9.-' '-')"
    cat > "${bundle}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>English</string>
  <key>CFBundleExecutable</key><string>${plugin_name}</string>
  <key>CFBundleIdentifier</key><string>${bundle_id}</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>${plugin_name}</string>
  <key>CFBundlePackageType</key><string>BNDL</string>
  <key>CFBundleSignature</key><string>????</string>
  <key>CFBundleVersion</key><string>1.0</string>
</dict>
</plist>
PLIST
    printf 'BNDL????' > "${bundle}/Contents/PkgInfo"
    ;;
  linux)
    case "$host_arch" in
      amd64) linux_arch="x86_64-linux" ;;
      arm64) linux_arch="aarch64-linux" ;;
      *) echo "unsupported Linux architecture: $host_arch" >&2; exit 1 ;;
    esac
    binary="${bundle}/Contents/${linux_arch}/${plugin_name}.so"
    mkdir -p "$(dirname "$binary")"
    ;;
  *)
    echo "unsupported host OS: $host_os (use scripts/build_windows_vst3.sh for Windows)" >&2
    exit 1
    ;;
esac

(
  cd "$repo_root"
  CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -o "$binary" "./${package_dir#./}"
)
rm -f "${binary%.so}.h" "${binary}.h"

if [[ "$host_os" == "darwin" ]] && command -v codesign >/dev/null 2>&1; then
  codesign --force --sign - "$bundle" >/dev/null 2>&1
fi

echo "built ${bundle}"

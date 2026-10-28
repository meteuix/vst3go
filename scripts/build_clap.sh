#!/usr/bin/env bash
# Builds a CLAP plugin for the host platform (macOS bundle or Linux .clap
# shared object) from a Go main package that registers plugins. The binary
# is the same c-shared library as the VST3 build: it exports clap_entry
# next to GetPluginFactory.
#
# usage: scripts/build_clap.sh <main-package-dir> <PluginName> [output-dir]
#   scripts/build_clap.sh examples/gain Gain dist
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
bundle="${output_root}/${plugin_name}.clap"

host_os="$(go env GOOS)"

rm -rf "$bundle"
case "$host_os" in
  darwin)
    binary="${bundle}/Contents/MacOS/${plugin_name}"
    mkdir -p "$(dirname "$binary")"
    bundle_id="$(printf '%s' "com.vst3go.clap.${plugin_name}" | tr -c 'A-Za-z0-9.-' '-')"
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
    binary="$bundle"
    mkdir -p "$output_root"
    ;;
  *)
    echo "unsupported host OS: $host_os (scripts/docker_build_windows.sh cross-builds Windows .clap files)" >&2
    exit 1
    ;;
esac

(
  cd "$repo_root"
  CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -o "$binary" "./${package_dir#./}"
)
rm -f "${binary%.clap}.h" "${binary}.h"

if [[ "$host_os" == "darwin" ]] && command -v codesign >/dev/null 2>&1; then
  codesign --force --sign - "$bundle" >/dev/null 2>&1
fi

echo "built ${bundle}"

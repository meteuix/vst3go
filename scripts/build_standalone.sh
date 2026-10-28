#!/usr/bin/env bash
# Builds the standalone application of a plugin for the host platform (a
# macOS .app bundle or a Linux executable) from a Go main package that
# registers plugins and calls standalone.Main from main. Tags in GOFLAGS
# (e.g. -tags=webkitgtk for the Linux editor) are kept.
#
# usage: scripts/build_standalone.sh <main-package-dir> <AppName> [output-dir]
#   scripts/build_standalone.sh internal/validation/synth Synth dist
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: $0 <main-package-dir> <AppName> [output-dir]" >&2
  exit 2
fi

package_dir="$1"
app_name="$2"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
output_root="${3:-${repo_root}/dist}"
if [[ "$output_root" != /* ]]; then
  output_root="$(pwd)/${output_root}"
fi

tags=standalone
for flag in ${GOFLAGS:-}; do
  case "$flag" in
    -tags=*) tags="${tags},${flag#-tags=}" ;;
  esac
done

host_os="$(go env GOOS)"
case "$host_os" in
  darwin)
    bundle="${output_root}/${app_name}.app"
    rm -rf "$bundle"
    binary="${bundle}/Contents/MacOS/${app_name}"
    mkdir -p "$(dirname "$binary")"
    bundle_id="$(printf '%s' "com.vst3go.standalone.${app_name}" | tr -c 'A-Za-z0-9.-' '-')"
    cat > "${bundle}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>English</string>
  <key>CFBundleExecutable</key><string>${app_name}</string>
  <key>CFBundleIdentifier</key><string>${bundle_id}</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>${app_name}</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleSignature</key><string>????</string>
  <key>CFBundleVersion</key><string>1.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSMicrophoneUsageDescription</key><string>${app_name} processes the audio input.</string>
</dict>
</plist>
PLIST
    printf 'APPL????' > "${bundle}/Contents/PkgInfo"
    ;;
  linux)
    bundle="${output_root}/${app_name}"
    binary="$bundle"
    mkdir -p "$output_root"
    ;;
  *)
    echo "unsupported host OS: $host_os (scripts/docker_build_windows.sh cross-builds Windows applications)" >&2
    exit 1
    ;;
esac

(
  cd "$repo_root"
  CGO_ENABLED=1 go build -tags "$tags" -trimpath -o "$binary" "./${package_dir#./}"
)

if [[ "$host_os" == "darwin" ]] && command -v codesign >/dev/null 2>&1; then
  codesign --force --sign - "$bundle" >/dev/null 2>&1
fi

echo "built ${bundle}"

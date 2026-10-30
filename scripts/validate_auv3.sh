#!/usr/bin/env bash
# Builds every validation plugin as an AUv3 extension (vst3go build -format
# auv3), registers the extensions with pluginkit (unregistered again on
# exit), and runs Apple's auval in strict mode twice over plus
# internal/validation/hostcheck/aucheck_darwin.m through the extensions,
# which load out of process, and auv3check_darwin.m (the extension's view
# controller and editor). macOS with a logged-in window server session.
# With IOS_SIMULATOR set to a simulator's UDID, it also builds the plugins
# for the iOS simulator, installs them there (boot the simulator first)
# and runs auv3check in it.
#
# usage: [IOS_SIMULATOR=<udid>] scripts/validate_auv3.sh [output-dir]
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
out="${1:-${repo_root}/dist/auv3}"
mkdir -p "$out"
if [[ "$out" != /* ]]; then
  out="$(pwd)/${out}"
fi

lsregister=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
registered=()
cleanup() {
  for app in ${registered[@]+"${registered[@]}"}; do
    pluginkit -r "${app}/Contents/PlugIns/$(basename "$app" .app).appex" >/dev/null 2>&1 || true
    "$lsregister" -u "$app" >/dev/null 2>&1 || true
  done
  killall -9 AudioComponentRegistrar >/dev/null 2>&1 || true
}
trap cleanup EXIT

clang -fobjc-arc -Wall "${repo_root}/internal/validation/hostcheck/aucheck_darwin.m" -o "${out}/aucheck" \
  -framework Cocoa -framework WebKit -framework AudioToolbox -framework CoreAudio -framework CoreMIDI
clang -fobjc-arc -Wall "${repo_root}/internal/validation/hostcheck/auv3check_darwin.m" -o "${out}/auv3check" \
  -framework Cocoa -framework AVFAudio -framework AudioToolbox -framework CoreAudioKit

names=(gain synth midifx ducker stall)
kinds=(effect instrument midifx sidechain stall)
# Editor sizes: the synth's page, the generic editor's default for the rest.
sizes=(900x640 640x400 900x640 900x640 900x640)
# Their smallest sizes (0x0: unbounded), for the view configurations.
minimums=(0x0 480x320 0x0 0x0 0x0)
for name in "${names[@]}"; do
  (cd "$repo_root" && go run ./cmd/vst3go build -format auv3 -o "${out}/${name}" "./internal/validation/${name}" "$name" >/dev/null)
  app="${out}/${name}/${name}.app"
  # Launch Services must know the container before pluginkit registers
  # the extension in it.
  "$lsregister" -f -R "$app"
  pluginkit -a "${app}/Contents/PlugIns/${name}.appex"
  registered+=("$app")
done
# The registrar caches the component list; restart it to pick up the
# extensions.
killall -9 AudioComponentRegistrar >/dev/null 2>&1 || true
codes() {
  local plist="${out}/${1}/${1}.app/Contents/PlugIns/${1}.appex/Contents/Info.plist" key
  for key in type subtype manufacturer; do
    /usr/libexec/PlistBuddy -c "Print :NSExtension:NSExtensionAttributes:AudioComponents:0:${key}" "$plist"
  done | paste -sd ' ' -
}
for _ in $(seq 1 30); do
  listed="$(auval -a)"
  missing=0
  for name in "${names[@]}"; do
    grep -q "^$(codes "$name") " <<<"$listed" || missing=1
  done
  [[ "$missing" == 0 ]] && break
  sleep 1
done

status=0
for i in "${!names[@]}"; do
  name="${names[$i]}"
  read -r -a codes <<<"$(codes "$name")"
  log="${out}/${name}.auval.txt"
  if ! auval -strict -r 2 -v "${codes[@]}" > "$log" 2>&1 || ! grep -q "AU VALIDATION SUCCEEDED" "$log" || ! grep -q "version 3 implementation" "$log"; then
    echo "FAIL ${name}: auval (see ${log})"
    status=1
    continue
  fi
  warnings="$(grep -c WARNING "$log" || true)"
  if ! "${out}/aucheck" "${codes[@]}" "${kinds[$i]}" > "${out}/${name}.aucheck.txt" 2>&1; then
    echo "FAIL ${name}: aucheck ($(tail -1 "${out}/${name}.aucheck.txt"))"
    status=1
    continue
  fi
  if ! "${out}/auv3check" "${codes[@]}" "${sizes[$i]}" "${minimums[$i]}" > "${out}/${name}.auv3check.txt" 2>&1; then
    echo "FAIL ${name}: auv3check ($(tail -1 "${out}/${name}.auv3check.txt"))"
    status=1
    continue
  fi
  echo "PASS ${name} (${codes[*]}): auval strict x2, ${warnings} warnings; aucheck ${kinds[$i]} ok; auv3check ok"
done

if [[ -n "${IOS_SIMULATOR:-}" ]]; then
  sdk="$(xcrun --sdk iphonesimulator --show-sdk-path)"
  xcrun --sdk iphonesimulator clang -fobjc-arc -Wall -isysroot "$sdk" -target "$(uname -m)-apple-ios14.0-simulator" \
    "${repo_root}/internal/validation/hostcheck/auv3check_darwin.m" -o "${out}/auv3check-ios" \
    -framework UIKit -framework AVFAudio -framework AudioToolbox -framework CoreAudioKit
  codesign -s - -f "${out}/auv3check-ios" >/dev/null 2>&1
  for i in "${!names[@]}"; do
    name="${names[$i]}"
    (cd "$repo_root" && go run ./cmd/vst3go build -format auv3 -platform ios-simulator -o "${out}/ios/${name}" "./internal/validation/${name}" "$name" >/dev/null)
    app="${out}/ios/${name}/${name}.app"
    read -r -a codes <<<"$(codes "$name")"
    id="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${app}/Info.plist")"
    # Launching the container registers its extension.
    if ! xcrun simctl install "$IOS_SIMULATOR" "$app" || ! xcrun simctl launch "$IOS_SIMULATOR" "$id" >/dev/null; then
      echo "FAIL ${name}: iOS simulator install"
      status=1
      continue
    fi
    sleep 2
    log="${out}/${name}.ios.auv3check.txt"
    if ! xcrun simctl spawn "$IOS_SIMULATOR" "${out}/auv3check-ios" "${codes[@]}" "${sizes[$i]}" "${minimums[$i]}" > "$log" 2>&1; then
      echo "FAIL ${name}: iOS simulator auv3check ($(tail -1 "$log"))"
      status=1
    else
      echo "PASS ${name}: iOS simulator auv3check ok"
    fi
    xcrun simctl terminate "$IOS_SIMULATOR" "$id" >/dev/null 2>&1 || true
    xcrun simctl uninstall "$IOS_SIMULATOR" "$id"
  done
fi
exit "$status"

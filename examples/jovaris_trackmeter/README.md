# Jovaris Track Meter Trial 0.1.4

Stereo/mono VU and sample peak analyzer with horizontal/vertical web editors,
peak hold and momentary Reset. Audio passes through unchanged. This is a trial,
not a production release or certified VU implementation.

## Build on Apple Silicon macOS

From this directory: `bash scripts/build_macos.sh`.
Add `--install` only to install into the current user's AU/VST3 folders.
No toolchain or OS updates are performed. The framework is used via `replace ../..`.

## Validation

Run `go test -race ./...` and `go vet ./...` here.
Actual macOS AU/WebKit regression harness:

```sh
mkdir -p dist evidence
clang -fobjc-arc -Wno-deprecated-declarations scripts/au_meter_check.m \
  -o dist/au_meter_check -framework Cocoa -framework WebKit \
  -framework AudioToolbox -framework CoreAudio -framework CoreMIDI
dist/au_meter_check "$PWD/dist/JovarisTrackMeterTrial.component"
```

The harness runs a dedicated test host with generated input, not Logic automation.
It checks dynamic input, Hold/Clip, running/stopped Reset, resume, a dropped
parameter-push fallback, and native H/V resizing. Evidence is historical and
platform-specific; it is not a CI pass claim for every host.

The stopped Reset failure was reproduced in 0.1.3. 0.1.4 adds native acknowledgement
and an atomic pull snapshot route while leaving the audio-owned measurement core
on the audio thread. Idle audio is labelled instead of presenting a stale VU as live.
The original reported freeze with different audio in Logic is not conclusively
root-caused. See `00_최종검증보고서.ko.md` for exact validation boundaries.

Original mockup assets and test signals are included. No user DAW sessions,
installed binary backups, private screenshots, or Mac plug-in binaries are committed.

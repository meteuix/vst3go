# Development Workflow

This document describes how development work in `vst3go` should be approached so the repository stays focused, testable, and release-ready.

## Repository Intent

- keep `vst3go` limited to the VST3 binding and runtime layer
- keep product DSP, showcase plugins, and higher-level audio behavior in downstream repos such as `synthkit`
- keep platform glue thin and keep behavior in Go whenever possible

## Working Style

- prefer small, boundary-respecting changes over broad refactors
- fix root causes instead of layering workarounds on top
- keep platform-specific code isolated to the smallest possible surface
- document behavior changes when they affect downstream consumers, validation, or release scope

## Preferred Change Order

When implementing a non-trivial change, follow this order:

1. confirm the repo boundary and affected package surface
2. inspect existing runtime, bridge, and docs contracts
3. implement the smallest change that solves the issue
4. add or extend focused tests
5. update docs and release/validation artifacts if the behavior changed
6. run build and test validation before closing the work

## Package Guidelines

- `pkg/vst3`: keep SDK-facing bindings and helper types straightforward
- `pkg/plugin`: keep wrapper, editor model, and platform bridge integration centered on runtime contracts
- `pkg/framework/*`: keep reusable runtime primitives generic and safe for downstream plugin authors
- `pkg/midi`: keep event and transport helpers lightweight and runtime-focused
- `pkg/plugin/*.c`, `*.h`: keep the C layer minimal and mechanical

## Web Editor Guidelines

- treat the Go-side editor snapshot and parameter model as the source of truth
- keep HTML, CSS, and JS coupled to the same snapshot contract across platforms
- keep hidden and read-only parameter semantics aligned with the native model
- avoid inventing a separate product UI framework inside this repo

## Windows Development Guidelines

- treat Windows support as build harness plus platform host glue, not a separate runtime architecture
- keep WebView2 hosting concerns isolated from shared runtime packages
- validate script-level behavior on non-Windows hosts with `bash scripts/windows_toolchain_smoke.sh`
- validate real loading, editor behavior, and bundle install flow on a real Windows machine using `docs/windows-validation.md`

## Validation Baseline

Before closing a change that touches runtime code or docs expectations, run:

```bash
git diff --check
go build ./...
go test -timeout=30000s ./...
```

Add this when thread-safety assumptions are involved:

```bash
go test -race -p 1 -timeout=30000s ./...
```

Add this when Windows build flow or docs changed:

```bash
bash scripts/windows_toolchain_smoke.sh
bash scripts/docker_build_windows.sh      # mingw-w64 cross build + export check
```

Add this when the VST3 runtime (`pkg/plugin`, the C bridge) changed. The
validator comes from a VST3 SDK checkout built with CMake:

```bash
bash scripts/validate_vst3.sh <sdk>/build/bin/Release/validator  # validator -e + hostcheck + preset files
bash scripts/validate_clap.sh <clap-validator>/target/release/clap-validator  # clap-validator + clapcheck
bash scripts/validate_au.sh                                     # macOS: auval -strict + aucheck (Audio Units)
bash scripts/validate_lv2.sh                                    # Linux: lv2_validate, lv2lint, lv2check (LV2)
bash scripts/docker_validate_linux.sh <sdk> [linux/amd64]       # VST3, CLAP, LV2 and the vst3go command on Linux, plus go test
bash scripts/docker_check_linux_editor.sh                       # WebKitGTK editor under Xvfb (VST3, CLAP, LV2 UI)
bash scripts/check_macos_editor.sh                              # WKWebView editor in an NSWindow (VST3 + CLAP)
bash scripts/check_editor_sdk.sh                                # editor SDK under node, bindings and tsc
bash scripts/check_standalone.sh                                # standalone apps, MIDI round trip, editor window
bash scripts/docker_check_linux_standalone.sh                   # standalone apps on Linux (SOUND=1 adds ALSA)
```

Add this when `cmd/vst3go` (the `new` templates or `build`) changed:

```bash
VST3_VALIDATOR=<validator> CLAP_VALIDATOR=<clap-validator> bash scripts/check_cli.sh  # scaffolded modules: tests, every format, validators, auval
```

clap-validator is built from https://github.com/free-audio/clap-validator
with `cargo build --release` (the Docker script builds a pinned revision).

## Documentation Expectations

- update `README.md` when scope, release guidance, or consumer guidance changes
- update `docs/runtime-contracts.md` when lifecycle, thread-safety, or persistence expectations change
- use `docs/developing-with-vst3go.md` for the detailed downstream plugin-building guide and examples
- update `docs/web-editor-bridge.md` when editor shell behavior or asset integration changes
- update Windows docs when build scripts, packaging rules, or validation expectations change

## Release Expectations

- use `docs/release-checklist.md` before tagging
- use `docs/release-notes-template.md` when preparing a release announcement
- keep Windows evidence attached when Windows remains in release scope

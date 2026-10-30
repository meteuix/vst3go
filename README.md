English | [한국어](README.ko.md)

# VST3Go

[![ci](https://github.com/th-release/vst3go/actions/workflows/ci.yml/badge.svg)](https://github.com/th-release/vst3go/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/th-release/vst3go/v2.svg)](https://pkg.go.dev/github.com/th-release/vst3go/v2) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`vst3go` is the low-level Go interface and runtime layer for VST3.

This fork is maintained separately from the original `justyntemme/vst3go` project. It ships the plugin runtime together with a DSP library and showcase plugins built on it.

It contains:

- The official VST3 C API header (`include/vst3`), the CLAP headers (`include/clap`), the LV2 headers (`include/lv2`) and the C bridge glue in `pkg/plugin`
- Go bindings for VST3 types and process structures
- The plugin wrapper/runtime used to expose Go processors as VST3, CLAP, (on macOS) AUv2 and (on Linux) LV2 plugins (one binary serves every format), as AUv3 app extensions for macOS and iOS, and the same package as a standalone application with audio/MIDI devices, a transport, an audio settings window and its editor in a window (`pkg/standalone`)
- ARA 2 support through VST3, CLAP and Audio Units (`pkg/ara`): plugins that edit and render the host's clips from the whole recording, with note analysis, archiving in the host's song and region rendering
- A web editor SDK (`window.vst3go`, custom pages, typed TypeScript parameter bindings, the platform's open file dialog)
- Generic plugin authoring primitives such as buses, parameters, process context, state, and MIDI/event transport
- A DSP library (`pkg/dsp`): filters, dynamics (compressor, limiter, gate, expander, transient shaper), delay lines, modulation (LFOs, chorus, flanger, phaser, tremolo, ring modulation), reverbs (Schroeder, Freeverb, FDN), distortion (waveshaper, tube, tape, bitcrusher), oscillators (band-limited, wavetable, FM operators), envelopes, panning, mixing, interpolation, oversampling and sample-rate conversion, FFT and STFT processing, partitioned convolution, pitch shifting, time stretching and pitch detection, granular synthesis, sample playback with disk streaming, Neural Amp Modeler models (WaveNet, LSTM) and analysis (spectrum, peak/RMS/LUFS metering, phase correlation)
- Processor chains (`pkg/framework/dsp`), polyphonic voice allocation (`pkg/framework/voice`) and debug/profiling helpers (`pkg/framework/debug`)
- Showcase plugins (`examples/`): compressors, a multiband compressor, limiter, gate, transient shaper, drum bus, vocal strip, distortion, chorus, flanger, delay, filter, sidechain ducking, surround, an oversampled saturator, a convolution reverb, a pitch shifter, a tuner, a granulator, wavetable, FM and polyphonic synths, an SFZ sampler, a NAM amp, an ARA clip editor, a debug-logging example and more
- Host features for effects, instruments and MIDI plugins: sample-accurate automation, 32/64-bit processing, sidechains and surround with host-negotiated layouts, MIDI CC/learn/MIDI 2.0 mapping and MIDI 2.0 input, note expression and MPE, keyswitches, factory programs, `.vstpreset` presets, units, SysEx, parameter smoothing, edit gestures, distributed processor/controller, several plugins per binary, host-free plugin tests (`pkg/plugintest`), and audio file reading and writing (`pkg/audiofile`: WAV, AIFF, FLAC, Ogg Vorbis, MP3)

For what kinds of plugins you can build and how to use each host feature, see [`docs/plugin-capabilities.md`](docs/plugin-capabilities.md).
For the web-rendered plugin editor flow, see [`docs/web-editor-bridge.md`](docs/web-editor-bridge.md).
For Windows build and packaging notes, see [`docs/windows-build.md`](docs/windows-build.md).
For the real Windows validation handoff checklist, see [`docs/windows-validation.md`](docs/windows-validation.md).
For a condensed Windows quickstart, see [`docs/windows-validation-quickstart.md`](docs/windows-validation-quickstart.md).
For a ready-to-fill Windows validation run record, see [`docs/windows-validation-report-template.md`](docs/windows-validation-report-template.md).
For lifecycle, thread-safety, and persistence expectations, see [`docs/runtime-contracts.md`](docs/runtime-contracts.md).
For day-to-day implementation and validation conventions, see [`docs/development-workflow.md`](docs/development-workflow.md).
For a very detailed downstream plugin-building guide, see [`docs/developing-with-vst3go.md`](docs/developing-with-vst3go.md).
For commercial login/licensing integration guidance, see [`docs/commercial-authentication.md`](docs/commercial-authentication.md).
For pre-tag signoff, see [`docs/release-checklist.md`](docs/release-checklist.md).
For publishing notes, see [`docs/release-notes-template.md`](docs/release-notes-template.md).

## Quick start

The `vst3go` command creates a plugin module and builds it in every format
(Go 1.24 or newer, a C compiler; on macOS the Xcode command line tools):

```bash
go run github.com/th-release/vst3go/v2/cmd/vst3go@latest new -type effect ./mygain "My Gain"
cd mygain
go test ./...                                # offline tests with pkg/plugintest
go tool vst3go build -install . "My Gain"     # VST3, CLAP, AU (macOS) or LV2 (Linux), into the user plugin folders
go tool vst3go build -format app . "My Gain"  # standalone application
```

`-type instrument` starts from a polyphonic synth and `-type midi` from a
MIDI transposer. The new module lists `vst3go` as a `tool` dependency, so
`go tool vst3go` always matches the runtime version it builds against.

To add the runtime to an existing module instead:

```bash
go get github.com/th-release/vst3go/v2@latest
```

Requirements per platform:

- macOS: Xcode command line tools (`xcode-select --install`); AUv3 for iOS needs full Xcode
- Linux: a C compiler, `pkg-config` and `libx11-dev`; the plugin editor also needs `libgtk-3-dev` and `libwebkit2gtk-4.1-dev`
- Windows: the cross build in [`docs/windows-build.md`](docs/windows-build.md)

## Scope

`vst3go` contains:

- `include/vst3/`
- `include/clap/`
- `pkg/vst3/`
- `pkg/plugin/`
- `pkg/midi/`
- `pkg/preset/`
- `pkg/plugintest/`
- `pkg/audiofile/`
- `pkg/standalone/` (and `internal/midiport`)
- `pkg/framework/bus`
- `pkg/framework/param`
- `pkg/framework/plugin`
- `pkg/framework/process`
- `pkg/framework/state`
- `pkg/dsp/` (the DSP library)
- `pkg/framework/dsp` (processor chains and DSP adapters)
- `pkg/framework/voice` (polyphonic voice allocation)
- `pkg/framework/debug` (logging, profiling and audio analysis helpers)
- `cmd/vst3go` (the `new`/`build` command)
- `examples/` (showcase plugins)
- `internal/validation/` (runtime validation plugins and host checks)

## Public API Boundary

- Downstream code should treat `pkg/plugin`, `pkg/vst3`, `pkg/midi`, `pkg/preset`, `pkg/plugintest`, `pkg/audiofile`, `pkg/standalone`, `pkg/dsp/...`, and the `pkg/framework/*` packages as the supported API surface.
- The C bridge (`pkg/plugin/*.c`) and the editor assets exist to support that API surface and are not called directly.
- `examples/` shows how the DSP library and the runtime fit together; copy from it rather than importing it.

## Consumption

- Downstream repos should import `github.com/th-release/vst3go/v2` and build on the supported package surface rather than reaching into bridge internals.
- Pin a release tag (`v2.x.y`) instead of a moving branch so bridge/runtime changes can be upgraded intentionally. The module path carries the major version, so the import path is `github.com/th-release/vst3go/v2/...`.
- DAW validation, signing, installers, and product-specific behavior remain downstream responsibilities.

## Development

Core checks in this repo:

```bash
just fmt
just fmt-check
just test
just vst3 ./path/to/plugin MyPlugin          # build a macOS/Linux .vst3 bundle
just validate-vst3 /path/to/vst3sdk/validator # validator + hostcheck on internal/validation
just clap ./path/to/plugin MyPlugin          # build a macOS/Linux .clap plugin
just validate-clap /path/to/clap-validator   # clap-validator + clapcheck on internal/validation
just au ./path/to/plugin MyPlugin            # build a macOS Audio Unit (.component)
just check-cli                                # vst3go new/build: scaffolded modules, tests, every format
just validate-au                              # auval (strict) + aucheck on internal/validation
just lv2 ./path/to/plugin MyPlugin           # build a Linux LV2 bundle (.lv2)
just validate-lv2                             # lv2_validate + lv2lint + lv2check (lilv) on internal/validation
just docker-validate-linux /path/to/vst3sdk   # VST3, CLAP, LV2 and vst3go command checks on Linux in Docker
just docker-check-linux-editor                # Linux WebKitGTK editor under Xvfb
just check-macos-editor                       # macOS editor in a real window, VST3 and CLAP
just check-editor-sdk                         # web editor SDK under node and tsc
just standalone ./path/to/plugin MyPlugin    # standalone application (.app / executable)
just check-standalone                         # standalone apps: null device, MIDI round trip, editor window
just docker-check-linux-standalone            # standalone apps on Linux in Docker
just docker-build-windows                     # mingw-w64 Windows cross build
just docker-check-windows-wine                # the Windows build under Wine: hostcheck, clapcheck, apps
```

## Validation

- This repo validates the shared runtime layer with `just test`, the VST3 contract with `just validate-vst3` (Steinberg validator in extensive mode plus `internal/validation/hostcheck` for sample-accurate processing), the CLAP contract with `just validate-clap` (clap-validator plus `hostcheck/clapcheck.c`), the Audio Unit contract with `just validate-au` (Apple's auval in strict mode plus `hostcheck/aucheck_darwin.m`), the LV2 contract with `just validate-lv2` (lv2_validate, lv2lint and `hostcheck/lv2check.c` on lilv), and the Windows build path with `just windows-smoke` when the host shell is available.
- The DSP library and the showcase plugins are covered by `just test` (`pkg/dsp/...`, `pkg/framework/...` and offline plugin tests in `examples/...`).
- Host app behavior and product-specific plugin wiring belong in the consumer project.
- Windows packaging and bundle layout checks in this repo stay focused on the generated DLL, header sidecar, and layout contract.
- Real Windows signoff should follow `docs/windows-validation.md` so toolchain, bundle, host, and WebView2 behavior are captured consistently.

## Release Scope

- This repo ships the VST3 binding/runtime layer, the DSP library, the showcase plugins, the web-rendered editor shell, and the Windows bundle/build harness that supports that shell.
- Release validation for this repo is centered on `just test`, `just windows-smoke`, and the documented bundle/layout checks.
- Final pre-tag review should follow `docs/release-checklist.md`.

Windows-specific editor and packaging notes:

- `pkg/plugin/windows_dll_windows.c` provides the DLL entry point.
- `pkg/plugin/vst3_bridge.c` exports `GetPluginFactory`, `InitDll` and `ExitDll`.
- `pkg/plugin/editor_view_windows.c` expects WebView2 headers and loader support.
- Windows support currently targets `amd64` and the `x86_64-win` bundle layout.
- `just windows-preflight` auto-selects a compatible Windows compiler when needed and checks for `windows.h`, `WebView2.h`, and `WebView2Loader`. If no compiler works, it reports the candidates it tried and why each one failed.
- `just windows-smoke` runs a local script-only smoke test that exercises selection, preflight, build, packaging, and bundle validation without a real Windows host.
- `just windows-init-report` creates a prefilled Windows validation report scaffold with current host metadata.
- `just windows-validate` runs the end-to-end validation sequence on a real Windows machine and writes a report file.
- `just windows-build-dll` builds the Windows shared library when the Windows toolchain is available.
- `just windows-build <dll>` assembles a VST3 bundle directory from a built Windows DLL and requires the generated header sidecar.
- `just windows-check-bundle` validates the resulting bundle layout.
- `just windows-release` runs preflight once, then build and validation in one go.
- `docs/windows-build.md` describes the expected bundle shape.

## License

This project is licensed under the MIT License (see [`LICENSE`](LICENSE)). Bundled third-party code keeps its own license:

- `include/vst3`: VST3 C API, BSD-style license ([`include/vst3/LICENSE.txt`](include/vst3/LICENSE.txt))
- `include/clap`: CLAP, MIT ([`include/clap/LICENSE`](include/clap/LICENSE))
- `include/lv2`: LV2, ISC ([`include/lv2/LICENSE`](include/lv2/LICENSE))
- `include/ara`: ARA API, Apache 2.0 ([`include/ara/LICENSE.txt`](include/ara/LICENSE.txt))
- `pkg/standalone/miniaudio.h`: miniaudio, public domain or MIT-0 (at the end of the file)

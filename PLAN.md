# VST3Go TODO

## Repo Boundary

- [x] Keep `vst3go` scoped as the VST3 binding/runtime layer.
- [x] Move DSP packages, showcase plugins, and synth-product planning out to `synthkit`.
- [x] Rename the module to `github.com/th-release/vst3go`.
- [x] Update the README to state that this fork intentionally differs from the original project.
- [x] Review the retained package boundary once more.
  - [x] `pkg/vst3`
  - [x] `pkg/plugin`
  - [x] `pkg/midi`
  - [x] `pkg/framework/{bus,param,plugin,process,state}`
  - [x] Confirm no higher-level audio/product concerns have drifted back in.

## Rules

- [x] Keep the C bridge minimal and direct.
- [x] Keep business logic in Go rather than in C.
- [x] Keep DSP and product-specific logic out of this repo.
- [x] Keep example/showcase plugin ownership out of this repo.
- [x] Keep these rules reflected in docs and future planning updates.

## Current Baseline

- [x] Plugin implementations only import `pkg/plugin`; the C bridge is compiled as part of that package.
- [x] Core runtime packages exist for buses, parameters, process context, state, MIDI/events, plugin runtime, and VST3 bindings.
- [x] Sample-accurate parameter automation is implemented.
- [x] MIDI/event handling is implemented.
- [x] Advanced bus support is implemented.
- [x] Parameter IDs are standardized around typed `uint32` constants with `ParamXxx` naming.
- [x] Most runtime reset paths are in place on deactivation.
- [x] `go test ./pkg/...` passes after the split and module rename.
- [x] Clean up the remaining bridge warning.
  - [x] Address the `const` qualifier mismatch around `GoCreateInstance` in `bridge.c` / `bridge.h`.

## Phase 1: Runtime API Cleanup

- [x] Resolve naming inconsistencies.
  - [x] Eliminate duplicate constant spellings and casing variants by making `ResultOK` canonical and keeping aliases compatibility-only.
  - [x] Standardize normalized/plain parameter naming semantics by adding explicit `GetNormalized`/`SetNormalized` and `GetPlain`/`SetPlain` accessors.
  - [x] Standardize canonical bus configuration entry points by adding primary constructors such as `Stereo`, `Mono`, `EffectStereo`, `Generator`, and `MIDIEffect`.
- [x] Clarify interface hierarchy.
  - [x] Document the relationships between plugin, processor, and optional stateful processor behavior in code comments.
  - [x] Keep optional interfaces explicit rather than implicit.
- [x] Tighten parameter and registry APIs.
  - [x] Audit and clean up the obvious unsafe parameter access patterns in retained runtime code.
  - [x] Prefer checked access where nil is possible by adding safe registry helpers such as `GetOK`, `Has`, `GetNormalized`, and `GetPlain`.
  - [x] Clarify duplicate-registration behavior and failure modes by making `Registry.Add` fail explicitly on duplicate IDs.
- [x] Tighten error handling.
  - [x] Replace silent failures where normal error returns are appropriate in the state/registry layer.
  - [x] Wrap errors with useful context in state save/load and core runtime setup paths.

## Phase 2: Runtime Architecture Hardening

- [x] Reassess package boundaries.
  - [x] Keep VST3-facing code, bridge glue, and generic runtime primitives clearly separated.
  - [x] Remove mixed-responsibility code where low-level and higher-level concerns are entangled.
- [x] Review lifecycle behavior.
  - [x] Audit `SetActive(false)` and related reset behavior in retained runtime code.
  - [x] Ensure state, parameter, and event handling reset deterministically in retained runtime paths.
- [x] Review process-layer structure.
  - [x] Refactor oversized runtime processing functions where readability is poor.
  - [x] Split transport updates, buffer mapping, and automation collection into smaller runtime helpers.
- [x] Keep helper abstractions honest.
  - [x] Only add base helpers if they reduce boilerplate without hiding core VST3 mechanics.

## Phase 3: Testing, Validation, And Performance

- [x] Expand automated coverage for retained packages.
  - [x] `pkg/framework/param`
  - [x] `pkg/framework/process`
  - [x] `pkg/framework/state`
  - [x] `pkg/midi`
  - [x] `pkg/plugin` where practical
  - [x] `pkg/vst3`
- [x] Add race-detector coverage where thread-safety assumptions are non-trivial.
- [x] Keep allocation-sensitive paths measurable.
  - [x] Benchmark process-context internals.
  - [x] Benchmark runtime hot paths where useful.
  - [x] Revisit fixed-size internal buffers where limits may be too rigid.
- [x] Keep validation expectations explicit.
  - [x] Define minimal validation expectations for the runtime layer itself.
  - [x] Document what downstream repos such as `synthkit` must validate on their own.

## Phase 4: Documentation And Consumer Experience

- [x] Document retained scope clearly.
  - [x] State the split boundary in README and PLAN.
  - [x] Add a concise “what belongs here vs. in `synthkit`” section if the README still needs tightening.
- [x] Improve API documentation.
  - [x] package-level docs
  - [x] lifecycle expectations
  - [x] thread-safety guarantees
  - [x] state/persistence expectations
- [x] Add migration guidance for the fork.
  - [x] Clarify intentional differences from the original project.
  - [x] Clarify the new module path.
  - [x] Document how downstream repos should consume this fork once versions are tagged.
- [x] Keep build and validation instructions accurate for the retained runtime repo.

## Phase 5: Web-Rendered Plugin UI

- [x] Define the editor surface and render scope.
  - [x] Identify the current plugin UI entry points and what `createView` should expose.
  - [x] Decide which controls, layout blocks, and parameter bindings must be rendered first.
- [x] Make the editor renderable through a web-based surface.
  - [x] Add a web-rendered view path for the VST editor instead of a standalone site.
  - [x] Keep the rendered UI aligned with the current plugin design and controls.
  - [x] Make local editing and parameter changes visible immediately in the view.
  - [x] Keep an explicit editor snapshot for save/restore and future React hydration.
  - [x] Document how HTML, CSS, and JS build outputs plug into the editor shell.
- [x] Treat the web-rendered editor as the prerequisite for platform expansion work.

## Phase 6: Cross-Platform Windows Support

- [x] Update the build system for Windows.
  - [x] Detect Windows cleanly.
  - [x] Produce the correct library format and bundle layout.
- [x] Extend the bridge for Windows entry points.
  - [x] Add required DLL entry handling.
  - [x] Export `GetPluginFactory`.
  - [x] Host the web-rendered editor with WebView2 on Windows.
  - [x] Lay down the Windows editor-view scaffold.
- [x] Review platform-specific CGO directives.
  - [x] Add Windows-specific flags only where necessary.
  - [x] Minimize platform divergence.
- [ ] Validate on real toolchains and hosts.
  - [ ] Start with MinGW-w64.
  - [ ] Keep MSVC as a later enhancement if justified.
- [x] Capture Windows-specific risks.
  - [x] Shared-library behavior with Go.
  - [x] Scheduler differences.
  - [x] File locking and reload friction.
  - [x] Path-length edge cases.

## Phase 7: Release Readiness

- [x] Keep architectural guardrails enforced.
- [x] Keep retained runtime packages tested and documented.
- [x] Confirm the public API boundary is stable enough for downstream repos.
- [x] Decide supported release scope explicitly.
  - [x] Linux/macOS only, or include Windows.
  - [x] Runtime-only, with higher-level features delegated to companion repos.
- [x] Capture a repeatable pre-tag release checklist.

## Phase 8: Framework Parity

Grow the runtime into a plugin framework that can replace JUCE for plugin
work, without taking on DSP (that stays in `synthkit`).

- [x] Parameter smoothing (`param.Ramp`, `ctx.Smoothed`) and host edit gestures.
- [x] `.vstpreset` files and a preset browser in the web editor (`pkg/preset`).
- [x] Offline runner and `pkg/plugintest` for testing plugins without a host.
- [x] CLAP plugin format from the same binary (`clap_entry`), validated with
      clap-validator and `hostcheck/clapcheck.c` on macOS and Linux.
  - [x] CLAP editor (`gui` extension) on the existing web views, checked in
        real windows on macOS and Linux (`editorcheck_darwin.m`, `editorcheck.c`).
  - [x] CLAP preset discovery for the user `.vstpreset` folder, with
        `preset-load` (clap-validator `preset-discovery-*`, clapcheck).
- [x] JavaScript SDK for web editors (`window.vst3go`): custom editor pages
      (`EditorProcessor`) with size limits and `vst3go.resize`, edit
      gestures, presets, custom messages with the processor, and typed
      TypeScript parameter bindings (`plugin.EditorBindings`), checked under
      node/tsc and in real web views on macOS and Linux.
- [x] Standalone application wrapper (`pkg/standalone`): the plugin hosted
      through its own CLAP entry with miniaudio device I/O, Core MIDI/ALSA/
      WinMM ports (`internal/midiport`), the editor in a window, settings
      and state kept between runs; checked with the null device, a virtual
      MIDI round trip and editor windows on macOS and Linux (Xvfb).
- [x] AUv2 wrapper on macOS (`au_darwin.c`): every Audio Unit hosts the
      plugin's CLAP entry; parameters, ClassInfo state, factory presets
      from programs, MIDI in and out, SysEx, scheduled parameters, host
      transport, latency/tail, side chains and the Cocoa editor; passes
      `auval -strict` and `hostcheck/aucheck_darwin.m`.

## Ongoing Rules

- [ ] If work belongs in `synthkit`, remove it from this file instead of duplicating it here.
- [ ] Avoid standalone roadmap documents; merge planning back here.
- [ ] Before adding a new abstraction, check whether it belongs in `vst3go` or in a higher-level companion repo.

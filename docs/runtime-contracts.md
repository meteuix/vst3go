English | [한국어](runtime-contracts.ko.md)

# Runtime Contracts

This repository ships the reusable VST3 binding and runtime layer, a DSP library for use inside `ProcessAudio`, and showcase plugins built on both. Product-specific logic (licensing, installers, branding) stays in consumer projects.

## Package Surface

Downstream projects should treat these packages as the supported API surface:

- `pkg/plugin`
- `pkg/vst3`
- `pkg/midi`
- `pkg/framework/bus`
- `pkg/framework/param`
- `pkg/framework/plugin`
- `pkg/framework/process`
- `pkg/framework/state`
- `pkg/preset`
- `pkg/plugintest`
- `pkg/audiofile`
- `pkg/standalone`
- `pkg/dsp/...`
- `pkg/framework/dsp`
- `pkg/framework/voice`
- `pkg/framework/debug`

The C bridge sources in `pkg/plugin`, embedded editor assets, and platform-specific editor shims support that API surface and are not called directly.

The DSP processors in `pkg/dsp` are created and sized outside the audio thread (constructor or `Initialize`) and their per-sample/per-buffer methods do not allocate, so they are safe to call from `ProcessAudio`. Each instance is owned by one processor and is not safe for concurrent use. `pkg/dsp/analysis` meters and scopes are also read from other threads: their `Get*` methods are safe from any goroutine and never make `Process` wait (readings are handed over by triple buffering), while `Process`, `Reset` and the setters stay on the audio thread.

## Lifecycle Expectations

- `plugin.Plugin` is a factory: `CreateProcessor` should return a fresh processor instance for each wrapper instance.
- `plugin.Processor.Initialize` prepares block-size and sample-rate dependent state.
- `plugin.Processor.SetActive(true)` should make the processor ready for realtime work without further allocation-sensitive setup.
- `plugin.Processor.ProcessAudio` is the steady-state audio callback and should avoid avoidable allocations and blocking work.
- `plugin.Processor.SetActive(false)` is the reset boundary for transport state, pending events, smoothing state, and other runtime-only caches.
- `plugin.StatefulProcessor` is optional and should only be implemented when parameter values alone are not enough to restore processor behavior.
- `Initialize` runs once when the host initializes the component and again from every `setupProcessing` call with the host sample rate and block size. Bus arrangements are final when `SetActive(true)` is called.
- Optional host features (64-bit processing, bus arrangements, MIDI mapping/learn, note expression, keyswitches, programs, units, channel info, host messages) are optional interfaces on the processor, described in `docs/plugin-capabilities.md`.
- `plugin.Register` may be called once per plugin; one binary can expose several plugins.
- A `DistributedPlugin` gets two processor instances per plugin instance: the processor side runs the lifecycle above, the controller side only serves parameters, the editor and controller queries (no `Initialize`, `SetActive` or `ProcessAudio`).
- A CLAP instance runs one processor through the same lifecycle: CLAP `init` calls `Initialize`, `activate` calls `Initialize` again with the host sample rate and maximum block size and then `SetActive(true)`, `deactivate` calls `SetActive(false)`. CLAP `reset` (audio thread) calls `SetActive(false)` and `SetActive(true)` without reallocating. A `DistributedPlugin` runs as one CLAP object.
- An Audio Unit instance (macOS) hosts one CLAP instance: `AudioUnitInitialize` activates it at the output sample rate and maximum frames per slice, the first render after that starts processing, `AudioUnitUninitialize` stops and deactivates it, and `AudioUnitReset` calls CLAP `reset`. Render, parameter flushes and reactivation are serialized by one lock; plugin callbacks (`request_callback`, edits for parameter listeners, editor resizes) run on the main dispatch queue.
- An LV2 instance (Linux) hosts one CLAP instance: `activate` activates it at the instantiation sample rate and `bufsz:maxBlockLength` (4096 without the option), the first `run` starts processing, `deactivate` stops and deactivates it. `run` is the audio thread; instantiation, activation, state and the UI's idle callback count as the main thread, and plugin `request_callback` work runs from the idle callback (and around activation and state calls). A restart request (latency change) reactivates the plugin at the start of the next `run`.
- A standalone application (`pkg/standalone`) is a CLAP host of its own plugin: one instance per process, activated at the device's sample rate and period, deactivated and reactivated when the plugin asks for a restart. Its main thread is the main goroutine (the package locks it to the process main thread in `init`), which runs the window and event loop; the audio device's thread runs `process`.

## Thread-Safety Expectations

- `pkg/framework/param.Registry` lookups take no lock: they read an immutable snapshot, and registration publishes a new one. Individual parameter values are atomics.
- The audio path takes no lock the runtime holds elsewhere: the per-call instance lookup of every host entry point reads an immutable snapshot, and CLAP plugin-side edits reach `process` through a lock-free queue.
- `pkg/framework/process.Context` is owned by one processing pass at a time and should not be shared concurrently across worker goroutines.
- `ProcessAudio` runs on the host audio thread; every other `Processor` method (including MIDI learn, channel info and `ReceiveMessage`) and all `Host` calls run on host/UI threads. The runtime serializes host-thread lifecycle calls per instance. Under CLAP, `Host` edits are queued and reported to the host from the next `process` or `flush` call.
- The runtime splits each host block at parameter change offsets and calls `ProcessAudio` once per segment, so a processor can rely on parameter values being constant inside one call.
- `pkg/framework/state.Manager` is a serialization helper and should be driven from host state callbacks, not from concurrent realtime audio paths.
- Platform editor bridges should treat the Go-side parameter model as the source of truth and route UI changes back through the normal parameter update flow instead of mutating parallel state.

## Real-Time Behavior

Processing is allocation-free and lock-free on the runtime side, but it runs on the Go runtime, which does not give hard real-time guarantees: a call that enters Go during a garbage collector stop-the-world pause waits for it. The pauses are short (sub-millisecond) and do not grow with the heap, so keep the rest of the plugin's allocation rate moderate and measure with `just rtcheck`. It calls a typical effect chain from a native real-time thread (as a host does) while other goroutines allocate, and fails when a block misses its budget. On an Apple M4 with 128-sample blocks at 48 kHz (2.67 ms budget), the longest call went from 0.87 ms with no garbage to 1.11 ms at 1 GB/s of allocation (56 GC cycles in 20 s), with no block over budget.

## State And Persistence

- Parameter state is the default persistence layer.
- `pkg/framework/state.Manager` writes a simple repo-owned binary format with a header, version, ordered parameter values, and optional custom payload.
- Unknown parameter IDs are ignored on load for forward compatibility.
- The program selector (`plugin.ProgramParamID`) is stored like any parameter; restoring it does not re-apply the program over the restored values.
- Program data written by hosts into program list slots (`IProgramListData`) uses a separate opaque format and is not part of the component state.
- Custom state should be appended only through `StatefulProcessor` or `state.Manager` custom save/load hooks.
- The web editor snapshot is a UI-facing mirror of the registry state; it is useful for local editor restore/hydration, but host-visible plugin state should still flow through the VST3 `GetState` / `SetState` callbacks.

## Downstream Consumption

When a downstream repo consumes this fork:

- import `github.com/th-release/vst3go`
- build plugin/runtime logic on top of the supported packages above
- use `pkg/dsp` for signal processing and `examples/` as reference plugins
- keep product-specific code (licensing, installers, branding) in the downstream repo
- validate host-specific behavior, installer layout, signing, and DAW compatibility in the downstream repo

Once tags are published, downstream repos should pin a tagged version instead of a moving branch so runtime and bridge changes can be upgraded deliberately.

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
- `pkg/ara`

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

- An ARA plugin (`plugin.ARAPlugin`) gets one `ara.Document` and controller per document controller the host creates; the document lives until the host destroys it, independently of the plugin instances. An instance is bound to at most one document (`ARAProcessor.BindARA`; the host binds once, before activating the instance) and unbound when it is destroyed, in either order with its document.

## Thread-Safety Expectations

- `pkg/framework/param.Registry` lookups take no lock: they read an immutable snapshot, and registration publishes a new one. Individual parameter values are atomics.
- The audio path takes no lock the runtime holds elsewhere: the per-call instance lookup of every host entry point reads an immutable snapshot, and CLAP plugin-side edits reach `process` through a lock-free queue.
- `pkg/framework/process.Context` is owned by one processing pass at a time and should not be shared concurrently across worker goroutines.
- `ProcessAudio` runs on the host audio thread; every other `Processor` method (including MIDI learn, channel info and `ReceiveMessage`) and all `Host` calls run on host/UI threads. The runtime serializes host-thread lifecycle calls per instance. Under CLAP, `Host` edits are queued and reported to the host from the next `process` or `flush` call.
- The runtime splits each host block at parameter change offsets and calls `ProcessAudio` once per segment, so a processor can rely on parameter values being constant inside one call.
- `pkg/framework/state.Manager` is a serialization helper and should be driven from host state callbacks, not from concurrent realtime audio paths.
- ARA model calls (document edits, archiving, content readers, `Binding` role changes) run on the host's main thread, as ARA requires; a plugin's controller methods and editor code that touches the `ara.Document` run there too. The analysis runs on its own goroutine per audio source. On the audio thread, `Binding.Playback` and the `RegionPlayback` methods read atomic snapshots and the source's samples in memory, never the host's audio reader.
- Platform editor bridges should treat the Go-side parameter model as the source of truth and route UI changes back through the normal parameter update flow instead of mutating parallel state.

## Real-Time Behavior

Processing is allocation-free and lock-free on the runtime side, but it runs on the Go runtime, which does not give hard real-time guarantees: a call that enters Go during a garbage collector stop-the-world pause waits for it. The pauses are short (sub-millisecond) and do not grow with the heap, so keep the rest of the plugin's allocation rate moderate and measure with `just rtcheck`. It calls a typical effect chain from a native real-time thread (as a host does) while other goroutines allocate, and fails when a block misses its budget. On an Apple M4 with 128-sample blocks at 48 kHz (2.67 ms budget), the longest call went from 0.87 ms with no garbage to 1.11 ms at 1 GB/s of allocation (56 GC cycles in 20 s), with no block over budget.

The runtime also makes those pauses rare. With Go's own pacing the collector runs whenever the heap doubles from a 4 MB minimum and at least every two minutes, so the small allocations of an editor or of parameter text keep it running every few minutes. When the first instance is created, vst3go turns the percentage pacing off and sets a memory limit of the live heap plus a headroom (`plugin.DefaultGCHeadroom`, 64 MB; or the live heap when it is larger), moved after every cycle: the collector runs only after the binary has allocated that much since the last one. Activating an instance collects first when more than 1 MB was allocated since the last cycle, so the garbage of creating and loading an instance goes before the first block. The cost is up to the headroom in extra memory. `plugin.SetGCHeadroom(bytes)` (from `init`) changes it and 0 restores Go's pacing; the `VST3GO_GC_HEADROOM` (bytes), `GOGC` and `GOMEMLIMIT` environment variables take precedence. `just rtcheck` allocating 1 MB/s for 60 s ran no collection with the headroom, against 6 with `GOGC=100`.

The deadline guard bounds what such a stall costs, and it is on by default: `NewBaseProcessor` sets a share of `DefaultProcessDeadline` (0.5), and a processor that does not use `BaseProcessor` gets the same unless it implements `plugin.DeadlineProcessor`. `SetProcessDeadline(0)` turns it off and runs the blocks on the host's audio thread. With the guard, blocks run on real-time worker threads (a time-constraint thread on macOS, `SCHED_FIFO` on Linux where the process may use it, time-critical priority on Windows). The binary's instances share them: each activated instance adds one worker to the pool, so an instance without a block in flight always finds a free one, and a block goes to the worker the calling host thread used last, which keeps spinning for 20 µs after a job, so a host running many instances in a row on one thread hands each block over without a thread wake-up. Where waking a thread is slow (virtual machines park idle CPUs for a millisecond or more), a worker whose wake-up took more than a quarter of the deadline spins through the gaps between blocks for its next 4096 jobs, up to 6 ms at a time. A worker enters Go once when it starts, at activation, so binding the thread to the runtime is not paid inside a block. Workers, and calls into Go on the host's thread, run with denormals flushed to zero on x86 (FTZ/DAZ), where they are a hundred times slower; ARM handles them at full speed. The host's audio thread runs only C code: it copies the block into memory the worker owns, waits at most `share` of the block's duration, and copies the result back. It spins for the first 30 µs and the last stretch before the deadline, and sleeps in between only as long as the system's timed waits have proved accurate (measured when the first guard starts and on every wait), so it returns at the deadline even where sleeps wake milliseconds late. A block that misses the deadline outputs silence and finishes in the background. Blocks that arrive meanwhile output silence too; their note and MIDI events (moved to offset 0) and the last value of each parameter they change are carried into the next block that runs, as are the events and parameter changes that do not fit a job (2048 VST3 or 4096 CLAP events, 1024 parameter queues, 8192 points), so none is lost. The output events and output parameter values of a late block are dropped. Parameter flushes and resets on the audio thread run as jobs too, and starting or stopping processing does not enter Go, so in real-time mode the host's audio thread never waits on the Go runtime. Offline rendering runs on the host's thread once the worker is free, without a deadline, as does a block that breaks the host contract (more samples, buses or channels than the plugin was set up for). The setting is read when the plugin is activated and applies to VST3, CLAP, AU, LV2 and the standalone application. The environment variable `VST3GO_PROCESS_DEADLINE=<share>` overrides it for every plugin in the process (`0` turns it off). With a share of 0.5 the host keeps the other half of the block for itself. Under LV2, `run` then never enters Go, so `plugins.ttl` declares `lv2:hardRTCapable`; a `freewheel` port (`lv2:freeWheeling`) switches the plugin to offline rendering while the host freewheels, and a latency change is reported on the latency port without reactivating the plugin from `run`. The standalone application's audio callback is C (vendored miniaudio): it takes MIDI input from a ring the MIDI ports fill and leaves the plugin's MIDI output in a ring a goroutine sends on. `internal/validation/stall` stalls for 60 ms, and hostcheck, clapcheck, aucheck and lv2check check that the call returns within the deadline with silence and that the skipped blocks' notes arrive afterwards; hostcheck and clapcheck also check that events past a job's capacity arrive with the next block, clapcheck that 32 instances on four audio threads, sharing the workers while more are created and destroyed, never get another block's audio, and lv2check that freewheeling renders without a deadline. Independently of the guard, the Audio Unit render call never waits for the main thread: while the main thread holds the render lock (to reinitialize, or to flush parameters through Go), the block renders silence.

## State And Persistence

- Parameter state is the default persistence layer.
- `pkg/framework/state.Manager` writes a simple repo-owned binary format with a header, version, ordered parameter values, and optional custom payload.
- Unknown parameter IDs are ignored on load for forward compatibility.
- The program selector (`plugin.ProgramParamID`) is stored like any parameter; restoring it does not re-apply the program over the restored values.
- Program data written by hosts into program list slots (`IProgramListData`) uses a separate opaque format and is not part of the component state.
- Custom state should be appended only through `StatefulProcessor` or `state.Manager` custom save/load hooks.
- An ARA plugin's document data (`ModificationArchiver`, `SourceArchiver`, `DocumentArchiver`) is stored in the host's song through ARA archiving, separately from the instance state; `Config.ArchiveID` names its format.
- The web editor snapshot is a UI-facing mirror of the registry state; it is useful for local editor restore/hydration, but host-visible plugin state should still flow through the VST3 `GetState` / `SetState` callbacks.

## Downstream Consumption

When a downstream repo consumes this fork:

- import `github.com/th-release/vst3go/v2`
- build plugin/runtime logic on top of the supported packages above
- use `pkg/dsp` for signal processing and `examples/` as reference plugins
- keep product-specific code (licensing, installers, branding) in the downstream repo
- validate host-specific behavior, installer layout, signing, and DAW compatibility in the downstream repo

Once tags are published, downstream repos should pin a tagged version instead of a moving branch so runtime and bridge changes can be upgraded deliberately.

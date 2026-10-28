# Runtime Contracts

This repository is intentionally narrow: it ships the reusable VST3 binding and runtime layer, not product-specific DSP or host-validation logic.

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

The C bridge sources in `pkg/plugin`, embedded editor assets, and platform-specific editor shims support that API surface but are not intended to become a higher-level product framework.

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

## Thread-Safety Expectations

- `pkg/framework/param.Registry` uses internal locking for parameter lookup and registration helpers.
- Individual parameter reads and writes are intended to be safe for the runtime paths used by this repo, but callers should still avoid long critical sections or external lock ordering around registry access.
- `pkg/framework/process.Context` is owned by one processing pass at a time and should not be shared concurrently across worker goroutines.
- `ProcessAudio` runs on the host audio thread; every other `Processor` method (including MIDI learn, channel info and `ReceiveMessage`) and all `Host` calls run on host/UI threads. The runtime serializes host-thread lifecycle calls per instance.
- The runtime splits each host block at parameter change offsets and calls `ProcessAudio` once per segment, so a processor can rely on parameter values being constant inside one call.
- `pkg/framework/state.Manager` is a serialization helper and should be driven from host state callbacks, not from concurrent realtime audio paths.
- Platform editor bridges should treat the Go-side parameter model as the source of truth and route UI changes back through the normal parameter update flow instead of mutating parallel state.

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
- keep DSP/product code outside this repo
- validate host-specific behavior, installer layout, signing, and DAW compatibility in the downstream repo

Once tags are published, downstream repos should pin a tagged version instead of a moving branch so runtime and bridge changes can be upgraded deliberately.

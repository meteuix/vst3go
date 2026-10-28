# Plugin Capabilities

What the runtime supports, and how a processor opts into each feature. The
plugins under `internal/validation/` exercise every item here and pass the
Steinberg validator in extensive mode (`just validate-vst3 <validator>`).

| Plugin type | Bus template | Validation plugin |
| --- | --- | --- |
| Audio effect (mono/stereo, surround) | `bus.NewEffectStereo`, `NewSurround5_1Effect`, `NewMultiChannelEffect` | `gain` |
| Sidechain effect, lookahead/latency | `bus.NewEffectStereoSidechain` | `ducker` |
| Instrument (synth, sampler, drums) | `bus.NewGenerator` | `synth` |
| MIDI effect (arpeggiator, transposer) | `bus.NewMIDIEffect` | `midifx` |
| Analyzer/meter | `bus.NewAnalyzer` + read-only parameters | `gain` (meter) |
| Multi-output (crossover, drum outs) | `bus.NewCrossover`, `NewSplitter`, `WithAuxOutput` | — |

## Building a plugin

A plugin is a `main` package that registers itself:

```go
func init() { plugin.Register(myPlugin{}) }
func main() {}
```

`scripts/build_vst3.sh <package> <Name> [out]` (or `just vst3 <package> <Name>`)
builds a macOS (`Contents/MacOS`) or Linux (`Contents/<arch>-linux`) bundle with
`-buildmode=c-shared`. Windows uses `scripts/build_windows_vst3.sh`.

`plugin.Info.Category` selects the host category, for example
`CategoryFxEQ`, `CategoryFxDynamics` or `CategoryInstrumentSynth`; combine
several with `|`.

## Processing model

`ProcessAudio(ctx)` runs on the audio thread. It must not lock, block or do I/O.

- **Buses.** `ctx.Input`/`ctx.Output` are the main buses.
  `ctx.InputBus(i)`/`ctx.OutputBus(i)` reach every bus, `ctx.Sidechain()` the
  first aux input. An inactive or unconnected bus has no channels.
- **Sample-accurate automation.** The runtime splits the host block at
  parameter change offsets, so `ctx.Param`/`ctx.ParamPlain` always hold the
  value valid for the current segment. `ctx.NumSamples()` is the segment
  length and `ctx.BlockOffset()` its start inside the host block.
- **Events** keep their host-block offsets: inside a segment, the sample index
  of an event is `event.SampleOffset() - ctx.BlockOffset()`.
  `ctx.GetAllInputEvents()` returns the segment's events in order.
- **Silence flags.** `ctx.InputSilenceFlags(bus)` reports silent input channels;
  `ctx.SetOutputSilenceFlags(bus, mask)` tells the host an output is silent.
- **Output parameters** (meters, gain reduction): `ctx.SetOutputParameter(id, normalized)`
  on a `ReadOnly()` parameter. The value goes to the host and the editor.
- **Transport.** `ctx.Transport` carries tempo, time signature, musical position,
  cycle and play/record state; each field group has a `Has*` validity flag.
- **Offline rendering.** `ctx.ProcessMode` is `ProcessModeRealtime`,
  `ProcessModePrefetch` or `ProcessModeOffline`.
- Only 32-bit float processing is offered to hosts.

## MIDI and notes

Input events arrive as `pkg/midi` types:

- `NoteOnEvent`/`NoteOffEvent` with 7-bit `Velocity`, full-precision
  `VelocityNorm`, `Tuning` (cents), `Length` and the host `NoteID`
  (`midi.NoteIDNone` when unassigned). Match note-offs on `NoteID` when both
  sides carry one.
- `PolyPressureEvent`, `NoteExpressionEvent` (`NoteExpressionVolume`,
  `...Tuning`, `...Brightness`, ...), and `SysExEvent`.

VST3 hosts do not send MIDI CC, pitch bend or channel aftertouch as events.
Two ways to receive them:

- `MIDIControllerProcessor.MIDIControllerChannels() int` — the runtime maps
  every controller to a hidden parameter (IDs from `MIDIControllerParamIDBase`)
  and turns its changes back into sample-accurate `ControlChangeEvent`,
  `PitchBendEvent` and `ChannelPressureEvent` input events. Plugin parameter
  IDs must stay below `MIDIControllerParamIDBase`.
- `MIDIMappingProcessor.MIDIControllerParameter(bus, channel, cc)` — map a
  controller straight to one of your parameters (mod wheel to vibrato depth).
  These mappings take precedence.

Output events: `ctx.AddOutputEvent(event)` with an event output bus. Notes,
poly pressure and note expression go out as VST3 events; CC, pitch bend,
channel pressure and program change go out as legacy MIDI CC events. SysEx
output is not supported.

## Bus layouts

- Configure buses with `bus.NewBuilder()` or a template. Main buses come first,
  then aux buses. Main buses are reported active by default, aux buses are
  enabled by the host on demand.
- Set an explicit speaker layout with `WithAudioInputArrangement`/`WithAudioOutputArrangement`
  (`bus.Arrangement51`, `bus.Arrangement71Cine`, ...).
- Accept other layouts from the host by implementing
  `BusArrangementProcessor.AcceptBusArrangements(inputs, outputs) bool`.
  When it returns true the runtime updates the bus configuration and channel
  counts before processing starts.

## Latency and tail

- `GetLatencySamples()` reports processing latency (lookahead, linear-phase
  filters). After it changes, call `Host.RestartComponent(plugin.RestartLatencyChanged)`
  from a non-audio thread.
- `GetTailSamples()` reports the decay after input stops; return
  `plugin.TailInfinite` for generators that never decay.

## Host interaction

Implement `HostAwareProcessor.SetHost(plugin.Host)` to talk back to the host
(from the UI/main thread only):

- `EditParameter(id, normalized)` — plugin-side parameter changes (preset
  loads, MIDI learn) recorded by the host as a full edit gesture.
- `RestartComponent(flags)` — ask the host to re-read latency, parameter
  values/titles, I/O layout or MIDI assignments.

## Parameters and state

- `param.Choice` creates list parameters (`IsList`, `N-1` steps); stepped
  parameters snap to their steps when denormalized, matching the VST3 rule.
- `param.BypassParameter` is flagged `IsBypass` so hosts link it to their
  bypass switch.
- Without a custom parser, string-to-value parsing accepts the formatted
  display text ("-6.0 dB", "Q: 0.71", "-∞ dB").
- Parameter values are persisted automatically. Implement `StatefulProcessor`
  for extra state (sample paths, wavetables, sequencer patterns).

## Known limits

- One class per binary; processor and controller are one object (single
  component). Distributed processor/controller setups are not supported.
- Input events are passed as `midi.Event` interfaces; each event is a small
  allocation on the audio thread.
- `INoteExpressionController` is not implemented, so hosts that only send
  note expression to plugins advertising it (Cubase) will not send any.
- 64-bit sample processing, unit/program lists and IInfoListener are not
  implemented.

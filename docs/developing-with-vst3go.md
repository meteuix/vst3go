English | [한국어](developing-with-vst3go.ko.md)

# Developing With `vst3go`

This guide shows how to build a plugin on top of `vst3go`, from the first `Plugin`/`Processor` pair all the way to editor metadata, state restore, and release validation.

It is prescriptive on purpose: if you are following this guide, treat the layouts, naming, and state flow below as the default implementation path.

If you follow the full guide in order, you end up with one working plugin: one metadata definition, one processor, one editor shell, one state path, and one validation flow.

The short version is:

- `vst3go` is the runtime and VST3 shell.
- `pkg/dsp` provides the signal processing building blocks (filters, dynamics, delay, modulation, reverb, distortion, oscillators, envelopes).
- Your downstream repo owns the actual effect/instrument behavior and how those blocks are combined.
- `examples/` holds complete plugins built this way; start from the closest one.

If you want a mental model, think of `vst3go` as the layer that answers:

- What is the plugin called?
- What parameters does it expose?
- What audio and event buses does it have?
- How does it process one block of audio?
- How does it save and restore state?
- How does the native editor reflect and update parameters?

It does not try to be a full DAW framework.

For the full list of supported host features (instruments, MIDI CC and pitch
bend, MIDI output, sidechains, bus layouts, latency, output meters, offline
rendering) see [`plugin-capabilities.md`](plugin-capabilities.md). Working
reference plugins live in `internal/validation/`.

## 1. The Three Layers You Need To Understand

When you build a plugin with this repo, there are three separate layers:

1. **Metadata layer**
   - `pkg/framework/plugin.Info` contains the name, ID, vendor, version, and category.
   - `pkg/plugin.Plugin.GetInfo()` returns that metadata to the host.

2. **Runtime layer**
   - `pkg/plugin.Processor` is the main contract.
   - It owns parameters, buses, lifecycle, and block processing.
   - The wrapper in `pkg/plugin` turns this into a VST3 component/controller pair.

3. **Editor/state layer**
   - `pkg/plugin.EditorModel` and `pkg/plugin.EditorSnapshot` describe what the browser-rendered editor sees.
   - `pkg/framework/state.Manager` and `StatefulProcessor` handle persistence beyond parameter values.
   - The web editor shell is only a view of the same parameter/state model.

The important rule is that the audio processor is the source of truth. The editor mirrors it, not the other way around.

## 2. What A Plugin Looks Like In Practice

At minimum, a plugin needs:

- a `pkg/plugin.Plugin` implementation
- a `pkg/plugin.Processor` implementation
- a `pkg/framework/param.Registry`
- a `pkg/framework/bus.Configuration`
- custom state hooks when parameter values are not enough

The runtime wrapper uses the following flow:

1. host asks for plugin metadata
2. host creates a processor instance
3. wrapper calls `Initialize(sampleRate, maxBlockSize)`
4. host starts processing
5. `ProcessAudio(ctx)` gets called repeatedly
6. editor/view asks for current parameter state when needed
7. host saves or restores state through `GetState` and `SetState`

## 3. A Minimal Plugin Skeleton

This is the shape you want in a downstream repo:

```go
package myplugin

import (
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
)

type Plugin struct{}

func (p *Plugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.example.myplugin",
		Name:     "My Plugin",
		Version:  "0.1.0",
		Vendor:   "Example Co",
		Category: frameworkplugin.CategoryFx,
	}
}

func (p *Plugin) CreateProcessor() plugin.Processor {
	return NewProcessor()
}

type Processor struct {
	params *param.Registry
	buses  *bus.Configuration
}
```

That part is boring on purpose. The important work is in the processor itself.

## 4. Build The Processor Around Real Parameters

Parameters are designed first, not added as an afterthought.

For each control, decide:

- is this a continuous or discrete parameter?
- should it be automatable?
- should it be hidden?
- should it be read-only?
- what is the plain range?
- how should it be formatted in the editor?

The builder helpers in `pkg/framework/param` are meant to keep this clean:

- `GainParameter`
- `FrequencyParameter`
- `MixParameter`
- `TimeParameter`
- `RatioParameter`
- `QParameter`
- `PanParameter`
- `PhaseParameter`
- `Choice`

### Example Parameter Block

For an `Eight EQ`-style plugin, the parameter set is:

- `Input Gain`
- `Band 1` through `Band 8`
  - each band has `Type`, `Frequency`, `Gain`, and `Q`
  - optional extras are `Shelf Slope`, `Stereo Link`, and `Solo`
- `Output Gain`
- `Analyzer`
- `Bypass`

Example setup:

```go
func NewProcessor() *Processor {
	params := param.NewRegistry()

	_ = params.Add(
		param.GainParameter(1, "Input Gain").Default(0).Build(),
		param.New(2, "Band 1 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(3, "Band 1 Frequency", 20, 20000, 120).Build(),
		param.GainParameter(4, "Band 1 Gain").Default(0).Build(),
		param.QParameter(5, "Band 1 Q", 0.1, 12.0, 0.707).Build(),
		param.New(6, "Band 2 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(7, "Band 2 Frequency", 20, 20000, 250).Build(),
		param.GainParameter(8, "Band 2 Gain").Default(0).Build(),
		param.QParameter(9, "Band 2 Q", 0.1, 12.0, 0.707).Build(),
		param.New(10, "Band 3 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(11, "Band 3 Frequency", 20, 20000, 500).Build(),
		param.GainParameter(12, "Band 3 Gain").Default(0).Build(),
		param.QParameter(13, "Band 3 Q", 0.1, 12.0, 1.0).Build(),
		param.New(14, "Band 4 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(15, "Band 4 Frequency", 20, 20000, 1000).Build(),
		param.GainParameter(16, "Band 4 Gain").Default(0).Build(),
		param.QParameter(17, "Band 4 Q", 0.1, 12.0, 1.0).Build(),
		param.New(18, "Band 5 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(19, "Band 5 Frequency", 20, 20000, 2000).Build(),
		param.GainParameter(20, "Band 5 Gain").Default(0).Build(),
		param.QParameter(21, "Band 5 Q", 0.1, 12.0, 1.0).Build(),
		param.New(22, "Band 6 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(23, "Band 6 Frequency", 20, 20000, 4000).Build(),
		param.GainParameter(24, "Band 6 Gain").Default(0).Build(),
		param.QParameter(25, "Band 6 Q", 0.1, 12.0, 1.0).Build(),
		param.New(26, "Band 7 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(27, "Band 7 Frequency", 20, 20000, 8000).Build(),
		param.GainParameter(28, "Band 7 Gain").Default(0).Build(),
		param.QParameter(29, "Band 7 Q", 0.1, 12.0, 1.0).Build(),
		param.New(30, "Band 8 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(31, "Band 8 Frequency", 20, 20000, 12000).Build(),
		param.GainParameter(32, "Band 8 Gain").Default(0).Build(),
		param.QParameter(33, "Band 8 Q", 0.1, 12.0, 0.707).Build(),
		param.GainParameter(34, "Output Gain").Default(0).Build(),
		param.New(35, "Analyzer").Toggle().Build(),
		param.New(36, "Bypass").Toggle().Bypass().Build(),
	)

	return &Processor{
		params: params,
		buses:  bus.Stereo(),
	}
}
```

For a `Utility`-style plugin, the parameter set is different:

- `Input Gain`
- `Output Gain`
- `Pan`
- `Width`
- `Mono`
- `Phase Invert L`
- `Phase Invert R`
- `Mute`

Those are all stock controls that users already understand, which is exactly why the plugin feels like a utility tool instead of a special effect.

## 5. Choose Your Bus Layout First

The bus layout determines how the host sees your plugin.

Use the helpers in `pkg/framework/bus`:

- `bus.Stereo()` for the common stereo-in/stereo-out effect
- `bus.Mono()` for a simple single-channel utility or instrument shell
- `bus.NewBuilder()` for sidechain or multibus layouts

### Stereo EQ

EQs start with a stereo main input and output:

```go
buses := bus.Stereo()
```

### Utility Plugin

If you are building a Utility-like tool, stereo I/O is still the normal choice:

```go
buses := bus.Stereo()
```

Even if the processing is simple, keep the same I/O shape as the host expects. A utility plugin manipulates the stereo image, gain, and phase, so stereo in/out is the right default.

### Sidechain Example

If you later want ducking or external analysis, use a builder:

```go
buses := bus.NewBuilder().
	WithStereoInput("Main In").
	WithStereoOutput("Main Out").
	WithSidechain("Sidechain In").
	MustBuild()
```

The guide rule here is simple: define the buses from the user-facing behavior backward, not from the implementation forward.

## 6. Implement `Initialize`, `SetActive`, And `ProcessAudio`

The lifecycle matters because `vst3go` is designed around realtime audio constraints.

### `Initialize`

Use `Initialize(sampleRate, maxBlockSize)` to allocate or configure anything that depends on host sample rate or block size.

Typical responsibilities:

- initialize filter coefficients
- reset envelopes or delay lines
- cache sample-rate-dependent constants
- prepare lookup tables

Do not do host-independent plugin setup here.

### `SetActive`

Use `SetActive(true)` when processing starts, and `SetActive(false)` when the host stops or deactivates the plugin.

Typical responsibilities:

- reset smoothing
- clear delay lines if needed
- clear pending MIDI/events
- reset transport followers
- prepare the processor for a clean next start

### `ProcessAudio`

This is the realtime callback. It is:

- allocation-free
- lock-free where possible
- branch-light
- deterministic

The `process.Context` gives you convenient access to:

- `Input`
- `Output`
- `NumSamples()`
- `NumInputChannels()`
- `NumOutputChannels()`
- `Param(id)`
- `ParamPlain(id)`
- `ProcessChannels`
- `ProcessStereo`
- `ProcessSamples`
- `PassThrough`
- `Clear`

### A Good Default Pattern

For an `Eight EQ`-style processor, a clean starting point is:

```go
func (p *Processor) ProcessAudio(ctx *process.Context) {
	if ctx == nil {
		return
	}

	if ctx.Param(11) >= 0.5 {
		ctx.PassThrough()
		return
	}

	ctx.ProcessStereo(func(ch int, input, output []float32) {
		for i := range input {
			sample := input[i]
			sample = p.processEQChain(sample, ctx)
			sample = applyGain(sample, ctx.ParamPlain(1), ctx.ParamPlain(34))
			output[i] = sample
		}
	})
}
```

`processEQChain` and `applyGain` are your plugin's code; build them from `pkg/dsp/filter` and `pkg/dsp/gain` as shown below.

## 7. How To Build An `Eight EQ`-Style Plugin

This is the most natural example because it exercises almost everything:

- continuous parameters
- tonal controls
- sample-rate-dependent math
- realtime audio processing
- editor controls
- state persistence

### 7.1 Decide The Control Model

An `Eight EQ`-style plugin has:

- input gain
- eight independently addressable bands
- per-band filter type
- per-band gain, frequency, and Q
- output gain
- analyzer enable
- bypass

The important difference from a 3-band EQ is that the user is not thinking in three fixed tone zones anymore. They are thinking in a chain of surgical or musical filters that are combined, adjusted independently, and shown clearly in the editor.

Add these after the first version ships:

- output meter
- phase invert
- oversampling toggle
- analyzer enable

Keep the first version small.

### 7.2 Design The Screen

If the EQ is going to feel like a real instrument instead of a pile of knobs, the screen layout needs to match the signal flow.

A first `Eight EQ` screen has four visual zones:

1. **Top strip**
   - plugin name
   - preset selector
   - undo/redo if your downstream app has it
   - bypass
   - input/output level readout

2. **Center graph**
   - response curve view
   - eight band nodes arranged across the spectrum
   - real-time curve response if analyzer data exists
   - frequency axis labels
   - gain axis labels

3. **Band control matrix**
   - one card per band in a 4x2 grid on wide screens
   - each card exposes gain, frequency, Q, and type
   - each card shows enabled or bypass state
   - each card collapses into a compact row on narrow screens

4. **Bottom utility strip**
   - global bypass
   - output gain
   - oversampling
   - analyzer enable
   - mono sum or phase utility if you include it

The screen reads left-to-right as:

- input enters
- bands shape the tone
- output leaves

That is the visual story users need.

#### Suggested Wireframe

```text
┌──────────────────────────────────────────────────────────────┐
│  My EQ   Preset ▼   Bypass   Input ▓▓▓▓▓   Output ▓▓▓░░      │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│                frequency response / analyzer                 │
│          ────────●────────────●────────────●──────          │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│ Band 1     Band 2     Band 3     Band 4                      │
│ Gain FreqQ Gain FreqQ Gain FreqQ Gain FreqQ                  │
├──────────────────────────────────────────────────────────────┤
│ Band 5     Band 6     Band 7     Band 8                      │
│ Gain FreqQ Gain FreqQ Gain FreqQ Gain FreqQ                  │
├──────────────────────────────────────────────────────────────┤
│ Input Gain   Output Gain   Bypass   Analyzer   Oversampling  │
└──────────────────────────────────────────────────────────────┘
```

#### What Each Zone Does

- the top strip anchors the product identity
- the graph gives immediate feedback on tone shape
- the band cards provide precise control
- the bottom strip keeps the utility actions out of the way

#### Why This Layout Works

- it makes the EQ feel like a proper tool, not a generic parameter panel
- it scales to advanced features without changing the mental model
- it maps naturally to `EditorModel.Sections`
- it remains readable on smaller windows because the band cards stack

#### Suggested Component Breakdown

Use the screen as a set of deliberately named components:

- `PluginHeader`
  - shows plugin name, preset name, bypass, and undo/redo when supported
  - stays compact so the graph gets the most space

- `ResponseGraph`
  - visualizes the combined EQ curve
  - shows draggable band nodes at low, mid, and high frequency points
  - overlays analyzer data or a simple static curve preview

- `BandCard`
  - one card per EQ band
  - contains gain, frequency, Q, and type controls
  - includes a band label such as `Band 1`, `Band 2`, and so on
  - shows a tiny state badge such as `solo`, `bypass`, or `linked`

- `GlobalControls`
  - handles input/output gain, oversampling, and analyzer toggle
  - carries global bypass and utility toggles

- `StatusStrip`
  - shows sample rate, oversampling state, or license state if your product has one
  - stays readable but never noisy

If you are implementing the UI in React, those components map to independent functional components or reusable sections. If you are implementing the UI directly in HTML/CSS/JS, they are still distinct visual regions.

#### Suggested Interaction Model

The EQ screen supports these interactions:

- dragging a node on the response graph changes frequency and gain together
- dragging a band card knob changes only one parameter at a time
- double-clicking a control resets it to its default
- shift-drag provides fine adjustment
- hover reveals exact values or tooltips
- clicking a band card can focus that band in the graph
- clicking a band type chip can cycle through filter shapes
- bypass is always obvious and instantly reversible

The graph and the knob view stay in sync:

- if a user moves a node, the card values update immediately
- if a host automation lane moves a parameter, the node moves too
- if a snapshot is restored, both the graph and the cards jump to the restored state at the same time

That immediate visual synchronization is what makes the screen feel professional.

#### Suggested Responsive Behavior

Do not let the EQ become unusable on smaller windows.

Required behavior:

- wide screens show graph on top and eight band cards below it in a 4x2 grid
- medium screens keep the graph wide and let the cards wrap into two rows of four
- narrow screens stack the band cards vertically and keep the graph tall enough to remain useful
- very narrow screens collapse advanced controls behind a disclosure row or band drawer

Also consider these platform-friendly details:

- preserve a minimum hit area for draggable nodes
- keep knob labels readable at common zoom levels
- avoid making the graph so dense that it becomes decorative only
- keep the parameter text visible even when the graph is hidden or collapsed

#### Suggested Screen States

Your EQ UI defines these states:

- `Idle`
  - plugin is open, no special warning

- `Bypassed`
  - visually dim the graph and show the bypass state clearly

- `Analyzing`
  - analyzer overlay is active

- `Offline/Grace`
  - if the product is commercial, a non-blocking banner explains license grace or limited mode

- `Narrow Layout`
  - cards stack and the graph compresses without losing control access

That state list is helpful because it prevents the UI from becoming a one-off layout with hidden assumptions.

#### Suggested React Tree

If you are implementing the EQ in React, the component tree is:

```text
<EqEditor>
  <PluginHeader />
  <MainSurface>
    <ResponseGraph />
    <BandCardGrid>
      <BandCard band="1" />
      <BandCard band="2" />
      <BandCard band="3" />
      <BandCard band="4" />
      <BandCard band="5" />
      <BandCard band="6" />
      <BandCard band="7" />
      <BandCard band="8" />
    </BandCardGrid>
    <GlobalControls />
    <StatusStrip />
  </MainSurface>
</EqEditor>
```

Practical responsibilities for each piece:

- `EqEditor`
  - owns the overall snapshot state
  - hydrates from the `EditorModel`
  - routes parameter change messages back to Go

- `PluginHeader`
  - shows the plugin title and preset
  - hosts bypass and high-level actions

- `ResponseGraph`
  - renders the frequency response
  - receives current band values and analyzer data
  - emits drag updates for frequency/gain points

- `BandCardGrid`
  - groups the individual band cards
  - switches between grid and stacked layouts based on width

- `BandCard`
  - exposes band-specific controls
  - makes it obvious which band is being edited
  - keeps advanced controls close to the band they affect

- `GlobalControls`
  - holds output gain, oversampling, analyzer, and similar global options
  - keeps non-band settings out of the main graph area

- `StatusStrip`
  - shows sample rate, CPU hints, or license state when relevant
  - provides small, non-blocking status messages

The same tree can be expressed in plain HTML/CSS/JS. The responsibilities stay separated either way.

#### Suggested HTML/CSS Structure

The HTML shell is simple and predictable:

```html
<main class="eq-editor">
  <header class="eq-header"></header>
  <section class="eq-graph-panel"></section>
  <section class="eq-band-grid"></section>
  <section class="eq-global-panel"></section>
  <footer class="eq-status-bar"></footer>
</main>
```

Required CSS behavior:

- `eq-editor` uses a vertical grid or flex column
- `eq-graph-panel` gets the tallest portion of the screen
- `eq-band-grid` is a responsive 4-column grid on wide screens
- `eq-global-panel` stays compact and visually subordinate
- `eq-status-bar` uses small text and low visual weight

Required visual hierarchy:

- graph first
- precise knobs second
- global actions third
- status last

That hierarchy matches how people actually work on an EQ.

#### React Mock

Below is the starting point for a downstream React editor. It is intentionally simple, and the structure is what you ship first.

```tsx
type EditorControl = {
  id: number
  normalized: number
  plain: number
  name: string
}

type EditorSnapshot = {
  model: {
    sections: Array<{
      controls: EditorControl[]
    }>
  }
}

type EqBand = {
  id: number
  label: string
  type: number
  gain: number
  frequency: number
  q: number
}

type Props = {
  model: {
    plugin: {
      name: string
      vendor: string
    }
  }
  snapshot: EditorSnapshot
  onParamChange: (id: number, plainValue: number) => void
}

const BAND_PARAMS = [
  { type: 2, frequency: 3, gain: 4, q: 5 },
  { type: 6, frequency: 7, gain: 8, q: 9 },
  { type: 10, frequency: 11, gain: 12, q: 13 },
  { type: 14, frequency: 15, gain: 16, q: 17 },
  { type: 18, frequency: 19, gain: 20, q: 21 },
  { type: 22, frequency: 23, gain: 24, q: 25 },
  { type: 26, frequency: 27, gain: 28, q: 29 },
  { type: 30, frequency: 31, gain: 32, q: 33 },
]

function controlById(snapshot: EditorSnapshot, id: number): EditorControl {
  const controls = snapshot.model.sections[0]?.controls ?? []
  const control = controls.find((candidate) => candidate.id === id)
  if (!control) {
    throw new Error(`missing control ${id}`)
  }
  return control
}

function EqEditor({ model, snapshot, onParamChange }: Props) {
  const bands: EqBand[] = BAND_PARAMS.map((band, index) => ({
    id: index + 1,
    label: `Band ${index + 1}`,
    type: controlById(snapshot, band.type).plain,
    frequency: controlById(snapshot, band.frequency).plain,
    gain: controlById(snapshot, band.gain).plain,
    q: controlById(snapshot, band.q).plain,
  }))

  return (
    <main className="eq-editor">
      <header className="eq-header">
        <div className="eq-title">
          <h1>{model.plugin.name}</h1>
          <span>{model.plugin.vendor}</span>
        </div>
        <button
          className="eq-bypass"
          onClick={() => onParamChange(36, controlById(snapshot, 36).plain)}
        >
          Bypass
        </button>
      </header>

      <section className="eq-graph-panel">
        <ResponseGraph
          bands={bands}
          onBandDrag={(bandId, nextFrequency, nextGain) => {
            const band = BAND_PARAMS[bandId - 1]
            onParamChange(band.frequency, nextFrequency)
            onParamChange(band.gain, nextGain)
          }}
          onBandTypeCycle={(bandId, nextType) => {
            onParamChange(BAND_PARAMS[bandId - 1].type, nextType)
          }}
        />
      </section>

      <section className="eq-band-grid">
        {bands.map((band) => (
          <BandCard
            key={band.id}
            band={band}
            onChange={(field, value) => {
              const bandParams = BAND_PARAMS[band.id - 1]
              if (field === "gain") onParamChange(bandParams.gain, value)
              if (field === "frequency") onParamChange(bandParams.frequency, value)
              if (field === "q") onParamChange(bandParams.q, value)
              if (field === "type") onParamChange(bandParams.type, value)
            }}
          />
        ))}
      </section>

      <section className="eq-global-panel">
        <Knob label="Input" />
        <Knob label="Output" />
        <Toggle label="Analyzer" />
        <Toggle label="Oversampling" />
      </section>

      <footer className="eq-status-bar">
        <span>48 kHz</span>
        <span>Analyzer off</span>
        <span>Host sync OK</span>
      </footer>
    </main>
  )
}
```

The helper functions are downstream-specific, but the structure is the important part:

- the editor receives a model and a snapshot
- band cards are generated from the snapshot data
- user edits call back into the Go parameter bridge
- the graph and cards share the same parameter source

#### CSS Mock

Use this layout:

```css
.eq-editor {
  display: grid;
  grid-template-rows: auto minmax(240px, 1fr) auto auto;
  gap: 16px;
  padding: 16px;
  color: #f4f4f5;
  background:
    radial-gradient(circle at top, rgba(255, 255, 255, 0.08), transparent 34%),
    linear-gradient(180deg, #17171b 0%, #101114 100%);
}

.eq-header,
.eq-status-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.eq-graph-panel {
  min-height: 280px;
  border-radius: 20px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.03);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.05);
}

.eq-band-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.eq-global-panel {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.eq-status-bar {
  font-size: 12px;
  opacity: 0.72;
}

@media (max-width: 900px) {
  .eq-band-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .eq-global-panel {
    grid-template-columns: 1fr;
  }
}
```

This mock is not decorative. It is:

- legible
- responsive
- easy to wire to `EditorModel`
- easy to hydrate from Go snapshot data
- easy to extend with a real analyzer later

#### Suggested Screen Copy

If you want the EQ to feel polished, use these labels:

- `Input Gain`
- `Band 1` through `Band 8`
- `Bell`
- `Shelf`
- `Cut`
- `Output Gain`
- `Analyzer`
- `Oversampling`
- `Bypass`

If you expose more advanced controls, keep the copy human-readable:

- `Frequency` rather than `Freq`
- `Phase Invert` rather than `Invert`
- `Stereo Width` rather than `Width` if that reads better in your product

The copy makes the control obvious without forcing the user to decode the UI.

### 7.3 Decide The Filter Topology

Each band is a `pkg/dsp/filter.Biquad` (one instance per band, holding the state of every channel):

- `SetLowShelf` for a low shelf band
- `SetPeakingEQ` for a bell band
- `SetHighShelf` for a high shelf band
- `SetHighpass` / `SetLowpass` for cut bands

```go
bands := make([]*filter.Biquad, 8)
for i := range bands {
	bands[i] = filter.NewBiquad(2)
}
bands[0].SetLowShelf(sampleRate, 120, 0.707, 3)
bands[1].SetPeakingEQ(sampleRate, 1000, 1.0, -2)
```

And then, per channel buffer:

```go
for _, band := range bands {
	band.Process(output, ch)
}
```

The important part for `vst3go` is where you store the filters:

- processor fields
- reconfigured in `Initialize`
- reset in `SetActive(false)`
- driven from current parameter values during `ProcessAudio`

### 7.4 Keep Parameter Reads Cheap

In `ProcessAudio`, read values once per block unless you truly need per-sample automation.

Example:

```go
inputGain := ctx.ParamPlain(1)
lowGain := ctx.ParamPlain(2)
lowFreq := ctx.ParamPlain(3)
lowQ := ctx.ParamPlain(4)
```

If you want sample-accurate automation, use the process context’s parameter-change flow and update coefficients or targets at the relevant offsets.

### 7.5 Handle Bypass Cleanly

Bypass is a true bypass path:

```go
if ctx.Param(11) >= 0.5 {
	ctx.PassThrough()
	return
}
```

For EQs, that is the expected behavior.

### 7.6 Add State Later

If your EQ only uses parameters and no hidden state, `StatefulProcessor` is unnecessary.

If you add features like:

- analyzer freeze
- preset name
- custom band modes
- user-defined routing

then add `SaveCustomState` and `LoadCustomState`.

## 8. How To Build A Utility Plugin

An Ableton Utility-style plugin is a better example for “small but useful” host behavior.

Common Utility controls:

- gain
- pan
- width
- mono on/off
- phase invert L/R
- mute

### 8.1 Parameters

Use plain, obvious values:

```go
_ = params.Add(
	param.GainParameter(1, "Input Gain").Default(0).Build(),
	param.GainParameter(2, "Output Gain").Default(0).Build(),
	param.PanParameter(3, "Pan").Build(),
	param.MixParameter(4, "Width").Default(100).Build(),
	param.New(5, "Mono").Toggle().Build(),
	param.New(6, "Phase Invert L").Toggle().Build(),
	param.New(7, "Phase Invert R").Toggle().Build(),
	param.New(8, "Mute").Toggle().Build(),
)
```

Make `Width` a `MixParameter` or a custom `0..200%` control depending on how you define stereo expansion.

### 8.2 Processing Strategy

The processing order is:

1. apply input gain
2. optionally collapse to mono
3. apply pan
4. apply width/stereo matrix
5. apply phase inversion
6. apply output gain
7. zero the output if muted

That gives a predictable mental model for users.

### 8.3 Example Processing Shape

```go
func (p *Processor) ProcessAudio(ctx *process.Context) {
	if ctx.Param(8) >= 0.5 {
		ctx.Clear()
		return
	}

	inputGain := ctx.ParamPlain(1)
	outputGain := ctx.ParamPlain(2)
	pan := ctx.ParamPlain(3)
	width := ctx.ParamPlain(4)
	mono := ctx.Param(5) >= 0.5
	invertLeft := ctx.Param(6) >= 0.5
	invertRight := ctx.Param(7) >= 0.5

	ctx.ProcessStereo(func(ch int, input, output []float32) {
		for i := range input {
			sample := input[i]

			if ch == 0 && invertLeft {
				sample = -sample
			}
			if ch == 1 && invertRight {
				sample = -sample
			}

			sample = applyGain(sample, inputGain, 0)

			if mono {
				// Downstream implementation averages both channels.
				// This branch is just the structural idea.
			}

			if ch == 0 {
				sample = applyPanLeft(sample, pan, width)
			} else {
				sample = applyPanRight(sample, pan, width)
			}

			sample = applyGain(sample, outputGain, 0)
			output[i] = sample
		}
	})
}
```

The exact gain math is up to you. The point is to keep the plugin behavior user-facing and simple.

## 9. Editor Integration

The editor path in this repo is built on `EditorModel` and `EditorSnapshot`.

That means your plugin thinks in terms of:

- parameter metadata
- current normalized values
- plain values for display
- hidden/read-only semantics

### 9.1 What The Editor Receives

The editor model is built from the parameter registry and plugin metadata.

Your job is to make sure the registry is clean:

- correct IDs
- correct names
- correct min/max/default values
- correct flags
- sensible short names and units

### 9.2 What The Editor Should Not Do

The editor never invents its own state source.

Instead:

- the host edits parameters
- the runtime updates the registry
- the editor mirrors that registry
- the snapshot can be saved and restored

### 9.3 Hidden And Read-Only Controls

Use these flags deliberately:

- `IsHidden` for internal or unsupported controls
- `IsReadOnly` for meters or values that display but are not edited
- `CanAutomate` for values the host writes to

If you add a meter, make it read-only and not automatable.

## 10. Saving And Restoring State

If parameter values are enough, do not add custom state.

If not, implement `StatefulProcessor`:

```go
type Processor struct {
	params *param.Registry
	buses  *bus.Configuration

	presetName string
	lastMode   int32
}

func (p *Processor) SaveCustomState(w io.Writer) error {
	// write custom fields after parameter state
	return nil
}

func (p *Processor) LoadCustomState(r io.Reader) error {
	// restore custom fields after parameter state
	return nil
}
```

Use custom state for things like:

- preset names
- mode selections that are not parameterized
- internal routing choices
- analysis display settings

Do not use custom state to duplicate regular parameter values unless you have a strong reason.

## 11. A Recommended File Layout For A Downstream Plugin Repo

Start the repo with `vst3go new` (see the README quick start): it writes a
working effect, instrument (`-type instrument`) or MIDI effect (`-type midi`)
module with offline tests, and `go tool vst3go build -install . <Name>`
builds and installs every format from it. As the plugin grows, a good layout
is:

```text
my-plugin/
  cmd/
  internal/
    dsp/
    ui/
  plugin/
    processor.go
    plugin.go
    state.go
  web/
    editor/
      index.html
      assets/
  justfile
```

Suggested division:

- `plugin/processor.go` owns the runtime processor
- `plugin/plugin.go` owns the `Plugin` entrypoint and metadata
- `plugin/state.go` owns custom state when parameter values are not enough
- `internal/dsp/` owns plugin-specific signal flow built from `pkg/dsp` blocks
- `web/editor/` owns the browser-rendered UI if you generate one

Keep `vst3go` itself as the runtime dependency, not the product workspace.

## 12. How To Think About A Real Plugin Project

When you start a new plugin, answer these questions in order:

1. What is the user trying to do?
2. What are the 5 to 12 controls that actually matter?
3. Is this mono, stereo, sidechain, or multibus?
4. What is the processing topology?
5. What state must survive reload?
6. What must the editor show immediately?
7. What needs to be automatable?
8. What is hidden?

That sequence defines the first version. Do not add extra branches until those eight answers are fixed.

## 13. Suggested Development Order

For a new plugin, do the work in this order and do not skip ahead:

1. define plugin metadata
2. define buses
3. define parameters
4. implement processor lifecycle
5. implement the basic audio algorithm
6. add state save/restore if needed
7. expose the editor model
8. connect the editor shell
9. test with `go test ./...` (`pkg/plugintest` renders the plugin offline)
10. build and install with `go tool vst3go build -install . <Name>` and open it in a host
11. run Windows validation if Windows is in scope

If you complete steps 1 through 11, you have a single downstream plugin that can be built, opened, edited, saved, restored, and validated.

## 14. A Practical Example Workflow

Here is the workflow for a downstream repo:

1. Start with a stereo utility plugin.
2. Verify gain, pan, width, and mute in the editor.
3. Add state save/restore for the last selected mode.
4. Turn the utility into an `Eight EQ`-style plugin by swapping the DSP layer.
5. Keep the parameter contract stable while the backend changes.
6. Reuse the same web editor shell and only change the data fed into it.

That sequence produces one coherent plugin instead of a collection of unrelated experiments.

## 15. What To Avoid

- do not put product-specific logic (licensing, branding, installers) into `vst3go`; generic DSP goes into `pkg/dsp`
- do not treat the browser editor as a separate app
- do not bypass the parameter registry to keep extra state
- do not allocate in `ProcessAudio` unless you have measured and accepted the tradeoff
- do not make platform glue own business logic
- do not hide important user controls behind undocumented behavior

### 15.1 Measuring And Tuning Performance

- Benchmark the processor with `go test -bench` on `plugintest` or
  `plugin.NewOffline`, and keep `plugintest.AllocsPerProcess` at zero.
- `go run ./internal/rtcheck` runs a processing chain on a real-time thread
  while other goroutines allocate, and reports call times against the
  block's budget.
- Profile-guided optimization: a CPU profile of the processor under load,
  saved as `default.pgo` in the plugin's main package directory (run
  `go test -bench . -cpuprofile default.pgo` there), is used by every
  `go build`, `vst3go build` included; DSP chains typically gain a few
  percent.
- Prefer `pkg/dsp` blocks where they fit: they convert dB with `math.Exp`
  rather than `math.Pow`, use radix-4 FFTs and non-uniformly partitioned
  convolution, and are benchmarked in the repository.

## 16. Validation Checklist For A New Plugin

Before you call a plugin “done”, make sure:

- it loads in the host
- it processes audio without crash or glitch
- its parameters reflect correctly in the editor
- its state survives save and reload
- its bus layout matches the intended use case
- its realtime path is allocation-conscious
- its build and packaging story are documented

If Windows is in scope, also verify:

- the DLL builds
- the bundle layout is correct
- the editor opens in WebView2
- repeated reopen cycles are stable
- the plugin survives host reloads and rescans

## 17. Closing Advice

The best `vst3go` plugin code is very boring in the right places:

- metadata is explicit
- parameters are named carefully
- buses are obvious
- processing is deterministic
- state is intentional
- the editor mirrors the runtime instead of replacing it

If you keep those rules, you can build anything from a straightforward EQ to a polished Utility-style plugin without fighting the framework.

# Simple Synth

A 16-voice sine synthesizer built on the vst3go DSP library and the framework voice allocator.

## Features

- 16-voice polyphony with oldest-voice stealing (`pkg/framework/voice`)
- ADSR amplitude envelope per voice (`pkg/dsp/envelope`)
- Sample-accurate notes: voices are rendered between events, so every note starts and stops on its sample offset
- Sustain pedal (CC 64), All Notes Off (CC 123) and All Sound Off (CC 120)
- Smoothed master volume
- No allocation on the audio thread; voices and scratch buffers are created in `Initialize`

## Parameters

| Parameter | Range | Default |
| --- | --- | --- |
| Attack | 0.001 - 2 s | 0.01 s |
| Decay | 0.001 - 2 s | 0.1 s |
| Sustain | 0 - 100 % | 70 % |
| Release | 0.001 - 5 s | 0.3 s |
| Volume | 0 - 100 % | 80 % |

Attack, decay and release are the time constants of exponential segments.

## Files

- `main.go`: plugin info, parameters, MIDI handling and the mix
- `voice.go`: `synthVoice`, the oscillator and envelope behind the `voice.Voice` interface
- `main_test.go`: offline tests for timing, tuning, release, the sustain pedal and allocation

## Build and test

```bash
scripts/build_vst3.sh examples/simplesynth SimpleSynth
scripts/build_standalone.sh examples/simplesynth SimpleSynth
go test ./examples/simplesynth
```

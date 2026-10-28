// Package bus provides VST3 audio bus configuration and management.
package bus

import (
	"fmt"
)

// Builder provides a fluent API for building bus configurations
type Builder struct {
	config *Configuration
	errors []error
}

// NewBuilder creates a new bus configuration builder
func NewBuilder() *Builder {
	return &Builder{
		config: &Configuration{
			audioBuses: []Info{},
			eventBuses: []Info{},
		},
		errors: []error{},
	}
}

// WithAudioInput adds an audio input bus
func (b *Builder) WithAudioInput(name string, channels int32) *Builder {
	b.config.audioBuses = append(b.config.audioBuses, Info{
		MediaType:    MediaTypeAudio,
		Direction:    DirectionInput,
		ChannelCount: channels,
		Arrangement:  ArrangementForChannels(channels),
		Name:         name,
		BusType:      TypeMain,
		IsActive:     true,
	})
	return b
}

// WithAudioOutput adds an audio output bus
func (b *Builder) WithAudioOutput(name string, channels int32) *Builder {
	b.config.audioBuses = append(b.config.audioBuses, Info{
		MediaType:    MediaTypeAudio,
		Direction:    DirectionOutput,
		ChannelCount: channels,
		Arrangement:  ArrangementForChannels(channels),
		Name:         name,
		BusType:      TypeMain,
		IsActive:     true,
	})
	return b
}

// WithAuxInput adds an auxiliary audio input bus (e.g., sidechain)
func (b *Builder) WithAuxInput(name string, channels int32) *Builder {
	b.config.audioBuses = append(b.config.audioBuses, Info{
		MediaType:    MediaTypeAudio,
		Direction:    DirectionInput,
		ChannelCount: channels,
		Arrangement:  ArrangementForChannels(channels),
		Name:         name,
		BusType:      TypeAux,
		IsActive:     false, // Aux buses start inactive by default
	})
	return b
}

// WithAuxOutput adds an auxiliary audio output bus
func (b *Builder) WithAuxOutput(name string, channels int32) *Builder {
	b.config.audioBuses = append(b.config.audioBuses, Info{
		MediaType:    MediaTypeAudio,
		Direction:    DirectionOutput,
		ChannelCount: channels,
		Arrangement:  ArrangementForChannels(channels),
		Name:         name,
		BusType:      TypeAux,
		IsActive:     false, // Aux buses start inactive by default
	})
	return b
}

// WithAudioInputArrangement adds a main audio input bus with an explicit speaker arrangement.
func (b *Builder) WithAudioInputArrangement(name string, arrangement SpeakerArrangement) *Builder {
	b.WithAudioInput(name, arrangement.ChannelCount())
	b.config.audioBuses[len(b.config.audioBuses)-1].Arrangement = arrangement
	return b
}

// WithAudioOutputArrangement adds a main audio output bus with an explicit speaker arrangement.
func (b *Builder) WithAudioOutputArrangement(name string, arrangement SpeakerArrangement) *Builder {
	b.WithAudioOutput(name, arrangement.ChannelCount())
	b.config.audioBuses[len(b.config.audioBuses)-1].Arrangement = arrangement
	return b
}

// WithEventInput adds an event (MIDI) input bus
func (b *Builder) WithEventInput(name string) *Builder {
	b.config.eventBuses = append(b.config.eventBuses, Info{
		MediaType:    MediaTypeEvent,
		Direction:    DirectionInput,
		ChannelCount: 1,
		Name:         name,
		BusType:      TypeMain,
		IsActive:     true,
	})
	return b
}

// WithEventOutput adds an event (MIDI) output bus
func (b *Builder) WithEventOutput(name string) *Builder {
	b.config.eventBuses = append(b.config.eventBuses, Info{
		MediaType:    MediaTypeEvent,
		Direction:    DirectionOutput,
		ChannelCount: 1,
		Name:         name,
		BusType:      TypeMain,
		IsActive:     true,
	})
	return b
}

// WithStereoInput is a convenience method for adding stereo input
func (b *Builder) WithStereoInput(name string) *Builder {
	return b.WithAudioInput(name, 2)
}

// WithStereoOutput is a convenience method for adding stereo output
func (b *Builder) WithStereoOutput(name string) *Builder {
	return b.WithAudioOutput(name, 2)
}

// WithMonoInput is a convenience method for adding mono input
func (b *Builder) WithMonoInput(name string) *Builder {
	return b.WithAudioInput(name, 1)
}

// WithMonoOutput is a convenience method for adding mono output
func (b *Builder) WithMonoOutput(name string) *Builder {
	return b.WithAudioOutput(name, 1)
}

// WithSidechain adds a sidechain input bus (auxiliary stereo input)
func (b *Builder) WithSidechain(name string) *Builder {
	return b.WithAuxInput(name, 2)
}

// WithQuadInput adds a quadraphonic (4-channel) input
func (b *Builder) WithQuadInput(name string) *Builder {
	return b.WithAudioInput(name, 4)
}

// WithQuadOutput adds a quadraphonic (4-channel) output
func (b *Builder) WithQuadOutput(name string) *Builder {
	return b.WithAudioOutput(name, 4)
}

// With5_1Input adds a 5.1 surround input
func (b *Builder) With5_1Input(name string) *Builder {
	return b.WithAudioInputArrangement(name, Arrangement51)
}

// With5_1Output adds a 5.1 surround output
func (b *Builder) With5_1Output(name string) *Builder {
	return b.WithAudioOutputArrangement(name, Arrangement51)
}

// With7_1Input adds a 7.1 surround input
func (b *Builder) With7_1Input(name string) *Builder {
	return b.WithAudioInputArrangement(name, Arrangement71Cine)
}

// With7_1Output adds a 7.1 surround output
func (b *Builder) With7_1Output(name string) *Builder {
	return b.WithAudioOutputArrangement(name, Arrangement71Cine)
}

// SetBusActive sets a specific bus as active/inactive
func (b *Builder) SetBusActive(mediaType MediaType, direction Direction, index int32, active bool) *Builder {
	buses := b.config.audioBuses
	if mediaType == MediaTypeEvent {
		buses = b.config.eventBuses
	}

	busIndex := int32(0)
	for i := range buses {
		if buses[i].Direction == direction {
			if busIndex == index {
				buses[i].IsActive = active
				return b
			}
			busIndex++
		}
	}

	b.errors = append(b.errors, fmt.Errorf("bus not found: mediaType=%d, direction=%d, index=%d", mediaType, direction, index))
	return b
}

// Validate checks if the configuration is valid
func (b *Builder) Validate() error {
	// Check for errors accumulated during building
	if len(b.errors) > 0 {
		return fmt.Errorf("builder errors: %v", b.errors)
	}

	// Check that we have at least one main output bus (audio or event)
	hasMainOutput := false
	for _, bus := range b.config.audioBuses {
		if bus.Direction == DirectionOutput && bus.BusType == TypeMain {
			hasMainOutput = true
			break
		}
	}

	// MIDI effects might only have event buses
	if !hasMainOutput && len(b.config.eventBuses) > 0 {
		for _, bus := range b.config.eventBuses {
			if bus.Direction == DirectionOutput && bus.BusType == TypeMain {
				hasMainOutput = true
				break
			}
		}
	}

	if !hasMainOutput {
		return fmt.Errorf("configuration must have at least one main output bus (audio or event)")
	}

	// Hosts treat bus 0 of a direction as the main bus whenever one exists,
	// so main buses must precede auxiliary buses.
	for _, direction := range []Direction{DirectionInput, DirectionOutput} {
		seenAux := false
		for _, bus := range b.config.audioBuses {
			if bus.Direction != direction {
				continue
			}
			if bus.BusType == TypeAux {
				seenAux = true
				continue
			}
			if seenAux {
				return fmt.Errorf("main audio bus %q of direction %d must precede auxiliary buses", bus.Name, direction)
			}
		}
	}

	// Validate channel counts
	for _, bus := range b.config.audioBuses {
		if bus.ChannelCount <= 0 {
			return fmt.Errorf("invalid channel count %d for bus %s", bus.ChannelCount, bus.Name)
		}
		if bus.ChannelCount > 32 {
			return fmt.Errorf("channel count %d exceeds maximum of 32 for bus %s", bus.ChannelCount, bus.Name)
		}
		if bus.SpeakerArrangement().ChannelCount() != bus.ChannelCount {
			return fmt.Errorf("speaker arrangement of bus %s has %d channels, want %d", bus.Name, bus.SpeakerArrangement().ChannelCount(), bus.ChannelCount)
		}
	}

	return nil
}

// Build returns the built configuration or an error
func (b *Builder) Build() (*Configuration, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return b.config, nil
}

// MustBuild returns the built configuration or panics on error
func (b *Builder) MustBuild() *Configuration {
	config, err := b.Build()
	if err != nil {
		panic(err)
	}
	return config
}

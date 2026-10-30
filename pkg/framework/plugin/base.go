// Package plugin provides framework-side metadata and convenience types for
// plugin authors. These types support, but do not replace, the runtime-facing
// contracts in pkg/plugin.
package plugin

import (
	"github.com/th-release/vst3go/v2/pkg/framework/param"
	"github.com/th-release/vst3go/v2/pkg/framework/state"
)

// Base provides shared metadata/parameter/state storage for plugin authors.
//
// It is a convenience type and is not required by the runtime wrapper. The
// actual runtime entrypoint remains pkg/plugin.Plugin.
type Base struct {
	Info   Info
	params *param.Registry
	state  *state.Manager
}

// NewBase creates a new plugin base
func NewBase(info *Info) *Base {
	b := &Base{
		Info:   *info,
		params: param.NewRegistry(),
	}

	// Initialize state manager with parameter registry
	b.state = state.NewManager(b.params)

	return b
}

// Parameters returns the parameter registry for configuration
func (b *Base) Parameters() *param.Registry {
	return b.params
}

// AudioProcessor is a legacy minimal audio-only processing contract used by
// helper layers that do not need the full runtime Processor interface.
//
// Prefer pkg/plugin.Processor for new runtime-facing code.
type AudioProcessor interface {
	// ProcessAudio processes audio buffers - zero allocations allowed!
	ProcessAudio(input, output [][]float32)
}

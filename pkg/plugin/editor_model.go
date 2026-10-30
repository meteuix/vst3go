package plugin

import (
	"fmt"

	"github.com/th-release/vst3go/pkg/framework/param"
	frameworkplugin "github.com/th-release/vst3go/pkg/framework/plugin"
)

// EditorModel is the browser-friendly snapshot of the plugin editor.
//
// The web renderer can consume this structure directly to build controls,
// display the current layout, and keep values in sync with the processor.
type EditorModel struct {
	Plugin   EditorPluginInfo `json:"plugin"`
	Sections []EditorSection  `json:"sections"`
}

// EditorSnapshot is a serializable capture of the current editor state.
//
// The snapshot currently mirrors the live model so the web editor can save and
// restore current control values without inventing a separate persistence layer.
type EditorSnapshot struct {
	Model EditorModel `json:"model"`
}

// EditorPluginInfo mirrors the metadata the editor needs for display.
type EditorPluginInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Vendor   string `json:"vendor"`
	Category string `json:"category"`
}

// EditorSection groups related controls for the renderer.
type EditorSection struct {
	Title    string          `json:"title"`
	Controls []EditorControl `json:"controls"`
}

// EditorControl describes a single parameter control in the editor surface.
type EditorControl struct {
	ID           uint32  `json:"id"`
	Name         string  `json:"name"`
	ShortName    string  `json:"shortName"`
	Unit         string  `json:"unit"`
	Kind         string  `json:"kind"`
	Normalized   float64 `json:"normalized"`
	Plain        float64 `json:"plain"`
	Text         string  `json:"text"` // the value as the parameter formats it
	Min          float64 `json:"min"`
	Max          float64 `json:"max"`
	DefaultValue float64 `json:"defaultValue"` // normalized
	Scale        string  `json:"scale"`        // "linear", "log" or "power"
	Exponent     float64 `json:"exponent"`     // of the "power" scale
	StepCount    int32   `json:"stepCount"`
	Flags        uint32  `json:"flags"`
	ReadOnly     bool    `json:"readOnly"`
	Hidden       bool    `json:"hidden"`
}

// scaleName names a parameter scale for the editor SDK.
func scaleName(s param.Scale) string {
	switch s {
	case param.ScaleLog:
		return "log"
	case param.ScalePower:
		return "power"
	default:
		return "linear"
	}
}

// BuildEditorModel turns plugin metadata and parameters into a renderable
// browser-friendly snapshot.
func BuildEditorModel(info frameworkplugin.Info, registry *param.Registry) (*EditorModel, error) {
	if registry == nil {
		return nil, fmt.Errorf("build editor model: nil registry")
	}

	model := &EditorModel{
		Plugin: EditorPluginInfo{
			ID:       info.ID,
			Name:     info.Name,
			Version:  info.Version,
			Vendor:   info.Vendor,
			Category: info.Category,
		},
	}

	controls := registry.All()
	section := EditorSection{
		Title:    "Parameters",
		Controls: make([]EditorControl, 0, len(controls)),
	}

	for _, p := range controls {
		if p == nil {
			continue
		}

		normalized := p.GetNormalized()
		control := EditorControl{
			ID:           p.ID,
			Name:         p.Name,
			ShortName:    p.ShortName,
			Unit:         p.Unit,
			Kind:         inferControlKind(p),
			Normalized:   normalized,
			Plain:        p.Denormalize(normalized),
			Text:         p.FormatValue(normalized),
			Min:          p.Min,
			Max:          p.Max,
			DefaultValue: p.DefaultValue,
			Scale:        scaleName(p.Scale),
			Exponent:     p.Exponent,
			StepCount:    p.StepCount,
			Flags:        p.Flags,
			ReadOnly:     p.Flags&param.IsReadOnly != 0,
			Hidden:       p.Flags&param.IsHidden != 0,
		}
		section.Controls = append(section.Controls, control)
	}

	model.Sections = []EditorSection{section}
	return model, nil
}

// BuildEditorSnapshot creates a stateful snapshot of the current editor model.
func BuildEditorSnapshot(info frameworkplugin.Info, registry *param.Registry) (*EditorSnapshot, error) {
	model, err := BuildEditorModel(info, registry)
	if err != nil {
		return nil, err
	}

	return &EditorSnapshot{Model: *model}, nil
}

// Apply writes the snapshot's parameter values back into the registry.
func (s *EditorSnapshot) Apply(registry *param.Registry) error {
	if registry == nil {
		return fmt.Errorf("apply editor snapshot: nil registry")
	}

	if s == nil {
		return fmt.Errorf("apply editor snapshot: nil snapshot")
	}

	for _, section := range s.Model.Sections {
		for _, control := range section.Controls {
			if paramEntry, ok := registry.GetOK(control.ID); ok {
				paramEntry.SetNormalized(control.Normalized)
			}
		}
	}

	return nil
}

func inferControlKind(p *param.Parameter) string {
	switch {
	case p == nil:
		return "unknown"
	case p.Flags&param.IsHidden != 0:
		return "hidden"
	case p.Flags&param.IsReadOnly != 0:
		return "meter"
	case p.Flags&param.IsBypass != 0:
		return "toggle"
	case p.Flags&param.IsList != 0 || p.StepCount > 0:
		return "choice"
	default:
		return "slider"
	}
}

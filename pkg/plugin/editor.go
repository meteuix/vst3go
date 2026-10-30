package plugin

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/meteuix/vst3go/v2/pkg/framework/param"
)

// Editor describes the web editor of a processor. Processors implement
// EditorProcessor to replace the built-in parameter editor with their own
// page, or only to set its size.
type Editor struct {
	// HTML is the editor page as one document, with its scripts and styles
	// inline (a Vite build with vite-plugin-singlefile, for example). The
	// vst3go SDK (window.vst3go, see docs/web-editor-bridge.md) is injected
	// at the start of <head>, before the page's own scripts. Empty uses the
	// built-in editor.
	HTML string
	// Width and Height are the initial size in logical pixels; 0 uses
	// 900x640.
	Width, Height int
	// MinWidth, MinHeight, MaxWidth and MaxHeight bound resizing by the
	// host and by vst3go.resize; 0 is unbounded.
	MinWidth, MinHeight, MaxWidth, MaxHeight int
	// FixedSize keeps the editor at Width x Height.
	FixedSize bool
}

// EditorProcessor provides the editor of a processor.
type EditorProcessor interface {
	Editor() Editor
}

// EditorMessageProcessor receives the messages an editor page sends with
// vst3go.send(name, data), on the main thread. Replies go back with
// Host.PostEditorMessage. In a DistributedPlugin the editor belongs to the
// controller side, so its processor instance receives them.
type EditorMessageProcessor interface {
	EditorMessage(name string, data json.RawMessage)
}

const (
	defaultEditorWidth  = 900
	defaultEditorHeight = 640
)

// editorSpec returns the editor of a processor with defaults applied.
func editorSpec(processor Processor) Editor {
	var e Editor
	if ep, ok := processor.(EditorProcessor); ok {
		e = ep.Editor()
	}
	if e.Width <= 0 {
		e.Width = defaultEditorWidth
	}
	if e.Height <= 0 {
		e.Height = defaultEditorHeight
	}
	if e.FixedSize {
		e.MinWidth, e.MaxWidth = e.Width, e.Width
		e.MinHeight, e.MaxHeight = e.Height, e.Height
	}
	e.Width, e.Height = e.constrain(e.Width, e.Height)
	return e
}

// constrain clamps a size to the editor's bounds.
func (e Editor) constrain(width, height int) (int, int) {
	return clampSize(width, e.MinWidth, e.MaxWidth), clampSize(height, e.MinHeight, e.MaxHeight)
}

func clampSize(v, lo, hi int) int {
	if hi > 0 && v > hi {
		v = hi
	}
	if v < lo {
		v = lo
	}
	if v < 1 {
		v = 1
	}
	return v
}

// editorBootstrap is the data the SDK starts from (window.__vst3goBootstrap).
type editorBootstrap struct {
	Model  EditorModel      `json:"model"`
	Editor editorSizeLimits `json:"editor"`
}

type editorSizeLimits struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	MinWidth  int  `json:"minWidth"`
	MinHeight int  `json:"minHeight"`
	MaxWidth  int  `json:"maxWidth"`
	MaxHeight int  `json:"maxHeight"`
	Resizable bool `json:"resizable"`
}

// editorPage builds the HTML of an editor: the processor's page or the
// built-in one, with the bootstrap data and the SDK injected.
func (c *componentImpl) editorPage() (string, error) {
	model, err := c.EditorModel()
	if err != nil {
		return "", err
	}
	e := c.editor
	data, err := json.Marshal(editorBootstrap{
		Model: *model,
		Editor: editorSizeLimits{
			Width: e.Width, Height: e.Height,
			MinWidth: e.MinWidth, MinHeight: e.MinHeight, MaxWidth: e.MaxWidth, MaxHeight: e.MaxHeight,
			Resizable: !e.FixedSize,
		},
	})
	if err != nil {
		return "", err
	}
	page := e.HTML
	if page == "" {
		page = builtinEditorHTML()
	}
	return injectEditorSDK(page, data), nil
}

var headTag = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)

// injectEditorSDK puts the bootstrap data and the SDK at the start of the
// page's <head> (or of the page), so they run before the page's scripts.
func injectEditorSDK(page string, bootstrap []byte) string {
	// "</" cannot appear in the JSON inside the script element.
	data := strings.ReplaceAll(string(bootstrap), "</", `<\/`)
	scripts := "<script>window.__vst3goBootstrap = " + data + ";</script>\n<script>\n" + editorSDK() + "\n</script>\n"
	if loc := headTag.FindStringIndex(page); loc != nil {
		return page[:loc[1]] + "\n" + scripts + page[loc[1]:]
	}
	return scripts + page
}

// editorParamMessage tells the page that a parameter changed.
type editorParamMessage struct {
	Type       string  `json:"type"` // "param-change"
	ID         uint32  `json:"id"`
	Normalized float64 `json:"normalized"`
	Plain      float64 `json:"plain"`
	Text       string  `json:"text"`
}

func paramMessage(p *param.Parameter) editorParamMessage {
	normalized := p.GetNormalized()
	return editorParamMessage{Type: "param-change", ID: p.ID, Normalized: normalized, Plain: p.Denormalize(normalized), Text: p.FormatValue(normalized)}
}

// editorCustomMessage carries Host.PostEditorMessage data to the page
// (vst3go.on(name, ...)).
type editorCustomMessage struct {
	Type string          `json:"type"` // "message"
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

func customEditorMessage(name string, data any) ([]byte, error) {
	if name == "" {
		return nil, errInvalidArgument
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(editorCustomMessage{Type: "message", Name: name, Data: raw})
}

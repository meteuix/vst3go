# Web Editor Bridge

The plugin editor is a web page rendered by the platform web view and embedded
into the host's editor window. Go owns the data model; the page renders it
through the vst3go JavaScript SDK (`window.vst3go`), which speaks JSON
messages with the plugin.

## Rendering Flow

1. `pkg/plugin/component.go` builds an `EditorModel` from the live parameter registry.
2. `editorPage` (`pkg/plugin/editor.go`) takes the processor's page (`Editor.HTML`) or the built-in one and injects, at the start of `<head>`, the bootstrap data (`window.__vst3goBootstrap`: the model and the editor size limits) and the SDK (`editor_assets/vst3go.js`). `GoEditControllerGetEditorHTML` returns that page.
3. The platform view loads the HTML: `editor_view_darwin.c` into `WKWebView` on macOS, `editor_view_windows.c` into WebView2 on Windows, and `editor_view_linux.c` into WebKitGTK on Linux (build tag `webkitgtk`, embedded into the host's X11 window).
4. User input flows back as JSON messages; plugin-side changes reach the page as JSON messages too.

The views are VST3 `IPlugView` objects. CLAP instances use them too: `pkg/plugin/clap_gui.c` drives a view from the CLAP `gui` extension, and `editor_session.go` holds the per-instance page state (view, messages, preset browser) for both formats.

## Custom Editors

A processor replaces the built-in editor by implementing `EditorProcessor`:

```go
//go:embed ui/dist/index.html
var editorHTML string

func (p *Processor) Editor() plugin.Editor {
	return plugin.Editor{
		HTML:  editorHTML, // one document, scripts and styles inline
		Width: 640, Height: 400,
		MinWidth: 480, MinHeight: 320, MaxWidth: 1200, MaxHeight: 900,
	}
}
```

- `HTML` is one self-contained document (e.g. a Vite build with
  `vite-plugin-singlefile`); the SDK runs before the page's scripts. Empty
  `HTML` keeps the built-in editor, so `Editor` can only set the size.
- `Width`/`Height` are the initial size (default 900x640). The min/max fields
  bound host resizing (`checkSizeConstraint`, CLAP `adjust_size`) and
  `vst3go.resize`; 0 is unbounded. `FixedSize` turns resizing off.
- `EditorMessageProcessor.EditorMessage(name, data)` receives
  `vst3go.send(name, data)` on the main thread; replies go back with
  `Host.PostEditorMessage(name, data)` (from `HostAwareProcessor.SetHost`),
  which the page receives with `vst3go.on(name, ...)`. In a
  `DistributedPlugin` the editor belongs to the controller side, and a
  processor-side `PostEditorMessage` is forwarded to it with the internal
  `vst3go.editor` message. `plugin.Offline` records the posted messages
  (`TakeEditorMessages`) and delivers page messages (`EditorMessage`) for tests.

`internal/validation/synth` is the reference: `editor.html` (range input,
keyboard-driven knob, choice buttons, program select, a status line polled
with `vst3go.send("status")`, and a `vst3go.resize` at startup).

## JavaScript SDK

`window.vst3go` (declarations: `editor_assets/vst3go.d.ts`):

| Member | Meaning |
| --- | --- |
| `version` | `1` |
| `plugin` | id, name, version, vendor, category |
| `params`, `param(idOrName)`, `sections` | live parameters in registry order, by ID or name, grouped as in the model |
| `onParamChange(fn)` | any parameter change, from the host, the plugin or the page; returns the unsubscribe function |
| `presets` | `list`, `current`, `error`, `refresh()`, `save(name)`, `load(name)`, `onChange(fn)` (the user `.vstpreset` folder) |
| `send(name, data)` / `on(name, fn)` | custom messages with the processor |
| `size`, `resize(w, h)` | initial size and limits; ask the host for a new size (clamped to the limits) |

A parameter (`Vst3goParam`) carries its `id`, `name`, `unit`, `kind`, plain
`min`/`max`, `defaultValue` (plain) and `defaultNormalized`, `stepCount`,
`readOnly`, `hidden`, and its current `normalized`, `plain` and `text` (the
plugin's formatter). Edits:

- `beginEdit()` / `endEdit()` bracket a drag, so the host records one
  automation pass and one undo step; they nest.
- `setNormalized(v)` / `setPlain(v)` edit the value; outside a gesture they are
  a complete edit (begin, change, end). `reset()` sets the default.
- `bindGesture(element)` turns pointer drags on an element into gestures.
- `onChange(fn)` follows one parameter.

### Typed Bindings

`plugin.EditorBindings(p)` generates a TypeScript module with a `params` table
keyed by camel-cased parameter names (ID, range, kind, plain default, step
count; hidden parameters left out), a `ParamKey` type and `param(key)`
returning the live `Vst3goParam`, so a misspelled parameter does not compile.
`plugin.WriteEditorBindings(p, dir)` writes it as `params.ts` next to
`vst3go.d.ts`. In the plugin's tests, `plugintest.EditorBindings(t, p, dir)`
fails when the files are stale; `VST3GO_UPDATE_BINDINGS=1 go test` rewrites
them. `internal/validation/synth/ui` holds the synth's bindings and
`check.ts`, which type-checks typical editor code against them.

## Messages

The SDK wraps this protocol; pages without the SDK can speak it directly.

Page to plugin: JSON objects posted to `window.webkit.messageHandlers.vst3go`
(macOS, Linux) or `window.chrome.webview` (Windows). Every platform view hands
the JSON to `GoEditorMessage`.

| Message | Effect |
| --- | --- |
| `{ type: "begin-edit", id }` / `{ type: "end-edit", id }` | bracket a drag: the host records one automation pass and one undo step |
| `{ type: "param-change", id, value }` | set the normalized `value`; outside a gesture it is a complete edit |
| `{ type: "preset-list" }` | ask for the preset list |
| `{ type: "preset-save", name }` | save the current state as `name` in the user preset folder (`"Bass/Deep"` uses a subfolder) |
| `{ type: "preset-load", name }` | load a preset from the user preset folder |
| `{ type: "resize", width, height }` | ask the host for a new size (VST3 `IPlugFrame::resizeView`, CLAP `request_resize`), clamped to the limits |
| `{ type: "message", name, data }` | `EditorMessageProcessor.EditorMessage(name, data)` |

Plugin to page: `VST3GoEditorViewPost` calls `window.__vst3goReceive(message)`
(on Windows through `PostWebMessageAsJson` and the `message` event).

| Message | Meaning |
| --- | --- |
| `{ type: "param-change", id, normalized, plain, text }` | a parameter changed (host automation, preset, plugin edit) |
| `{ type: "presets", presets: [names], current, error }` | the user presets after a preset message; `error` explains a failed save/load |
| `{ type: "message", name, data }` | `Host.PostEditorMessage(name, data)` |

## Built-in Editor

`pkg/plugin/editor_assets/` holds the built-in page (`index.html`,
`editor.css`, `editor.js`), embedded into the plugin binary. `editor.js` is
written on the SDK: one control per visible parameter by kind, a preset bar,
and gestures on the sliders.

## State

- **Plugin state** is the parameter registry plus `StatefulProcessor` data, persisted by the host through `getState`/`setState` and in `.vstpreset` files (`pkg/preset`).
- **The editor snapshot** is the bootstrap copy of the registry the page starts from; `EditorSnapshot.Apply` restores snapshot values into a registry.

## Checks

- `scripts/check_editor_sdk.sh` runs the SDK in a fake page under node
  (`hostcheck/sdkcheck.mjs`), checks that the synth's bindings are current and
  type-checks the declarations, the bindings and `check.ts` with `tsc`.
- `scripts/check_macos_editor.sh` and `scripts/docker_check_linux_editor.sh`
  open the gain (built-in) and synth (custom) editors in real windows, as
  VST3 and CLAP. On macOS `editorcheck_darwin.m` drives the SDK in the page:
  an edit gesture that must reach the plugin, a host change that must reach
  the SDK's value, text and listeners, the synth's startup resize, clamping to
  its limits and a `send`/`on` round trip with the processor. On Linux
  `editorcheck.c` edits with XTest keyboard input and checks the startup
  resize.

## Why This Shape

- Go owns the plugin data model.
- Platform code only hosts the browser renderer and forwards JSON.
- The page speaks one SDK on every platform and format, so the same UI runs everywhere.

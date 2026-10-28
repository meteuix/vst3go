# Web Editor Bridge

The plugin editor is a web page rendered by the platform web view and embedded
into the host's editor window. Go owns the data model; the page only renders
it and exchanges JSON messages with the plugin.

## Rendering Flow

1. `pkg/plugin/component.go` builds an `EditorModel` from the live parameter registry.
2. `pkg/plugin/wrapper_controller.go` serializes the model as an `EditorSnapshot` into the HTML bootstrap returned by `GoEditControllerGetEditorHTML`.
3. The platform view loads the HTML: `editor_view_darwin.c` into `WKWebView` on macOS, `editor_view_windows.c` into WebView2 on Windows, and `editor_view_linux.c` into WebKitGTK on Linux (build tag `webkitgtk`, embedded into the host's X11 window).
4. User input flows back as JSON messages; plugin-side changes reach the page as JSON messages too.

## Messages

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

Plugin to page: `VST3GoEditorViewPost` calls `window.__vst3goReceive(message)`
(on Windows through `PostWebMessageAsJson` and the `message` event).

| Message | Meaning |
| --- | --- |
| `{ type: "param-change", id, normalized, plain }` | a parameter changed (host automation, preset, plugin edit) |
| `{ type: "presets", presets: [names], current, error }` | the user presets after a preset message; `error` explains a failed save/load |

## HTML, CSS, And JS

`pkg/plugin/editor_assets/` holds the default shell (`index.html`,
`editor.css`, `editor.js`), embedded into the plugin binary. The JS renders one
control per visible parameter, a preset bar, and speaks the messages above.

A custom UI (React, Svelte, plain HTML) replaces those assets and keeps the
same contract:

- read the initial `EditorSnapshot` from the bootstrap,
- post `param-change` (wrapped in `begin-edit`/`end-edit` for drags) and
  the preset messages,
- handle `window.__vst3goReceive`.

## State

- **Plugin state** is the parameter registry plus `StatefulProcessor` data, persisted by the host through `getState`/`setState` and in `.vstpreset` files (`pkg/preset`).
- **The editor snapshot** is the bootstrap copy of the registry the page starts from; `EditorSnapshot.Apply` restores snapshot values into a registry.

## Why This Shape

- Go owns the plugin data model.
- Platform code only hosts the browser renderer and forwards JSON.
- The page only speaks the snapshot and the messages above, so the same UI runs on every platform.

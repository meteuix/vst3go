package plugin

import (
	"embed"
	"strings"
)

//go:embed editor_assets/index.html editor_assets/editor.css editor_assets/editor.js editor_assets/vst3go.js editor_assets/vst3go.d.ts
var editorAssets embed.FS

func loadEditorAsset(path string) string {
	data, err := editorAssets.ReadFile(path)
	if err != nil {
		panic("vst3go: missing editor asset " + path)
	}
	return string(data)
}

// editorSDK is the editor SDK script (window.vst3go).
func editorSDK() string { return loadEditorAsset("editor_assets/vst3go.js") }

// EditorSDKTypes returns the TypeScript declarations of the editor SDK
// (window.vst3go) for custom editor pages.
func EditorSDKTypes() string { return loadEditorAsset("editor_assets/vst3go.d.ts") }

// builtinEditorHTML is the built-in editor: one control per parameter and
// a preset bar, written against the SDK.
func builtinEditorHTML() string {
	html := loadEditorAsset("editor_assets/index.html")
	html = strings.ReplaceAll(html, "__VST3GO_CSS__", loadEditorAsset("editor_assets/editor.css"))
	return strings.ReplaceAll(html, "__VST3GO_JS__", loadEditorAsset("editor_assets/editor.js"))
}

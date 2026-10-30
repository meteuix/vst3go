package plugintest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/th-release/vst3go/v2/pkg/plugin"
)

// UpdateBindingsEnv is the environment variable that makes EditorBindings
// write the bindings instead of comparing them.
const UpdateBindingsEnv = "VST3GO_UPDATE_BINDINGS"

// EditorBindings checks that dir holds the current editor SDK declarations
// and typed parameter bindings of p (plugin.WriteEditorBindings), so a
// custom editor's TypeScript breaks at compile time when parameters change.
// With VST3GO_UPDATE_BINDINGS=1 it writes them instead:
//
//	func TestEditorBindings(t *testing.T) { plugintest.EditorBindings(t, myPlugin{}, "ui/src") }
func EditorBindings(t testing.TB, p plugin.Plugin, dir string) {
	t.Helper()
	if os.Getenv(UpdateBindingsEnv) == "1" {
		if err := plugin.WriteEditorBindings(p, dir); err != nil {
			t.Fatalf("plugintest: write editor bindings: %v", err)
		}
		return
	}
	want := t.TempDir()
	if err := plugin.WriteEditorBindings(p, want); err != nil {
		t.Fatalf("plugintest: editor bindings: %v", err)
	}
	for _, name := range []string{"vst3go.d.ts", "params.ts"} {
		expected, err := os.ReadFile(filepath.Join(want, name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, expected) {
			t.Errorf("plugintest: %s is not the current editor binding of %s; run the test with %s=1 to update it",
				filepath.Join(dir, name), p.GetInfo().Name, UpdateBindingsEnv)
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestGoflagsTags(t *testing.T) {
	got := goflagsTags("-mod=mod -tags=webkitgtk,foo --tags=bar -trimpath")
	if !slices.Equal(got, []string{"webkitgtk", "foo", "bar"}) {
		t.Fatalf("tags %v", got)
	}
	if got := goflagsTags(""); len(got) != 0 {
		t.Fatalf("tags of empty GOFLAGS: %v", got)
	}
}

func TestRenderEveryKind(t *testing.T) {
	for _, kind := range kinds {
		p := project{Name: "My Plug", Kind: kind, ID: "com.example.myplug", Vendor: "Acme", Module: "example.com/myplug", Replace: "/src/vst3go", VST3GoVersion: "v2.0.0-00010101000000-000000000000"}
		files, err := p.render()
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		main := string(files["main.go"])
		for _, want := range []string{`ID:       "com.example.myplug"`, `Name:     "My Plug"`, `Vendor:   "Acme"`, "func main() { standalone.Main() }", "plugin.Register("} {
			if !strings.Contains(main, want) {
				t.Errorf("%s main.go lacks %q", kind, want)
			}
		}
		mod := string(files["go.mod"])
		for _, want := range []string{"module example.com/myplug", "replace github.com/meteuix/vst3go/v2 => /src/vst3go", "tool github.com/meteuix/vst3go/v2/cmd/vst3go"} {
			if !strings.Contains(mod, want) {
				t.Errorf("%s go.mod lacks %q:\n%s", kind, want, mod)
			}
		}
		if !strings.Contains(string(files["main_test.go"]), "plugintest.New") {
			t.Errorf("%s main_test.go does not use plugintest", kind)
		}
	}
}

func TestGoModWithoutVersion(t *testing.T) {
	files, err := project{Name: "X", Kind: "effect", ID: "a.b", Vendor: "V", Module: "x"}.render()
	if err != nil {
		t.Fatal(err)
	}
	if mod := string(files["go.mod"]); strings.Contains(mod, "require") || strings.Contains(mod, "replace") {
		t.Fatalf("go.mod pins a version it does not know:\n%s", mod)
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"-type", "reverb", filepath.Join(dir, "a"), "Name"},
		{filepath.Join(dir, "b"), "1Name"},
		{"-vendor", `Bad "Vendor"`, filepath.Join(dir, "c"), "Name"},
	} {
		if err := runNew(append([]string{"-tidy=false"}, args...)); err == nil {
			t.Errorf("new %v accepted", args)
		}
	}
	full := filepath.Join(dir, "full")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runNew([]string{"-tidy=false", full, "Name"}); err == nil {
		t.Error("new into a non-empty folder accepted")
	}
}

func TestNewWritesModule(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My-Plug")
	if err := runNew([]string{"-tidy=false", "-type", "instrument", dir, "My Plug"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "main.go", "main_test.go", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Error(err)
		}
	}
	mod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if !strings.HasPrefix(string(mod), "module my-plug\n") {
		t.Errorf("default module path:\n%s", mod)
	}
	main, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(main), `"com.example.myplug"`) {
		t.Error("default plugin ID")
	}
}

func TestAUViewClass(t *testing.T) {
	a := (&buildConfig{name: "My Plug", id: "com.example.a"}).auViewClass()
	b := (&buildConfig{name: "My Plug", id: "com.example.b"}).auViewClass()
	if a == b || !regexp.MustCompile(`^VST3GoAUView_My_Plug_[0-9a-f]{8}$`).MatchString(a) {
		t.Fatalf("view classes %q %q", a, b)
	}
}

package main

import (
	"bytes"
	"embed"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"text/template"
)

//go:embed templates
var templates embed.FS

// project is the data of the plugin templates.
type project struct {
	Name, Kind, ID, Vendor, Module string
	VST3GoVersion, Replace         string
}

var kinds = []string{"effect", "instrument", "midi"}

func runNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `usage: vst3go new [flags] <dir> <Name>

Creates a plugin module in dir: main.go (the plugin, built as VST3, CLAP,
Audio Unit and standalone application), main_test.go (offline tests with
pkg/plugintest) and go.mod with vst3go as a dependency and tool:

  cd <dir> && go test ./... && go tool vst3go build -install . <Name>

`)
		fs.PrintDefaults()
	}
	kind := fs.String("type", "effect", "plugin type: "+strings.Join(kinds, ", "))
	module := fs.String("module", "", "module path (default: the folder name)")
	vendor := fs.String("vendor", "My Company", "vendor name shown by hosts")
	id := fs.String("id", "", "plugin ID, reverse domain (default com.example.<name>)")
	replace := fs.String("replace", "", "use the vst3go checkout in this folder (replace directive)")
	tidy := fs.Bool("tidy", true, "run go mod tidy in the new module")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return flag.ErrHelp
	}
	dir, name := fs.Arg(0), fs.Arg(1)
	p := project{Name: name, Kind: *kind, Vendor: *vendor, ID: *id, Module: *module}
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _-]*$`).MatchString(name) {
		return fmt.Errorf("name %q: start with a letter, then letters, digits, space, '_' or '-'", name)
	}
	if !validKind(p.Kind) {
		return fmt.Errorf("unknown type %q (want %s)", p.Kind, strings.Join(kinds, ", "))
	}
	slug := strings.ToLower(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(name, ""))
	if p.ID == "" {
		p.ID = "com.example." + slug
	}
	if p.Module == "" {
		p.Module = strings.ToLower(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(filepath.Base(dir), "-"))
	}
	if strings.ContainsAny(p.Vendor+p.ID, "\"\\\n") {
		return errors.New("vendor and ID must not contain quotes, backslashes or newlines")
	}
	if *replace != "" {
		abs, err := filepath.Abs(*replace)
		if err != nil {
			return err
		}
		p.Replace = abs
	}
	p.VST3GoVersion = vst3goVersion(p.Replace != "")

	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", dir)
	}
	files, err := p.render()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"go.mod", "main.go", "main_test.go", ".gitignore"} {
		if err := os.WriteFile(filepath.Join(dir, name), files[name], 0o644); err != nil {
			return err
		}
	}
	if *tidy {
		cmd := exec.Command("go", "mod", "tidy")
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go mod tidy: %w", err)
		}
	}
	fmt.Printf("created %s (%s %q)\n", dir, p.Kind, p.Name)
	return nil
}

func validKind(kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// render executes the templates of the project's kind.
func (p project) render() (map[string][]byte, error) {
	files := map[string][]byte{}
	for name, source := range map[string]string{
		"go.mod":       "templates/go.mod.tmpl",
		"main.go":      "templates/" + p.Kind + ".go.tmpl",
		"main_test.go": "templates/" + p.Kind + "_test.go.tmpl",
		".gitignore":   "templates/gitignore.tmpl",
	} {
		t, err := template.ParseFS(templates, source)
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := t.Execute(&b, p); err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		data := b.Bytes()
		if strings.HasSuffix(name, ".go") {
			if data, err = format.Source(data); err != nil {
				return nil, fmt.Errorf("%s: %w", source, err)
			}
		}
		files[name] = data
	}
	return files, nil
}

// vst3goVersion is the vst3go version the new module requires: the one this
// command was built from, a placeholder under a replace directive, or empty
// for go mod tidy to pick the latest release.
func vst3goVersion(replaced bool) string {
	if replaced {
		return "v0.0.0-00010101000000-000000000000"
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	version := ""
	if info.Main.Path == modulePath {
		version = info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			version = dep.Version
		}
	}
	if version == "(devel)" {
		return ""
	}
	return version
}

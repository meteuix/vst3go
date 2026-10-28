package preset

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Preset is a preset file in a preset folder.
type Preset struct {
	// Name is the path relative to the folder without the extension, with
	// "/" between subfolders: "Bass/Fat Saw".
	Name string
	Path string
}

// UserDir returns the per-user preset folder the VST3 specification assigns
// to a plugin, where hosts look for its presets:
//
//	macOS    ~/Library/Audio/Presets/<vendor>/<plugin>
//	Windows  %USERPROFILE%\Documents\VST3 Presets\<vendor>\<plugin>
//	Linux    ~/.vst3/presets/<vendor>/<plugin>
func UserDir(vendor, plugin string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if err := checkSegment(vendor); err != nil {
		return "", fmt.Errorf("preset folder vendor: %w", err)
	}
	if err := checkSegment(plugin); err != nil {
		return "", fmt.Errorf("preset folder plugin name: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Audio", "Presets", vendor, plugin), nil
	case "windows":
		return filepath.Join(home, "Documents", "VST3 Presets", vendor, plugin), nil
	default:
		return filepath.Join(home, ".vst3", "presets", vendor, plugin), nil
	}
}

// List returns the presets in dir and its subfolders, sorted by name. A
// missing folder has no presets.
func List(dir string) ([]Preset, error) {
	var presets []Preset
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), Extension) || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(strings.TrimSuffix(rel, filepath.Ext(rel)))
		presets = append(presets, Preset{Name: name, Path: path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(presets, func(a, b Preset) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return presets, nil
}

// PathFor returns the file path of a preset name in dir. Names may use "/"
// for subfolders; they must not leave dir or use characters that are
// invalid in file names on any platform.
func PathFor(dir, name string) (string, error) {
	segments := strings.Split(name, "/")
	for _, s := range segments {
		if err := checkSegment(s); err != nil {
			return "", fmt.Errorf("preset name %q: %w", name, err)
		}
	}
	return filepath.Join(append([]string{dir}, segments...)...) + Extension, nil
}

// Save writes f as the preset name in dir, creating subfolders.
func Save(dir, name string, f *File) (string, error) {
	path, err := PathFor(dir, name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, WriteFile(path, f)
}

func checkSegment(s string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return errors.New("empty name")
	case s == "." || s == "..":
		return errors.New("relative path")
	case strings.ContainsAny(s, `<>:"/\|?*`) || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 }):
		return errors.New(`contains one of <>:"/\|?* or a control character`)
	case strings.HasSuffix(s, ".") || strings.HasSuffix(s, " "):
		return errors.New("ends with a dot or space")
	}
	return nil
}

func dirOf(path string) string { return filepath.Dir(path) }

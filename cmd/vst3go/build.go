package main

import (
	"bytes"
	"crypto/sha1" // #nosec G505 - names an Objective-C class, not a security use
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const modulePath = "github.com/th-release/vst3go/v2"

// buildConfig is one vst3go build invocation.
type buildConfig struct {
	pkg, name string
	out       string
	formats   []string
	tags      []string
	install   bool
	id        string // bundle identifier
	version   string
	platform  string // AUv3 platform
	identity  string // code signing identity on Apple platforms

	goos, goarch string
	vst3goDir    string // module root: scripts/*.c and include/
	tmp          string
}

// allFormats in build order: a standalone application is built before
// the AUv3 extension it contains.
var allFormats = []string{"vst3", "clap", "au", "lv2", "app", "auv3"}

func defaultFormats(goos string) string {
	if goos == "darwin" {
		return "vst3,clap,au"
	}
	return "vst3,clap,lv2"
}

func runBuild(args []string) error {
	goos, goarch, err := goEnv()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `usage: vst3go build [flags] <package> <Name>

Builds the plugin package as the requested formats into the output folder:
  vst3   <Name>.vst3 bundle with moduleinfo.json
  clap   <Name>.clap (bundle on macOS, shared object on Linux)
  au     <Name>.component Audio Unit bundle (macOS)
  lv2    <Name>.lv2 bundle with its Turtle files (Linux)
  app    standalone application (<Name>.app on macOS, executable on Linux;
         main must call standalone.Main)
  auv3   AUv3 extension <Name>.appex in <Name>.app: in the standalone
         application when app is built too, else in a minimal container
         application; -platform ios or ios-simulator builds it for iOS
Tags in GOFLAGS are kept. Windows builds: scripts/docker_build_windows.sh.

`)
		fs.PrintDefaults()
	}
	formats := fs.String("format", defaultFormats(goos), "comma-separated formats: "+strings.Join(allFormats, ", "))
	out := fs.String("o", "dist", "output folder")
	tags := fs.String("tags", "", "extra comma-separated build tags (e.g. webkitgtk for the Linux editor)")
	install := fs.Bool("install", false, "copy the plugins into the user plugin folders (~/Library/Audio/Plug-Ins, ~/.vst3, ~/.clap, ~/.lv2)")
	id := fs.String("id", "", "bundle identifier (default com.vst3go.<name>)")
	version := fs.String("version", "1.0.0", "bundle version")
	platform := fs.String("platform", platformMacOS, "platform of the auv3 format: "+strings.Join(allPlatforms, ", "))
	identity := fs.String("sign", "-", "code signing identity on Apple platforms (- signs ad hoc)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return flag.ErrHelp
	}
	c := &buildConfig{
		pkg: fs.Arg(0), name: fs.Arg(1), out: *out, install: *install, version: *version,
		platform: *platform, identity: *identity, goos: goos, goarch: goarch,
	}
	if !slices.Contains(allPlatforms, c.platform) {
		return fmt.Errorf("unknown platform %q (want %s)", c.platform, strings.Join(allPlatforms, ", "))
	}
	if c.platform != platformMacOS {
		set := false
		fs.Visit(func(f *flag.Flag) { set = set || f.Name == "format" })
		if !set {
			*formats = "auv3"
		} else if *formats != "auv3" {
			return fmt.Errorf("-platform %s builds the auv3 format only", c.platform)
		}
		if *install {
			return fmt.Errorf("-install does not install %s builds: use xcrun simctl install or Xcode", c.platform)
		}
	}
	if !strings.HasPrefix(c.pkg, ".") && !filepath.IsAbs(c.pkg) {
		// A folder of the current module, not an import path.
		if info, err := os.Stat(c.pkg); err == nil && info.IsDir() {
			c.pkg = "./" + c.pkg
		}
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]*$`).MatchString(c.name) {
		return fmt.Errorf("name %q: use letters, digits, space, '_', '.' or '-'", c.name)
	}
	c.id = *id
	if c.id == "" {
		c.id = "com.vst3go." + strings.Trim(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(c.name, "-"), "-")
	}
	for _, f := range strings.Split(*formats, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !slices.Contains(allFormats, f) {
			return fmt.Errorf("unknown format %q (want %s)", f, strings.Join(allFormats, ", "))
		}
		if (f == "au" || f == "auv3") && goos != "darwin" {
			return errors.New("Audio Units build on macOS only")
		}
		if f == "lv2" && goos != "linux" {
			return errors.New("LV2 plugins build on Linux only")
		}
		if !slices.Contains(c.formats, f) {
			c.formats = append(c.formats, f)
		}
	}
	if len(c.formats) == 0 {
		return errors.New("no formats")
	}
	slices.SortFunc(c.formats, func(a, b string) int { return slices.Index(allFormats, a) - slices.Index(allFormats, b) })
	if goos != "darwin" && goos != "linux" {
		return fmt.Errorf("vst3go build runs on macOS and Linux; Windows builds use scripts/docker_build_windows.sh")
	}
	c.tags = append(goflagsTags(os.Getenv("GOFLAGS")), splitList(*tags)...)
	if c.out, err = filepath.Abs(c.out); err != nil {
		return err
	}
	if c.vst3goDir, err = moduleDir(); err != nil {
		return err
	}
	if c.tmp, err = os.MkdirTemp("", "vst3go-build-"); err != nil {
		return err
	}
	defer os.RemoveAll(c.tmp)
	return c.build()
}

func (c *buildConfig) build() error {
	if err := os.MkdirAll(c.out, 0o755); err != nil {
		return err
	}
	var built []string
	var lib, app string
	for _, format := range c.formats {
		if format != "app" && lib == "" {
			lib = filepath.Join(c.tmp, "lib"+c.name)
			if err := c.goBuild(lib, true); err != nil {
				return err
			}
		}
		var path string
		var err error
		switch format {
		case "vst3":
			path, err = c.vst3(lib)
		case "clap":
			path, err = c.clap(lib)
		case "au":
			path, err = c.au(lib)
		case "lv2":
			path, err = c.lv2(lib)
		case "app":
			path, err = c.app()
			app = path
		case "auv3":
			path, err = c.auv3(lib, app)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", format, err)
		}
		if c.goos == "darwin" {
			if err := c.sign(path); err != nil {
				return fmt.Errorf("%s: %w", format, err)
			}
		}
		fmt.Println("built", path)
		if !slices.Contains(built, path) {
			built = append(built, path)
		}
	}
	if c.install {
		return c.installAll(built)
	}
	return nil
}

// goBuild builds the plugin package as a c-shared library or, for the
// standalone application, an executable with the standalone tag.
func (c *buildConfig) goBuild(output string, shared bool) error {
	tags := slices.Clone(c.tags)
	args := []string{"build", "-trimpath", "-o", output}
	if shared {
		args = append(args, "-buildmode=c-shared")
	} else {
		tags = append([]string{"standalone"}, tags...)
	}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, c.pkg)
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	if c.goos == "darwin" {
		cmd.Env = append(cmd.Env, "CGO_CFLAGS="+c.cgoCFlags())
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build %s: %w", c.pkg, err)
	}
	return nil
}

// cgoCFlags are CGO_CFLAGS with the name of the Audio Unit editor's view
// factory: an Objective-C class looked up by name in the bundle, named
// after the bundle so several vst3go plugins can share a host process.
func (c *buildConfig) cgoCFlags() string {
	cflags := os.Getenv("CGO_CFLAGS")
	if cflags == "" {
		cflags = "-O2 -g"
	}
	return cflags + " -DVST3GO_AU_VIEW_CLASS=" + c.auViewClass()
}

func (c *buildConfig) auViewClass() string {
	sum := sha1.Sum([]byte(c.id)) // #nosec G401
	return "VST3GoAUView_" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(c.name, "_") + "_" + hex.EncodeToString(sum[:4])
}

func (c *buildConfig) vst3(lib string) (string, error) {
	bundle := filepath.Join(c.out, c.name+".vst3")
	var binary string
	switch c.goos {
	case "darwin":
		binary = filepath.Join(bundle, "Contents", "MacOS", c.name)
	default:
		arch := map[string]string{"amd64": "x86_64-linux", "arm64": "aarch64-linux"}[c.goarch]
		if arch == "" {
			return "", fmt.Errorf("unsupported architecture %s", c.goarch)
		}
		binary = filepath.Join(bundle, "Contents", arch, c.name+".so")
	}
	if err := c.freshBundle(bundle, binary, lib); err != nil {
		return "", err
	}
	if c.goos == "darwin" {
		if err := c.bundlePlist(bundle, c.id+".vst3", "BNDL", nil); err != nil {
			return "", err
		}
	}
	// moduleinfo.json lets hosts scan the bundle without loading the binary.
	tool, err := c.tool("moduleinfo")
	if err != nil {
		return "", err
	}
	info, err := output(tool, binary, c.name)
	if err != nil {
		return "", err
	}
	resources := filepath.Join(bundle, "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		return "", err
	}
	return bundle, os.WriteFile(filepath.Join(resources, "moduleinfo.json"), info, 0o644)
}

func (c *buildConfig) clap(lib string) (string, error) {
	if c.goos != "darwin" {
		path := filepath.Join(c.out, c.name+".clap")
		return path, copyFile(lib, path, 0o755)
	}
	bundle := filepath.Join(c.out, c.name+".clap")
	if err := c.freshBundle(bundle, filepath.Join(bundle, "Contents", "MacOS", c.name), lib); err != nil {
		return "", err
	}
	return bundle, c.bundlePlist(bundle, c.id+".clap", "BNDL", nil)
}

func (c *buildConfig) au(lib string) (string, error) {
	bundle := filepath.Join(c.out, c.name+".component")
	binary := filepath.Join(bundle, "Contents", "MacOS", c.name)
	if err := c.freshBundle(bundle, binary, lib); err != nil {
		return "", err
	}
	tool, err := c.tool("auinfo")
	if err != nil {
		return "", err
	}
	components, err := output(tool, binary)
	if err != nil {
		return "", err
	}
	extra := []string{"<key>LSMinimumSystemVersion</key><string>10.13</string>", string(components)}
	if err := c.bundlePlist(bundle, c.id+".component", "BNDL", extra); err != nil {
		return "", err
	}
	if _, err := output("plutil", "-lint", filepath.Join(bundle, "Contents", "Info.plist")); err != nil {
		return "", err
	}
	return bundle, os.MkdirAll(filepath.Join(bundle, "Contents", "Resources"), 0o755)
}

func (c *buildConfig) lv2(lib string) (string, error) {
	bundle := filepath.Join(c.out, c.name+".lv2")
	binary := filepath.Join(bundle, c.name+".so")
	if err := c.freshBundle(bundle, binary, lib); err != nil {
		return "", err
	}
	tool, err := c.tool("lv2info")
	if err != nil {
		return "", err
	}
	_, err = output(tool, binary, bundle)
	return bundle, err
}

func (c *buildConfig) app() (string, error) {
	if c.goos != "darwin" {
		path := filepath.Join(c.out, c.name)
		return path, c.goBuild(path, false)
	}
	bundle := filepath.Join(c.out, c.name+".app")
	binary := filepath.Join(bundle, "Contents", "MacOS", c.name)
	if err := os.RemoveAll(bundle); err != nil {
		return "", err
	}
	if err := c.goBuild(binary, false); err != nil {
		return "", err
	}
	extra := []string{
		"<key>NSHighResolutionCapable</key><true/>",
		"<key>NSMicrophoneUsageDescription</key><string>" + xmlEscape(c.name) + " processes the audio input.</string>",
	}
	return bundle, c.bundlePlist(bundle, c.id, "APPL", extra)
}

// freshBundle recreates bundle with the library as its binary.
func (c *buildConfig) freshBundle(bundle, binary, lib string) error {
	if err := os.RemoveAll(bundle); err != nil {
		return err
	}
	return copyFile(lib, binary, 0o755)
}

func (c *buildConfig) bundlePlist(bundle, id, kind string, extra []string) error {
	return c.writePlist(filepath.Join(bundle, "Contents"), id, kind, extra)
}

// writePlist writes Info.plist and PkgInfo into contents, the Contents
// folder of a macOS bundle or an iOS bundle itself.
func (c *buildConfig) writePlist(contents, id, kind string, extra []string) error {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	for _, kv := range [][2]string{
		{"CFBundleDevelopmentRegion", "English"},
		{"CFBundleDisplayName", c.name},
		{"CFBundleExecutable", c.name},
		{"CFBundleIdentifier", id},
		{"CFBundleInfoDictionaryVersion", "6.0"},
		{"CFBundleName", c.name},
		{"CFBundlePackageType", kind},
		{"CFBundleShortVersionString", c.version},
		{"CFBundleSignature", "????"},
		{"CFBundleVersion", c.version},
	} {
		fmt.Fprintf(&b, "  <key>%s</key><string>%s</string>\n", kv[0], xmlEscape(kv[1]))
	}
	for _, e := range extra {
		b.WriteString(e)
		if !strings.HasSuffix(e, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("</dict>\n</plist>\n")
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(contents, "PkgInfo"), []byte(kind+"????"), 0o644)
}

// tool compiles one of the vst3go helper programs (scripts/<name>.c), which
// load the built binary and describe it.
func (c *buildConfig) tool(name string) (string, error) {
	path := filepath.Join(c.tmp, name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}
	args := []string{"-O1", "-Wall", "-I" + filepath.Join(c.vst3goDir, "include"), filepath.Join(c.vst3goDir, "scripts", name+".c"), "-o", path}
	if c.goos == "linux" {
		args = append(args, "-ldl")
	}
	if _, err := output(cc, args...); err != nil {
		return "", err
	}
	return path, nil
}

func (c *buildConfig) installAll(built []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	registrar := false
	var extensions []string
	for _, path := range built {
		var dir string
		appex := filepath.Join(path, "Contents", "PlugIns", c.name+".appex")
		switch {
		case strings.HasSuffix(path, ".app") && slices.Contains(c.formats, "auv3"):
			// macOS registers the extensions of applications in
			// ~/Applications; pluginkit registers this one now.
			dir = filepath.Join(home, "Applications")
			extensions = append(extensions, filepath.Join(dir, filepath.Base(path), "Contents", "PlugIns", filepath.Base(appex)))
		case strings.HasSuffix(path, ".vst3"):
			dir = filepath.Join(home, ".vst3")
			if c.goos == "darwin" {
				dir = filepath.Join(home, "Library", "Audio", "Plug-Ins", "VST3")
			}
		case strings.HasSuffix(path, ".clap"):
			dir = filepath.Join(home, ".clap")
			if c.goos == "darwin" {
				dir = filepath.Join(home, "Library", "Audio", "Plug-Ins", "CLAP")
			}
		case strings.HasSuffix(path, ".lv2"):
			dir = filepath.Join(home, ".lv2")
		case strings.HasSuffix(path, ".component"):
			dir = filepath.Join(home, "Library", "Audio", "Plug-Ins", "Components")
			registrar = true
		default:
			continue // applications run from the output folder
		}
		target := filepath.Join(dir, filepath.Base(path))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := copyTree(path, target); err != nil {
			return err
		}
		fmt.Println("installed", target)
	}
	for _, appex := range extensions {
		if _, err := output("pluginkit", "-a", appex); err != nil {
			return err
		}
	}
	if registrar || len(extensions) > 0 {
		// The registrar caches the component list.
		_ = exec.Command("killall", "-9", "AudioComponentRegistrar").Run()
	}
	return nil
}

func (c *buildConfig) sign(path string) error {
	_, err := output("codesign", "--force", "--sign", c.identity, path)
	return err
}

func goEnv() (goos, goarch string, err error) {
	out, err := output("go", "env", "GOOS", "GOARCH")
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return "", "", fmt.Errorf("go env: %q", out)
	}
	return fields[0], fields[1], nil
}

// moduleDir finds the vst3go module the current module builds against.
func moduleDir() (string, error) {
	out, err := output("go", "list", "-m", "-f", "{{.Dir}}", modulePath)
	if err != nil {
		return "", fmt.Errorf("find the %s module (run vst3go in a module that requires it): %w", modulePath, err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("module %s is not downloaded (go mod download)", modulePath)
	}
	return dir, nil
}

// goflagsTags returns the tags of -tags flags in GOFLAGS, which an explicit
// -tags would otherwise replace.
func goflagsTags(goflags string) []string {
	var tags []string
	for _, f := range strings.Fields(goflags) {
		for _, prefix := range []string{"-tags=", "--tags="} {
			if v, ok := strings.CutPrefix(f, prefix); ok {
				tags = append(tags, splitList(v)...)
			}
		}
	}
	return tags
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

func output(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

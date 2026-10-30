package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Apple platforms of AUv3 extensions (-platform).
const (
	platformMacOS        = "macos"
	platformIOS          = "ios"
	platformIOSSimulator = "ios-simulator"
)

var allPlatforms = []string{platformMacOS, platformIOS, platformIOSSimulator}

// Minimum OS versions of AUv3 extensions: the extension reaches the Audio
// Unit behind AUAudioUnitV2Bridge through its audioUnit property.
const (
	auv3MacOSVersion = "11.0"
	auv3IOSVersion   = "14.0"
)

// appleTarget is the SDK and compiler target of an AUv3 build.
type appleTarget struct {
	sdk          string // xcrun SDK
	triple       string // clang -target
	goos, goarch string
	platformName string // CFBundleSupportedPlatforms, "" on macOS
	ios          bool   // flat iOS bundles
}

func (c *buildConfig) appleTarget() (appleTarget, error) {
	arch := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[c.goarch]
	if arch == "" {
		return appleTarget{}, fmt.Errorf("unsupported architecture %s", c.goarch)
	}
	switch c.platform {
	case platformIOS:
		return appleTarget{"iphoneos", "arm64-apple-ios" + auv3IOSVersion, "ios", "arm64", "iPhoneOS", true}, nil
	case platformIOSSimulator:
		return appleTarget{"iphonesimulator", arch + "-apple-ios" + auv3IOSVersion + "-simulator", "ios", c.goarch, "iPhoneSimulator", true}, nil
	}
	return appleTarget{"macosx", arch + "-apple-macos" + auv3MacOSVersion, "darwin", c.goarch, "", false}, nil
}

// compiler returns the target's clang and its flags.
func (t appleTarget) compiler() (string, []string, error) {
	clang, err := output("xcrun", "--sdk", t.sdk, "--find", "clang")
	if err != nil {
		return "", nil, err
	}
	sdk, err := output("xcrun", "--sdk", t.sdk, "--show-sdk-path")
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(string(clang)), []string{"-isysroot", strings.TrimSpace(string(sdk)), "-target", t.triple}, nil
}

// auv3 builds the AUv3 extension of the plugin package into a container
// application: the standalone application when this build made one
// (macOS), else a minimal one. lib is the plugin built for this Mac, which
// describes the components.
func (c *buildConfig) auv3(lib, app string) (string, error) {
	t, err := c.appleTarget()
	if err != nil {
		return "", err
	}
	clang, flags, err := t.compiler()
	if err != nil {
		return "", err
	}
	tool, err := c.tool("auinfo")
	if err != nil {
		return "", err
	}
	components, err := output(tool, lib)
	if err != nil {
		return "", err
	}
	archive := filepath.Join(c.tmp, "lib"+c.name+"-auv3.a")
	ldflags, err := c.goArchive(t, clang, flags, archive)
	if err != nil {
		return "", err
	}

	container := app
	if container == "" {
		if container, err = c.auv3Container(t, clang, flags); err != nil {
			return "", err
		}
	}
	plugins := filepath.Join(container, "Contents", "PlugIns")
	if t.ios {
		plugins = filepath.Join(container, "PlugIns")
	}
	appex := filepath.Join(plugins, c.name+".appex")
	contents, binary := filepath.Join(appex, "Contents"), filepath.Join(appex, "Contents", "MacOS", c.name)
	if t.ios {
		contents, binary = appex, filepath.Join(appex, c.name)
	}
	if err := os.RemoveAll(appex); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		return "", err
	}
	// The extension's executable starts in NSExtensionMain, which runs the
	// principal class; force_load keeps the archive's Objective-C classes,
	// which nothing references.
	args := append(slices.Clone(flags), "-fapplication-extension", "-e", "_NSExtensionMain",
		"-Wl,-force_load,"+archive, "-framework", "Foundation", "-o", binary)
	if _, err := output(clang, append(args, ldflags...)...); err != nil {
		return "", err
	}
	extension := []string{
		"<key>NSExtension</key>\n<dict>\n<key>NSExtensionAttributes</key>\n<dict>\n" +
			auv3Components(string(components)) + "</dict>\n" +
			"<key>NSExtensionPointIdentifier</key><string>com.apple.AudioUnit-UI</string>\n" +
			"<key>NSExtensionPrincipalClass</key><string>" + c.auViewClass() + "Extension</string>\n</dict>",
	}
	if err := c.writePlist(contents, c.id+".auv3", "XPC!", append(extension, t.plistKeys(false)...)); err != nil {
		return "", err
	}
	if _, err := output("plutil", "-lint", filepath.Join(contents, "Info.plist")); err != nil {
		return "", err
	}
	if err := c.signExtension(t, appex); err != nil {
		return "", err
	}
	return container, nil
}

// auv3Components is the AudioComponents entry of the Audio Unit bundle
// without its factory function: the extension's principal class creates
// the audio units.
func auv3Components(components string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(components, "\n") {
		if !strings.Contains(line, "<key>factoryFunction</key>") {
			b.WriteString(line)
		}
	}
	return b.String()
}

// plistKeys are the platform keys of an Info.plist.
func (t appleTarget) plistKeys(app bool) []string {
	if !t.ios {
		return []string{"<key>LSMinimumSystemVersion</key><string>" + auv3MacOSVersion + "</string>"}
	}
	keys := []string{
		"<key>MinimumOSVersion</key><string>" + auv3IOSVersion + "</string>",
		"<key>CFBundleSupportedPlatforms</key><array><string>" + t.platformName + "</string></array>",
		"<key>UIDeviceFamily</key><array><integer>1</integer><integer>2</integer></array>",
	}
	if app {
		keys = append(keys, "<key>LSRequiresIPhoneOS</key><true/>", "<key>UILaunchScreen</key><dict/>")
	}
	return keys
}

// goArchive builds the plugin package with the AUv3 front end as a static
// library for the target and returns the linker flags its cgo packages
// need.
func (c *buildConfig) goArchive(t appleTarget, clang string, flags []string, archive string) ([]string, error) {
	tags := strings.Join(append(slices.Clone(c.tags), "auv3"), ",")
	env := append(os.Environ(),
		"GOOS="+t.goos, "GOARCH="+t.goarch, "CGO_ENABLED=1", "CC="+clang,
		"CGO_CFLAGS="+c.cgoCFlags()+" "+strings.Join(flags, " "),
		"CGO_LDFLAGS="+strings.Join(flags, " "),
	)
	cmd := exec.Command("go", "build", "-trimpath", "-buildmode=c-archive", "-tags", tags, "-o", archive, c.pkg)
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go build %s for %s: %w", c.pkg, c.platform, err)
	}
	list := exec.Command("go", "list", "-deps", "-tags", tags, "-f", `{{join .CgoLDFLAGS "\n"}}`, c.pkg)
	list.Env = env
	out, err := list.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w", c.pkg, err)
	}
	return strings.Fields(string(out)), nil
}

// auv3Container builds the minimal container application.
func (c *buildConfig) auv3Container(t appleTarget, clang string, flags []string) (string, error) {
	bundle := filepath.Join(c.out, c.name+".app")
	contents, binary := filepath.Join(bundle, "Contents"), filepath.Join(bundle, "Contents", "MacOS", c.name)
	ui := "Cocoa"
	if t.ios {
		contents, binary, ui = bundle, filepath.Join(bundle, c.name), "UIKit"
	}
	if err := os.RemoveAll(bundle); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		return "", err
	}
	args := append(slices.Clone(flags), "-x", "objective-c", filepath.Join(c.vst3goDir, "scripts", "auv3container.m"),
		"-framework", "Foundation", "-framework", ui, "-o", binary)
	if _, err := output(clang, args...); err != nil {
		return "", err
	}
	return bundle, c.writePlist(contents, c.id, "APPL", t.plistKeys(true))
}

// signExtension signs the extension (sandboxed on macOS, as extensions
// must be, reading the files the user picks in vst3go.chooseFile) with the
// build's identity; build signs the container after.
func (c *buildConfig) signExtension(t appleTarget, appex string) error {
	args := []string{"--force", "--sign", c.identity}
	if !t.ios {
		entitlements := filepath.Join(c.tmp, "auv3.entitlements")
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>com.apple.security.app-sandbox</key><true/>
  <key>com.apple.security.files.user-selected.read-only</key><true/>
</dict>
</plist>
`
		if err := os.WriteFile(entitlements, []byte(plist), 0o644); err != nil {
			return err
		}
		args = append(args, "--entitlements", entitlements)
	}
	_, err := output("codesign", append(args, appex)...)
	return err
}

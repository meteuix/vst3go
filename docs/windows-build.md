# Windows Build Notes

This repo now has a Windows editor-view scaffold and a `GetPluginFactory` export path, but Windows packaging still needs the host toolchain and bundle layout to be wired by the consumer build.

For the real-host validation checklist and required evidence, see `docs/windows-validation.md`.

## Required Pieces

- A Windows CGO toolchain
- The Microsoft WebView2 SDK headers and loader library
- The WebView2 runtime installed on the target machine
- On non-Windows hosts, the build script preflights the compiler by checking that it can preprocess `<windows.h>` and `<WebView2.h>` and link `WebView2Loader`.

## Current Contract

- `pkg/plugin/windows_dll_windows.c` provides a minimal `DllMain`.
- `pkg/plugin/vst3_bridge.c` exports `GetPluginFactory`, `InitDll` and `ExitDll` with a Windows-friendly symbol annotation.
- `pkg/plugin/editor_view_windows.c` hosts the editor through WebView2.
- `pkg/plugin/windows_bundle.go` defines the canonical bundle path layout, including the generated header sidecar.
- `pkg/plugin/editor_assets/` provides the HTML/CSS/JS snapshot shell.
- `cmd/vst3go-dll/main_windows.go` is the `c-shared` entrypoint scaffold for building a Windows DLL on `amd64`.

## Expected Bundle Shape

- The VST3 consumer should place the built library in the normal plugin bundle layout for the host.
- The Windows host-side package should ship the DLL together with the WebView2 loader/runtime dependencies it expects.
- The editor bridge should stay inside the plugin DLL; the web assets remain embedded in the binary.
- The repo ships `scripts/package_windows_vst3.sh`, `just windows-package`, and `just windows-build` to assemble the bundle from an already-built Windows DLL, requiring the generated header sidecar.
- The repo also ships `scripts/check_windows_vst3.sh` and `just windows-check-bundle` to validate the resulting layout, including the generated header sidecar.
- The repo also ships `scripts/build_windows_vst3.sh` and `just windows-build-dll` to build the DLL when a Windows toolchain is available.
- The repo also ships `scripts/select_windows_cc.sh`, `scripts/preflight_windows_vst3.sh`, and `just windows-preflight` to auto-select or verify a Windows compiler, WebView2 headers, and the loader library before a build. When selection fails, the helper prints which candidates were tried and why they were rejected.
- `just windows-smoke` exercises the selector, preflight, build, packaging, and bundle-check scripts with fake toolchain shims so regressions can be caught on non-Windows hosts.
- `just windows-release` runs preflight once, then the build and the layout check together.

## Build Expectations

- Cross-compiling from Linux is possible only when the Windows C toolchain and WebView2 headers are available.
- Current Windows packaging targets `amd64` and the `x86_64-win` bundle layout.
- The Windows scripts resolve relative bundle roots, bundle inputs, and build helpers against the repository root, so they can be called from any working directory.
- The repo-level Go tests continue to validate the shared runtime code on the current platform.
- Windows-specific packaging validation should happen on an actual Windows toolchain.

## Windows Risks

- Shared-library loading behavior can vary across hosts, especially when the plugin is reloaded repeatedly.
- Scheduler and message-loop timing can make WebView2 focus or resize bugs look intermittent.
- File locking is often stricter on Windows, so generated DLLs may need a clean rebuild before repackaging.
- Path-length edge cases are still worth checking in real plugin install locations.
- The WebView2 host uses a per-editor temp user-data folder to reduce reload and profile-lock friction, and it cleans that folder up on release when possible.
- The WebView2 host keeps default context menus, devtools, and status bar disabled to stay plugin-like.

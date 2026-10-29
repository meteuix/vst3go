English | [한국어](windows-validation.ko.md)

# Windows Validation Checklist

This document is the handoff checklist for real Windows validation. The repository already covers shared runtime correctness with Go tests and script-level Windows smoke coverage on non-Windows hosts. The remaining work is confirming that the actual Windows toolchain, bundle, and WebView2-backed editor behave correctly on a real Windows machine.

For a ready-to-fill execution record, use `docs/windows-validation-report-template.md`.
To create a prefilled run record with current host metadata, run `just windows-init-report`.
To run the whole sequence on a Windows machine and capture the result, use `just windows-validate`.
The runner stops at the first failed step and writes the completed-step list, log directory, and failure point into the report.
For a condensed execution path, see `docs/windows-validation-quickstart.md`.

## Validation Goals

- prove that `just windows-preflight` selects or accepts a usable Windows CGO compiler
- prove that `just windows-build-dll` produces the DLL and generated header sidecar
- prove that `just windows-build` and `just windows-check-bundle` produce the expected `x86_64-win` bundle layout
- prove that the plugin loads in at least one real Windows VST3 host
- prove that the embedded web editor opens, updates parameters, and survives repeated reopen cycles

## Minimum Environment

- Windows 10 or later on `amd64`
- Go toolchain matching the repo's supported version
- MinGW-w64 toolchain on `PATH`
- Microsoft WebView2 SDK headers and loader library available to the build
- WebView2 runtime installed on the test machine
- at least one VST3 host for smoke validation

## Required Commands

Run these from the repository root:

```bash
just windows-init-report
go test -timeout=30000s ./...
just windows-preflight
just windows-build-dll
just windows-build dist/windows/vst3go.dll
just windows-check-bundle
just windows-validate
```

If the DLL lands in a different path, pass that path explicitly to `just windows-build`.

## Build And Packaging Checks

Confirm all of the following:

- `just windows-preflight` succeeds without missing-header or missing-loader errors
- the generated DLL exists
- the generated C header sidecar exists beside the DLL
- the packaged bundle uses the expected `Contents/x86_64-win` layout
- the packaged bundle contains the plugin DLL and `WebView2Loader.dll`
- `just windows-check-bundle` exits successfully
- `just windows-validate` writes a report file with the completed and failed step list

## Host Validation Checks

Install the resulting `.vst3` bundle into a Windows VST3 location and verify:

- the host discovers the plugin without a loader crash
- the plugin can be inserted on a track
- audio processing starts without immediate instability
- the editor window opens successfully
- the WebView2-backed UI renders the embedded HTML/CSS/JS shell
- parameter changes from the UI reach the processor
- host-driven parameter automation reflects back into the UI
- repeated editor open/close cycles do not leave the plugin unusable
- unloading and rescanning the plugin does not fail due to file-locking surprises

## Web Editor Focus Checks

Because the Windows editor host is the biggest platform-specific risk, explicitly verify:

- first open after host scan
- resize behavior
- focus handoff between host and editor
- reopening the editor after closing it
- creating multiple plugin instances in one project
- state restore after saving and reopening the host project

## Capture Artifacts

Keep these artifacts with the validation run:

- exact Windows version
- Go version
- MinGW-w64 compiler version
- WebView2 SDK version or install source
- WebView2 runtime version
- host name and exact host version
- terminal output from `just windows-preflight`
- terminal output from `just windows-build-dll`
- terminal output from `just windows-check-bundle`
- screenshots or video of the editor opening successfully
- any crash logs or host diagnostics if validation fails

## Pass Criteria

The Windows validation task is complete for the MinGW-w64 milestone when:

- all required commands succeed on a real Windows machine
- the packaged bundle passes `just windows-check-bundle`
- the plugin loads and opens its editor in at least one Windows VST3 host
- the embedded web editor updates parameters correctly
- no reproducible crash blocks basic insert, reopen, save, and reload flows

MSVC can stay out of scope until MinGW-w64 validation is stable and a concrete compatibility need appears.

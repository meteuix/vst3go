English | [한국어](windows-validation-quickstart.ko.md)

# Windows Validation Quickstart

Use this page when you are on a real Windows `amd64` machine and want the shortest path to validation.

## One-Minute Path

1. Open a shell that provides `bash` and the repo tools.
2. `cd` into the repository root.
3. Run `just windows-init-report`.
4. Run `just windows-preflight`.
5. Run `just windows-validate`.
6. Open the generated report and fill in the host-validation fields that require manual confirmation.

## Expected Result

- `just windows-preflight` confirms the Windows compiler, headers, and loader are available.
- `just windows-validate` writes a report file and stops at the first failure if anything is missing.
- If validation passes, the report will include the log directory and completed step list.

## If It Fails

- If the compiler is missing, install MinGW-w64 and retry `just windows-preflight`.
- If `WebView2.h` or `WebView2Loader` is missing, install the WebView2 SDK and retry.
- If the host cannot load the plugin, keep the report and attach the host logs and crash details.

## Required Evidence

- Windows version
- Go version
- MinGW-w64 version
- WebView2 SDK version or source
- WebView2 runtime version
- host application name and version
- the generated report from `just windows-validate`

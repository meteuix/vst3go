English | [한국어](windows-validation-attempt-2026-06-16.ko.md)

# Windows Validation Attempt - 2026-06-16

## Summary

- Date: 2026-06-16
- Validator: Codex
- Result: blocked
- Scope attempted: real Windows validation preflight

## Environment

- Host OS: Darwin
- Host architecture: arm64
- `go env GOHOSTOS`: darwin
- `go env GOHOSTARCH`: arm64
- `go env GOOS`: darwin
- `go env GOARCH`: arm64

## Preconditions Checked

- `x86_64-w64-mingw32-gcc`: not found
- `wine64`: not found

## Command Attempted

### `bash scripts/preflight_windows_vst3.sh`

- Result: failed
- Reason: no Windows-capable C compiler available on this machine

```text
no Windows-capable C compiler found
tried:
  - x86_64-w64-mingw32-gcc: not found
  - /usr/bin/clang: missing <windows.h>
  - /usr/bin/cc: missing <windows.h>
```

### `bash scripts/windows_toolchain_smoke.sh`

- Result: passed
- Notes: script-level regression coverage still works on this non-Windows host, but this does not satisfy the real Windows validation requirement.

## Decision

Real Windows validation remains blocked until the work is moved to a real Windows `amd64` machine with:

- MinGW-w64 on `PATH`
- WebView2 SDK headers and loader library
- WebView2 runtime
- at least one Windows VST3 host

Use `docs/windows-validation.md` and `docs/windows-validation-report-template.md` for the next real validation run.

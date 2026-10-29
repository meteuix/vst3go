English | [한국어](windows-validation-report-sanity.ko.md)

# Windows Validation Report

## Summary

- Date: 2026-06-17
- Validator:
- Result: pass / fail / blocked
- Scope: MinGW-w64 / host load / editor reopen / save-restore

## Environment

- Host OS: darwin
- Host architecture: arm64
- Target GOOS: darwin
- Target GOARCH: arm64
- Go version: go version go1.26.1 darwin/arm64
- Windows version:
- CPU architecture:
- MinGW-w64 version:
- WebView2 SDK version or source:
- WebView2 runtime version:
- Host application name:
- Host application version:
- `x86_64-w64-mingw32-gcc`: not found
- `wine64`: not found

## Commands

### `go test -timeout=30000s ./...`

- Result:
- Notes:

```text
paste output here
```

### `just windows-preflight`

- Result:
- Notes:

```text
paste output here
```

### `just windows-build-dll`

- Result:
- Output DLL path:
- Generated header path:
- Notes:

```text
paste output here
```

### `just windows-build dist/windows/vst3go.dll`

- Result:
- Bundle path:
- Notes:

```text
paste output here
```

### `just windows-check-bundle`

- Result:
- Notes:

```text
paste output here
```

## Bundle Verification

- DLL present in `Contents/x86_64-win`: yes / no
- Generated header sidecar present: yes / no
- `WebView2Loader.dll` present: yes / no
- Bundle layout matches expectation: yes / no

## Host Validation

- Plugin discovered by host: yes / no
- Plugin inserted on track: yes / no
- Audio processing starts: yes / no
- Editor opens: yes / no
- Embedded web UI renders: yes / no
- UI parameter changes reach processor: yes / no
- Host automation reflects in UI: yes / no
- Reopen after close works: yes / no
- Multiple instances work: yes / no
- Project save and reopen restores state: yes / no
- Rescan or reload avoids file-lock issues: yes / no

## Evidence

- Screenshot paths:
- Video paths:
- Crash log paths:
- Additional notes:

## Issues

### Issue 1

- Title:
- Repro steps:
- Expected:
- Actual:
- Severity:
- Attachments:

### Issue 2

- Title:
- Repro steps:
- Expected:
- Actual:
- Severity:
- Attachments:

## Final Decision

- MinGW-w64 validation complete: yes / no
- Ready to keep Windows in release scope: yes / no
- Follow-up tasks:

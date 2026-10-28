# Release Checklist

Use this checklist before tagging a new `vst3go` release.

## Scope Decision

- confirm the release still matches the repo boundary in `README.md`
- confirm higher-level DSP, showcase, or product logic has not drifted back into this repo
- confirm the intended release scope is still the VST3 binding/runtime layer, web-rendered editor shell, and Windows build harness

## Runtime Validation

- run `go build ./...`
- run `go test -timeout=30000s ./...`
- run `go test -race -p 1 -timeout=30000s ./...`
- confirm `git diff --check` passes

## Windows Validation

- run `bash scripts/windows_toolchain_smoke.sh` on the current non-Windows development environment if applicable
- confirm the latest real Windows validation followed `docs/windows-validation.md`
- confirm a completed `docs/windows-validation-report-template.md` record exists for the target release candidate
- confirm MinGW-w64 validation passed on a real Windows machine if Windows remains in release scope

## Documentation

- confirm `README.md` still reflects the current package surface and release scope
- confirm `docs/runtime-contracts.md` still matches runtime behavior
- confirm `docs/web-editor-bridge.md` still matches the editor shell contract
- confirm `docs/windows-build.md` and `docs/windows-validation.md` still match the current scripts and bundle flow
- confirm `PLAN.md` reflects the remaining work accurately

## Consumer Readiness

- confirm the module path is still `github.com/th-release/vst3go`
- confirm supported downstream API packages remain stable enough for tagging
- confirm any intentionally compatibility-breaking changes are called out in release notes

## Release Artifacts

- record the release version or tag candidate
- record the exact Go version used for validation
- record the exact commit SHA being tagged
- collect the latest Windows validation artifacts if Windows is included

## Go Or No-Go

The release is ready when:

- runtime validation is green
- documentation matches the shipped behavior
- Windows evidence is present for any Windows-in-scope release
- no known blocker remains in the supported API surface

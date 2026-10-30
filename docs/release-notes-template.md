English | [한국어](release-notes-template.ko.md)

# Release Notes Template

Use this template when publishing a new `vst3go` tag.

## Summary

`vst3go` `<version>` delivers updates to the VST3 runtime layer, DSP library, showcase plugins, web-rendered editor shell, and Windows build/validation flow.

## Highlights

- highlight 1
- highlight 2
- highlight 3

## Runtime And API

- summarize runtime-facing improvements
- note any parameter, state, process, or wrapper behavior changes
- call out any compatibility-sensitive API adjustments

## DSP And Examples

- summarize new or changed `pkg/dsp` processors and their behavior changes
- note new, changed or removed showcase plugins in `examples/`

## Web Editor

- summarize editor shell or snapshot-contract changes
- note any hidden/read-only/state-restore behavior changes

## Windows

- summarize build harness, bundle, or WebView2 changes
- state whether real Windows MinGW-w64 validation completed for this release
- link or reference the matching Windows validation report if applicable

## Validation

- `go build ./...`
- `go test -timeout=30000s ./...`
- `go test -race -p 1 -timeout=30000s ./...`
- `bash scripts/windows_toolchain_smoke.sh`
- real Windows validation status: pass / pending / out of scope

## Compatibility Notes

- module path: `github.com/th-release/vst3go/v2`
- supported public API surface: `pkg/plugin`, `pkg/vst3`, `pkg/midi`, `pkg/framework/{bus,param,plugin,process,state,dsp,voice,debug}`, `pkg/dsp/...`
- any breaking changes:

## Upgrade Notes

- mention any downstream action required
- mention any changed build assumptions
- mention if consumers should retest Windows hosts before rollout

## Known Gaps

- note any remaining non-blocking issues
- note whether MSVC is still out of scope

## References

- `docs/release-checklist.md`
- `docs/runtime-contracts.md`
- `docs/windows-validation.md`
- `docs/windows-validation-report-template.md`

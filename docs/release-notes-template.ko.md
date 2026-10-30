[English](release-notes-template.md) | 한국어

# 릴리스 노트 템플릿

새 `vst3go` 태그를 게시할 때 이 템플릿을 사용합니다.

## 요약

`vst3go` `<version>`은 VST3 런타임 계층, DSP 라이브러리, 쇼케이스 플러그인, 웹 렌더링 에디터 셸, Windows 빌드/검증 흐름에 대한 업데이트를 제공합니다.

## 주요 변경 사항

- 주요 변경 사항 1
- 주요 변경 사항 2
- 주요 변경 사항 3

## 런타임과 API

- 런타임 관련 개선 사항을 요약합니다
- 파라미터, 상태, 처리, 래퍼 동작의 변경 사항을 기록합니다
- 호환성에 영향을 주는 API 조정 사항을 명시합니다

## DSP와 예제

- 새로 추가되거나 바뀐 `pkg/dsp` 프로세서와 그 동작 변경을 요약합니다
- `examples/`에서 새로 추가, 변경, 제거된 쇼케이스 플러그인을 기록합니다

## 웹 에디터

- 에디터 셸 또는 스냅샷 계약의 변경 사항을 요약합니다
- hidden/read-only/상태 복원 동작의 변경 사항을 기록합니다

## Windows

- 빌드 하네스, 번들, WebView2 관련 변경 사항을 요약합니다
- 이번 릴리스에서 실제 Windows MinGW-w64 검증을 완료했는지 명시합니다
- 해당되는 경우 대응하는 Windows 검증 리포트에 대한 링크나 참조를 남깁니다

## 검증

- `go build ./...`
- `go test -timeout=30000s ./...`
- `go test -race -p 1 -timeout=30000s ./...`
- `bash scripts/windows_toolchain_smoke.sh`
- 실제 Windows 검증 상태: pass / pending / out of scope

## 호환성 참고 사항

- 모듈 경로: `github.com/meteuix/vst3go/v2`
- 지원하는 공개 API 범위: `pkg/plugin`, `pkg/vst3`, `pkg/midi`, `pkg/framework/{bus,param,plugin,process,state,dsp,voice,debug}`, `pkg/dsp/...`
- 호환성을 깨는 변경 사항:

## 업그레이드 참고 사항

- 다운스트림에서 필요한 조치를 언급합니다
- 변경된 빌드 전제 조건을 언급합니다
- 배포 전에 사용자가 Windows host를 다시 테스트해야 하는지 언급합니다

## 알려진 공백

- 남아 있는, 릴리스를 막지 않는 이슈를 기록합니다
- MSVC가 여전히 범위 밖인지 기록합니다

## 참고 자료

- `docs/release-checklist.md`
- `docs/runtime-contracts.md`
- `docs/windows-validation.md`
- `docs/windows-validation-report-template.md`

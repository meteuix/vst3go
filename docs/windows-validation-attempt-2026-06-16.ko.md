[English](windows-validation-attempt-2026-06-16.md) | 한국어

# Windows 검증 시도 - 2026-06-16

## 요약

- 날짜: 2026-06-16
- 검증자: Codex
- 결과: blocked
- 시도한 범위: 실제 Windows 검증 사전 점검(preflight)

## 환경

- Host OS: Darwin
- Host 아키텍처: arm64
- `go env GOHOSTOS`: darwin
- `go env GOHOSTARCH`: arm64
- `go env GOOS`: darwin
- `go env GOARCH`: arm64

## 확인한 사전 조건

- `x86_64-w64-mingw32-gcc`: 찾을 수 없음
- `wine64`: 찾을 수 없음

## 시도한 명령

### `bash scripts/preflight_windows_vst3.sh`

- 결과: 실패
- 사유: 이 머신에는 Windows용 C 컴파일러가 없음

```text
no Windows-capable C compiler found
tried:
  - x86_64-w64-mingw32-gcc: not found
  - /usr/bin/clang: missing <windows.h>
  - /usr/bin/cc: missing <windows.h>
```

### `bash scripts/windows_toolchain_smoke.sh`

- 결과: 통과
- 비고: 이 비 Windows host에서도 스크립트 수준의 회귀 검사는 여전히 동작하지만, 실제 Windows 검증 요구 사항을 충족하지는 않습니다.

## 결정

다음 조건을 갖춘 실제 Windows `amd64` 머신으로 작업을 옮기기 전까지 실제 Windows 검증은 blocked 상태로 남습니다:

- `PATH`에 있는 MinGW-w64
- WebView2 SDK 헤더와 로더 라이브러리
- WebView2 런타임
- 최소 하나 이상의 Windows VST3 host

다음 실제 검증 실행에는 `docs/windows-validation.md`([한국어](windows-validation.ko.md))와 `docs/windows-validation-report-template.md`([한국어](windows-validation-report-template.ko.md))를 사용하세요.

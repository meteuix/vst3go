[English](development-workflow.md) | 한국어

# 개발 워크플로

이 문서는 저장소가 범위에 집중하고, 테스트 가능하며, 언제든 릴리스할 수 있는 상태를 유지하도록 `vst3go`에서 개발 작업을 어떻게 진행해야 하는지 설명합니다.

## 저장소의 의도

- `vst3go`는 VST3 바인딩과 런타임 계층으로 한정합니다
- 제품 DSP, 쇼케이스 플러그인, 상위 수준의 오디오 동작은 `synthkit` 같은 다운스트림 저장소에 둡니다
- 플랫폼 글루 코드는 얇게 유지하고, 가능하면 동작은 Go에 둡니다

## 작업 방식

- 광범위한 리팩터링보다 경계를 지키는 작은 변경을 선호합니다
- 우회책을 덧쌓지 말고 근본 원인을 고칩니다
- 플랫폼별 코드는 가능한 한 가장 작은 범위로 격리합니다
- 다운스트림 사용자, 검증, 릴리스 범위에 영향을 주는 동작 변경은 문서화합니다

## 권장 변경 순서

간단하지 않은 변경을 구현할 때는 다음 순서를 따릅니다:

1. 저장소 경계와 영향을 받는 패키지 범위를 확인합니다
2. 기존 런타임, 브리지, 문서 계약을 살펴봅니다
3. 문제를 해결하는 가장 작은 변경을 구현합니다
4. 집중된 테스트를 추가하거나 확장합니다
5. 동작이 바뀌었다면 문서와 릴리스/검증 산출물을 갱신합니다
6. 작업을 마치기 전에 빌드와 테스트 검증을 실행합니다

## 패키지 가이드라인

- `pkg/vst3`: SDK와 맞닿은 바인딩과 헬퍼 타입을 단순하게 유지합니다
- `pkg/plugin`: 래퍼, 에디터 모델, 플랫폼 브리지 통합을 런타임 계약 중심으로 유지합니다
- `pkg/framework/*`: 재사용 가능한 런타임 기본 요소를 범용적이고 다운스트림 플러그인 작성자에게 안전하게 유지합니다
- `pkg/midi`: 이벤트와 트랜스포트 헬퍼를 가볍고 런타임 중심으로 유지합니다
- `pkg/plugin/*.c`, `*.h`: C 계층을 최소한의 기계적인 코드로 유지합니다

## 웹 에디터 가이드라인

- Go 측 에디터 스냅샷과 파라미터 모델을 단일 진실 공급원(source of truth)으로 취급합니다
- HTML, CSS, JS가 모든 플랫폼에서 동일한 스냅샷 계약에 맞춰지도록 유지합니다
- hidden 및 read-only 파라미터의 의미를 네이티브 모델과 일치시킵니다
- 이 저장소 안에 별도의 제품 UI 프레임워크를 만들지 않습니다

## Windows 개발 가이드라인

- Windows 지원은 별도의 런타임 아키텍처가 아니라 빌드 하네스와 플랫폼 host 글루로 취급합니다
- WebView2 호스팅 관련 사항은 공용 런타임 패키지와 격리합니다
- 비 Windows host에서는 `bash scripts/windows_toolchain_smoke.sh`로 스크립트 수준 동작을 검증합니다
- 실제 로딩, 에디터 동작, 번들 설치 흐름은 `docs/windows-validation.md`([한국어](windows-validation.ko.md))를 따라 실제 Windows 머신에서 검증합니다

## 검증 기준선

런타임 코드나 문서상의 기대 사항을 건드리는 변경을 마치기 전에 다음을 실행합니다:

```bash
git diff --check
go build ./...
go test -timeout=30000s ./...
```

스레드 안전성 가정이 관련된 경우 다음을 추가합니다:

```bash
go test -race -p 1 -timeout=30000s ./...
```

Windows 빌드 흐름이나 문서가 바뀐 경우 다음을 추가합니다:

```bash
bash scripts/windows_toolchain_smoke.sh
bash scripts/docker_build_windows.sh      # mingw-w64 cross build + export check
```

VST3 런타임(`pkg/plugin`, C 브리지)이 바뀐 경우 다음을 추가합니다. validator는 CMake로
빌드한 VST3 SDK 체크아웃에서 가져옵니다:

```bash
bash scripts/validate_vst3.sh <sdk>/build/bin/Release/validator  # validator -e + hostcheck + preset files
bash scripts/validate_clap.sh <clap-validator>/target/release/clap-validator  # clap-validator + clapcheck
bash scripts/validate_au.sh                                     # macOS: auval -strict + aucheck (Audio Units)
bash scripts/validate_lv2.sh                                    # Linux: lv2_validate, lv2lint, lv2check (LV2)
bash scripts/docker_validate_linux.sh <sdk> [linux/amd64]       # VST3, CLAP, LV2 and the vst3go command on Linux, plus go test
bash scripts/docker_check_linux_editor.sh                       # WebKitGTK editor under Xvfb (VST3, CLAP, LV2 UI)
bash scripts/check_macos_editor.sh                              # WKWebView editor in an NSWindow (VST3 + CLAP)
bash scripts/check_editor_sdk.sh                                # editor SDK under node, bindings and tsc
bash scripts/check_standalone.sh                                # standalone apps, MIDI round trip, editor window
bash scripts/docker_check_linux_standalone.sh                   # standalone apps on Linux (SOUND=1 adds ALSA)
```

`cmd/vst3go`(`new` 템플릿이나 `build`)가 바뀐 경우 다음을 추가합니다:

```bash
VST3_VALIDATOR=<validator> CLAP_VALIDATOR=<clap-validator> bash scripts/check_cli.sh  # scaffolded modules: tests, every format, validators, auval
```

clap-validator는 https://github.com/free-audio/clap-validator 에서
`cargo build --release`로 빌드합니다(Docker 스크립트는 고정된 리비전을 빌드합니다).

## 문서화 기대 사항

- 범위, 릴리스 가이드, 사용자 가이드가 바뀌면 `README.md`를 갱신합니다
- 라이프사이클, 스레드 안전성, 영속성 기대 사항이 바뀌면 `docs/runtime-contracts.md`를 갱신합니다
- 다운스트림 플러그인 개발에 대한 자세한 가이드와 예제는 `docs/developing-with-vst3go.md`([한국어](developing-with-vst3go.ko.md))를 사용합니다
- 에디터 셸 동작이나 에셋 통합이 바뀌면 `docs/web-editor-bridge.md`를 갱신합니다
- 빌드 스크립트, 패키징 규칙, 검증 기대 사항이 바뀌면 Windows 문서를 갱신합니다

## 릴리스 기대 사항

- 태그를 달기 전에 `docs/release-checklist.md`([한국어](release-checklist.ko.md))를 사용합니다
- 릴리스 공지를 준비할 때 `docs/release-notes-template.md`([한국어](release-notes-template.ko.md))를 사용합니다
- Windows가 릴리스 범위에 남아 있는 동안에는 Windows 증빙 자료를 함께 첨부합니다

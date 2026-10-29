[English](README.md) | 한국어

# VST3Go

`vst3go`는 VST3를 위한 저수준 Go 인터페이스이자 런타임 계층입니다.

이 포크는 원본 `justyntemme/vst3go` 프로젝트와 의도적으로 다릅니다. 깔끔한 VST3 바인딩/런타임 계층으로서 범위를 일부러 좁게 유지하고 있으며, 상위 수준의 DSP와 신스 중심 기능은 자매 저장소인 `synthkit`에 있습니다.

포함된 내용:

- 공식 VST3 C API 헤더(`include/vst3`), CLAP 헤더(`include/clap`), LV2 헤더(`include/lv2`), 그리고 `pkg/plugin`의 C 브리지 글루 코드
- VST3 타입과 처리 구조체를 위한 Go 바인딩
- Go 프로세서를 VST3, CLAP, (macOS에서) AUv2, (Linux에서) LV2 플러그인으로 노출하는 플러그인 래퍼/런타임(하나의 바이너리가 모든 포맷을 제공), 그리고 같은 패키지를 오디오/MIDI 장치와 창에 띄운 에디터를 갖춘 스탠드얼론 애플리케이션으로 실행하는 기능(`pkg/standalone`)
- 웹 에디터 SDK(`window.vst3go`, 커스텀 페이지, 타입이 지정된 TypeScript 파라미터 바인딩)
- 버스, 파라미터, 처리 컨텍스트, 상태, MIDI/이벤트 전송 같은 범용 플러그인 작성 기본 요소
- 이펙트, 악기, MIDI 플러그인을 위한 host 기능: 샘플 단위로 정확한 오토메이션, 32/64비트 처리, 사이드체인과 서라운드, MIDI CC/learn/MIDI 2.0 매핑, 노트 익스프레션과 MPE, 키스위치, 팩토리 프로그램, `.vstpreset` 프리셋, 유닛, SysEx, 파라미터 스무딩, 편집 제스처, 분산 프로세서/컨트롤러, 바이너리당 여러 플러그인, host 없이 하는 플러그인 테스트(`pkg/plugintest`), 오디오 파일 읽기/쓰기(`pkg/audiofile`: WAV, AIFF, FLAC, Ogg Vorbis, MP3)

상위 수준의 오디오 및 신스 계층은 이제 자매 저장소인 `synthkit`에 있습니다.

어떤 종류의 플러그인을 만들 수 있는지와 각 host 기능의 사용법은 [`docs/plugin-capabilities.ko.md`](docs/plugin-capabilities.ko.md)를 참고하세요.
웹으로 렌더링되는 플러그인 에디터 흐름은 [`docs/web-editor-bridge.ko.md`](docs/web-editor-bridge.ko.md)를 참고하세요.
Windows 빌드 및 패키징 참고 사항은 [`docs/windows-build.ko.md`](docs/windows-build.ko.md)를 참고하세요.
실제 Windows 검증 인계 체크리스트는 [`docs/windows-validation.ko.md`](docs/windows-validation.ko.md)를 참고하세요.
요약된 Windows 퀵스타트는 [`docs/windows-validation-quickstart.ko.md`](docs/windows-validation-quickstart.ko.md)를 참고하세요.
바로 채워 넣을 수 있는 Windows 검증 실행 기록은 [`docs/windows-validation-report-template.ko.md`](docs/windows-validation-report-template.ko.md)를 참고하세요.
라이프사이클, 스레드 안전성, 영속성 기대 사항은 [`docs/runtime-contracts.ko.md`](docs/runtime-contracts.ko.md)를 참고하세요.
일상적인 구현 및 검증 관례는 [`docs/development-workflow.ko.md`](docs/development-workflow.ko.md)를 참고하세요.
아주 자세한 다운스트림 플러그인 개발 가이드는 [`docs/developing-with-vst3go.ko.md`](docs/developing-with-vst3go.ko.md)를 참고하세요.
상용 로그인/라이선스 연동 가이드는 [`docs/commercial-authentication.ko.md`](docs/commercial-authentication.ko.md)를 참고하세요.
태그 전 최종 확인은 [`docs/release-checklist.ko.md`](docs/release-checklist.ko.md)를 참고하세요.
릴리스 노트 게시는 [`docs/release-notes-template.ko.md`](docs/release-notes-template.ko.md)를 참고하세요.

## 빠른 시작

`vst3go` 명령은 플러그인 모듈을 만들고 모든 포맷으로 빌드합니다
(Go 1.24 이상, C 컴파일러 필요. macOS에서는 Xcode 명령줄 도구):

```bash
go run github.com/th-release/vst3go/cmd/vst3go@latest new -type effect ./mygain "My Gain"
cd mygain
go test ./...                                # offline tests with pkg/plugintest
go tool vst3go build -install . "My Gain"     # VST3, CLAP, AU (macOS) or LV2 (Linux), into the user plugin folders
go tool vst3go build -format app . "My Gain"  # standalone application
```

`-type instrument`는 폴리포닉 신스에서, `-type midi`는 MIDI 트랜스포저에서 시작합니다.
새 모듈은 `vst3go`를 `tool` 의존성으로 등록하므로, `go tool vst3go`는 항상 빌드 대상
런타임 버전과 일치합니다.

## 범위

`vst3go`는 VST3와 맞닿은 부분을 담당합니다:

- `include/vst3/`
- `include/clap/`
- `pkg/vst3/`
- `pkg/plugin/`
- `pkg/midi/`
- `pkg/preset/`
- `pkg/plugintest/`
- `pkg/audiofile/`
- `pkg/standalone/` (및 `internal/midiport`)
- `pkg/framework/bus`
- `pkg/framework/param`
- `pkg/framework/plugin`
- `pkg/framework/process`
- `pkg/framework/state`
- `cmd/vst3go` (`new`/`build` 명령)

자매 저장소 `synthkit`이 담당하는 부분:

- `pkg/dsp/`
- `pkg/framework/dsp`
- `pkg/framework/debug`
- `pkg/framework/voice`
- 쇼케이스 플러그인(`internal/validation/`에는 런타임 검증용 플러그인만 있습니다)

## 공개 API 경계

- 다운스트림 코드는 `pkg/plugin`, `pkg/vst3`, `pkg/midi`, `pkg/preset`, `pkg/plugintest`, `pkg/audiofile`, `pkg/standalone`, 그리고 유지되는 `pkg/framework/*` 패키지를 지원되는 API 범위로 취급해야 합니다.
- C 브리지(`pkg/plugin/*.c`)와 에디터 에셋은 그 API 범위를 뒷받침하기 위해 존재하지만, 제품별 DSP와 쇼케이스 로직은 이 저장소에 포함되지 않습니다.
- 어떤 기능이 상위 수준의 오디오 제품 동작을 필요로 한다면, 이 런타임 계층이 아니라 `synthkit`이나 사용하는 쪽 프로젝트에 속합니다.

## 사용 방법

- 다운스트림 저장소는 브리지 내부에 직접 손대지 말고 `github.com/th-release/vst3go`를 import하여 지원되는 패키지 범위 위에 구축해야 합니다.
- 버전에 태그가 붙으면, 사용자는 브리지/런타임 변경을 의도적으로 업그레이드할 수 있도록 계속 움직이는 브랜치 대신 태그에 고정해야 합니다.
- DAW 검증, 서명, 인스톨러, 제품별 DSP 동작은 다운스트림의 책임으로 남습니다.

## 자매 저장소

권장 이름: `synthkit`

목적:

- DSP 빌딩 블록
- 악기/이펙트 쇼케이스 플러그인
- `vst3go` 위에 구축한 상위 수준 오디오 헬퍼

## 개발

이 저장소의 핵심 검사:

```bash
just fmt
just fmt-check
just test
just vst3 ./path/to/plugin MyPlugin          # build a macOS/Linux .vst3 bundle
just validate-vst3 /path/to/vst3sdk/validator # validator + hostcheck on internal/validation
just clap ./path/to/plugin MyPlugin          # build a macOS/Linux .clap plugin
just validate-clap /path/to/clap-validator   # clap-validator + clapcheck on internal/validation
just au ./path/to/plugin MyPlugin            # build a macOS Audio Unit (.component)
just check-cli                                # vst3go new/build: scaffolded modules, tests, every format
just validate-au                              # auval (strict) + aucheck on internal/validation
just lv2 ./path/to/plugin MyPlugin           # build a Linux LV2 bundle (.lv2)
just validate-lv2                             # lv2_validate + lv2lint + lv2check (lilv) on internal/validation
just docker-validate-linux /path/to/vst3sdk   # VST3, CLAP, LV2 and vst3go command checks on Linux in Docker
just docker-check-linux-editor                # Linux WebKitGTK editor under Xvfb
just check-macos-editor                       # macOS editor in a real window, VST3 and CLAP
just check-editor-sdk                         # web editor SDK under node and tsc
just standalone ./path/to/plugin MyPlugin    # standalone application (.app / executable)
just check-standalone                         # standalone apps: null device, MIDI round trip, editor window
just docker-check-linux-standalone            # standalone apps on Linux in Docker
just docker-build-windows                     # mingw-w64 Windows cross build
```

## 검증

- 이 저장소는 공용 런타임 계층을 `just test`로, VST3 계약을 `just validate-vst3`(extensive 모드의 Steinberg validator와 샘플 단위 정확한 처리를 확인하는 `internal/validation/hostcheck`)로, CLAP 계약을 `just validate-clap`(clap-validator와 `hostcheck/clapcheck.c`)로, Audio Unit 계약을 `just validate-au`(strict 모드의 Apple auval과 `hostcheck/aucheck_darwin.m`)로, LV2 계약을 `just validate-lv2`(lv2_validate, lv2lint, lilv 기반의 `hostcheck/lv2check.c`)로, 그리고 host 셸을 사용할 수 있을 때 Windows 빌드 경로를 `just windows-smoke`로 검증합니다.
- 플랫폼별 통합 검사, host 앱 동작, 다운스트림 DSP/플러그인 연결은 자매 저장소 `synthkit`이나 사용하는 쪽 프로젝트에 속합니다.
- 이 저장소의 Windows 패키징 및 번들 레이아웃 검사는 생성된 DLL, 헤더 사이드카, 레이아웃 계약에 집중합니다.
- 실제 Windows 최종 승인은 툴체인, 번들, host, WebView2 동작이 일관되게 기록되도록 `docs/windows-validation.md`([한국어](docs/windows-validation.ko.md))를 따라야 합니다.

## 릴리스 범위

- 이 저장소는 VST3 바인딩/런타임 계층, 웹 렌더링 에디터 셸, 그리고 그 셸을 지원하는 Windows 번들/빌드 하네스를 배포합니다.
- 이 저장소는 상위 수준의 DSP, 쇼케이스, 제품별 신스 로직을 배포하지 않으며, 이들은 `synthkit`에 남습니다.
- 이 저장소의 릴리스 검증은 `just test`, `just windows-smoke`, 그리고 문서화된 번들/레이아웃 검사를 중심으로 합니다.
- 태그 전 최종 검토는 `docs/release-checklist.md`([한국어](docs/release-checklist.ko.md))를 따라야 합니다.

Windows 전용 에디터 및 패키징 참고 사항:

- `pkg/plugin/windows_dll_windows.c`는 DLL 엔트리 포인트를 제공합니다.
- `pkg/plugin/vst3_bridge.c`는 `GetPluginFactory`, `InitDll`, `ExitDll`을 export합니다.
- `pkg/plugin/editor_view_windows.c`는 WebView2 헤더와 로더 지원을 필요로 합니다.
- Windows 지원은 현재 `amd64`와 `x86_64-win` 번들 레이아웃을 대상으로 합니다.
- `just windows-preflight`는 필요할 때 호환되는 Windows 컴파일러를 자동 선택하고 `windows.h`, `WebView2.h`, `WebView2Loader`를 확인합니다. 동작하는 컴파일러가 없으면 시도한 후보와 각각 실패한 이유를 보고합니다.
- `just windows-smoke`는 실제 Windows host 없이 선택, preflight, 빌드, 패키징, 번들 검증을 실행하는 로컬 스크립트 전용 스모크 테스트를 실행합니다.
- `just windows-init-report`는 현재 host 메타데이터가 미리 채워진 Windows 검증 리포트 뼈대를 만듭니다.
- `just windows-validate`는 실제 Windows 머신에서 종단 간 검증 절차를 실행하고 리포트 파일을 작성합니다.
- `just windows-build-dll`은 Windows 툴체인이 있을 때 Windows 공유 라이브러리를 빌드합니다.
- `just windows-build <dll>`은 빌드된 Windows DLL로부터 VST3 번들 디렉터리를 조립하며, 생성된 헤더 사이드카가 필요합니다.
- `just windows-check-bundle`은 결과 번들 레이아웃을 검증합니다.
- `just windows-release`는 preflight를 한 번 실행한 뒤 빌드와 검증을 한 번에 실행합니다.
- `docs/windows-build.md`([한국어](docs/windows-build.ko.md))에 기대하는 번들 형태가 설명되어 있습니다.

## 라이선스

이 프로젝트는 MIT 라이선스로 배포됩니다. 함께 포함된 VST3 SDK 헤더는 각각의 라이선스를 따릅니다.

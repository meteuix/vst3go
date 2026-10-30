[English](README.md) | 한국어

# VST3Go

[![ci](https://github.com/th-release/vst3go/actions/workflows/ci.yml/badge.svg)](https://github.com/th-release/vst3go/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/th-release/vst3go/v2.svg)](https://pkg.go.dev/github.com/th-release/vst3go/v2) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`vst3go`는 VST3를 위한 저수준 Go 인터페이스이자 런타임 계층입니다.

이 포크는 원본 `justyntemme/vst3go` 프로젝트와 별도로 관리됩니다. 플러그인 런타임과 함께 DSP 라이브러리, 그리고 그 위에 만든 쇼케이스 플러그인을 제공합니다.

포함된 내용:

- 공식 VST3 C API 헤더(`include/vst3`), CLAP 헤더(`include/clap`), LV2 헤더(`include/lv2`), 그리고 `pkg/plugin`의 C 브리지 글루 코드
- VST3 타입과 처리 구조체를 위한 Go 바인딩
- Go 프로세서를 VST3, CLAP, (macOS에서) AUv2, (Linux에서) LV2 플러그인으로 노출하는 플러그인 래퍼/런타임(하나의 바이너리가 모든 포맷을 제공), macOS와 iOS용 AUv3 앱 확장, 그리고 같은 패키지를 오디오/MIDI 장치, 트랜스포트, 오디오 설정 창, 창에 띄운 에디터를 갖춘 스탠드얼론 애플리케이션으로 실행하는 기능(`pkg/standalone`)
- VST3, CLAP, Audio Units를 통한 ARA 2 지원(`pkg/ara`): 녹음 전체를 보면서 host의 클립을 편집하고 렌더링하는 플러그인용으로, 노트 분석, host 곡에 저장, 영역 렌더링 포함
- 웹 에디터 SDK(`window.vst3go`, 커스텀 페이지, 타입이 지정된 TypeScript 파라미터 바인딩, 플랫폼의 파일 열기 대화상자)
- 버스, 파라미터, 처리 컨텍스트, 상태, MIDI/이벤트 전송 같은 범용 플러그인 작성 기본 요소
- DSP 라이브러리(`pkg/dsp`): 필터, 다이내믹스(컴프레서, 리미터, 게이트, 익스팬더, 트랜지언트 셰이퍼), 딜레이 라인, 모듈레이션(LFO, 코러스, 플랜저, 페이저, 트레몰로, 링 모듈레이션), 리버브(Schroeder, Freeverb, FDN), 디스토션(웨이브셰이퍼, 튜브, 테이프, 비트크러셔), 오실레이터(대역 제한, 웨이브테이블, FM 오퍼레이터), 엔벨로프, 패닝, 믹싱, 보간, 오버샘플링과 샘플 레이트 변환, FFT와 STFT 처리, 분할 컨볼루션, 피치 시프트, 타임 스트레치와 피치 검출, 그래뉼러 합성, 디스크 스트리밍을 지원하는 샘플 재생, Neural Amp Modeler 모델(WaveNet, LSTM), 분석(스펙트럼, 피크/RMS/LUFS 미터링, 위상 상관)
- 프로세서 체인(`pkg/framework/dsp`), 폴리포닉 보이스 할당(`pkg/framework/voice`), 디버그/프로파일링 헬퍼(`pkg/framework/debug`)
- 쇼케이스 플러그인(`examples/`): 컴프레서, 멀티밴드 컴프레서, 리미터, 게이트, 트랜지언트 셰이퍼, 드럼 버스, 보컬 스트립, 디스토션, 코러스, 플랜저, 딜레이, 필터, 사이드체인 더킹, 서라운드, 오버샘플링 새추레이터, 컨볼루션 리버브, 피치 시프터, 튜너, 그래뉼레이터, 웨이브테이블/FM/폴리포닉 신스, SFZ 샘플러, NAM 앰프, ARA 클립 에디터, 디버그 로깅 예제 등
- 이펙트, 악기, MIDI 플러그인을 위한 host 기능: 샘플 단위로 정확한 오토메이션, 32/64비트 처리, host와 협상하는 레이아웃을 지원하는 사이드체인과 서라운드, MIDI CC/learn/MIDI 2.0 매핑과 MIDI 2.0 입력, 노트 익스프레션과 MPE, 키스위치, 팩토리 프로그램, `.vstpreset` 프리셋, 유닛, SysEx, 파라미터 스무딩, 편집 제스처, 분산 프로세서/컨트롤러, 바이너리당 여러 플러그인, host 없이 하는 플러그인 테스트(`pkg/plugintest`), 오디오 파일 읽기/쓰기(`pkg/audiofile`: WAV, AIFF, FLAC, Ogg Vorbis, MP3)

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
go run github.com/th-release/vst3go/v2/cmd/vst3go@latest new -type effect ./mygain "My Gain"
cd mygain
go test ./...                                # offline tests with pkg/plugintest
go tool vst3go build -install . "My Gain"     # VST3, CLAP, AU (macOS) or LV2 (Linux), into the user plugin folders
go tool vst3go build -format app . "My Gain"  # standalone application
```

`-type instrument`는 폴리포닉 신스에서, `-type midi`는 MIDI 트랜스포저에서 시작합니다.
새 모듈은 `vst3go`를 `tool` 의존성으로 등록하므로, `go tool vst3go`는 항상 빌드 대상
런타임 버전과 일치합니다.

기존 모듈에 런타임만 추가하려면 다음을 실행합니다.

```bash
go get github.com/th-release/vst3go/v2@latest
```

플랫폼별 준비물은 다음과 같습니다.

- macOS: Xcode 명령줄 도구(`xcode-select --install`). iOS용 AUv3는 전체 Xcode가 필요합니다
- Linux: C 컴파일러, `pkg-config`, `libx11-dev`. 플러그인 에디터를 쓰려면 `libgtk-3-dev`와 `libwebkit2gtk-4.1-dev`도 필요합니다
- Windows: [`docs/windows-build.ko.md`](docs/windows-build.ko.md)의 교차 빌드

## 범위

`vst3go`에 포함된 것:

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
- `pkg/dsp/` (DSP 라이브러리)
- `pkg/framework/dsp` (프로세서 체인과 DSP 어댑터)
- `pkg/framework/voice` (폴리포닉 보이스 할당)
- `pkg/framework/debug` (로깅, 프로파일링, 오디오 분석 헬퍼)
- `cmd/vst3go` (`new`/`build` 명령)
- `examples/` (쇼케이스 플러그인)
- `internal/validation/` (런타임 검증용 플러그인과 host 검사)

## 공개 API 경계

- 다운스트림 코드는 `pkg/plugin`, `pkg/vst3`, `pkg/midi`, `pkg/preset`, `pkg/plugintest`, `pkg/audiofile`, `pkg/standalone`, `pkg/dsp/...`, 그리고 `pkg/framework/*` 패키지를 지원되는 API 범위로 취급해야 합니다.
- C 브리지(`pkg/plugin/*.c`)와 에디터 에셋은 그 API 범위를 뒷받침하기 위해 존재하며 직접 호출하지 않습니다.
- `examples/`는 DSP 라이브러리와 런타임이 어떻게 맞물리는지 보여 줍니다. import하지 말고 필요한 부분을 복사해서 쓰세요.

## 사용 방법

- 다운스트림 저장소는 브리지 내부에 직접 손대지 말고 `github.com/th-release/vst3go/v2`를 import하여 지원되는 패키지 범위 위에 구축해야 합니다.
- 브리지/런타임 변경을 의도적으로 업그레이드할 수 있도록 계속 움직이는 브랜치 대신 릴리스 태그(`v2.x.y`)에 고정해야 합니다. 모듈 경로에 메이저 버전이 들어가므로 import 경로는 `github.com/th-release/vst3go/v2/...`입니다.
- DAW 검증, 서명, 인스톨러, 제품별 동작은 다운스트림의 책임으로 남습니다.

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
just docker-check-windows-wine                # the Windows build under Wine: hostcheck, clapcheck, apps
```

## 검증

- 이 저장소는 공용 런타임 계층을 `just test`로, VST3 계약을 `just validate-vst3`(extensive 모드의 Steinberg validator와 샘플 단위 정확한 처리를 확인하는 `internal/validation/hostcheck`)로, CLAP 계약을 `just validate-clap`(clap-validator와 `hostcheck/clapcheck.c`)로, Audio Unit 계약을 `just validate-au`(strict 모드의 Apple auval과 `hostcheck/aucheck_darwin.m`)로, LV2 계약을 `just validate-lv2`(lv2_validate, lv2lint, lilv 기반의 `hostcheck/lv2check.c`)로, 그리고 host 셸을 사용할 수 있을 때 Windows 빌드 경로를 `just windows-smoke`로 검증합니다.
- DSP 라이브러리와 쇼케이스 플러그인은 `just test`로 검사합니다(`pkg/dsp/...`, `pkg/framework/...`, `examples/...`의 오프라인 플러그인 테스트).
- host 앱 동작과 제품별 플러그인 연결은 사용하는 쪽 프로젝트에 속합니다.
- 이 저장소의 Windows 패키징 및 번들 레이아웃 검사는 생성된 DLL, 헤더 사이드카, 레이아웃 계약에 집중합니다.
- 실제 Windows 최종 승인은 툴체인, 번들, host, WebView2 동작이 일관되게 기록되도록 `docs/windows-validation.md`([한국어](docs/windows-validation.ko.md))를 따라야 합니다.

## 릴리스 범위

- 이 저장소는 VST3 바인딩/런타임 계층, DSP 라이브러리, 쇼케이스 플러그인, 웹 렌더링 에디터 셸, 그리고 그 셸을 지원하는 Windows 번들/빌드 하네스를 배포합니다.
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

이 프로젝트는 MIT 라이선스로 배포됩니다([`LICENSE`](LICENSE)). 함께 포함된 서드파티 코드는 각자의 라이선스를 따릅니다.

- `include/vst3`: VST3 C API, BSD 계열 라이선스([`include/vst3/LICENSE.txt`](include/vst3/LICENSE.txt))
- `include/clap`: CLAP, MIT([`include/clap/LICENSE`](include/clap/LICENSE))
- `include/lv2`: LV2, ISC([`include/lv2/LICENSE`](include/lv2/LICENSE))
- `include/ara`: ARA API, Apache 2.0([`include/ara/LICENSE.txt`](include/ara/LICENSE.txt))
- `pkg/standalone/miniaudio.h`: miniaudio, 퍼블릭 도메인 또는 MIT-0(파일 끝에 명시)

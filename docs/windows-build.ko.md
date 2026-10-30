[English](windows-build.md) | 한국어

# Windows 빌드 참고 사항

이 저장소에는 이제 Windows 에디터 뷰 스캐폴드와 `GetPluginFactory` export 경로가 있지만, Windows 패키징을 위해서는 여전히 사용하는 쪽 빌드에서 host 툴체인과 번들 레이아웃을 연결해야 합니다.

실제 host 검증 체크리스트와 필요한 증빙 자료는 `docs/windows-validation.md`([한국어](windows-validation.ko.md))를 참고하세요.

## 필요한 구성 요소

- Windows CGO 툴체인
- Microsoft WebView2 SDK 헤더와 로더 라이브러리
- 대상 머신에 설치된 WebView2 런타임
- 비 Windows host에서는 빌드 스크립트가 컴파일러가 `<windows.h>`와 `<WebView2.h>`를 전처리하고 `ole32`와 `user32`를 링크할 수 있는지 사전 점검합니다.

## 현재 계약

- `pkg/plugin/windows_dll_windows.c`는 최소한의 `DllMain`을 제공합니다.
- `pkg/plugin/vst3_bridge.c`는 Windows에 맞는 심볼 어노테이션으로 `GetPluginFactory`, `InitDll`, `ExitDll`을 export합니다.
- `pkg/plugin/editor_view_windows.c`는 WebView2를 통해 에디터를 호스팅합니다.
- `pkg/plugin/windows_bundle.go`는 생성된 헤더 사이드카를 포함한 표준 번들 경로 레이아웃을 정의합니다.
- `pkg/plugin/editor_assets/`는 기본 HTML/CSS/JS 에디터 페이지를 제공합니다.
- `cmd/vst3go-dll/main_windows.go`는 `amd64`용 Windows DLL을 빌드하기 위한 `c-shared` 엔트리포인트 스캐폴드입니다.

## 기대하는 번들 형태

- VST3를 사용하는 쪽은 빌드된 라이브러리를 host가 인식하는 일반적인 플러그인 번들 레이아웃에 배치해야 합니다.
- Windows host 측 패키지는 DLL과 함께 DLL이 기대하는 WebView2 로더/런타임 의존성을 배포해야 합니다.
- 에디터 브리지는 플러그인 DLL 안에 있어야 하며, 웹 에셋은 바이너리에 임베디드된 상태로 유지됩니다.
- 이 저장소는 이미 빌드된 Windows DLL로부터 번들을 조립하는 `scripts/package_windows_vst3.sh`, `just windows-package`, `just windows-build`를 제공하며, 이때 생성된 헤더 사이드카가 필요합니다.
- 이 저장소는 생성된 헤더 사이드카를 포함해 결과 레이아웃을 검증하는 `scripts/check_windows_vst3.sh`와 `just windows-check-bundle`도 제공합니다.
- 이 저장소는 Windows 툴체인이 있을 때 DLL을 빌드하는 `scripts/build_windows_vst3.sh`와 `just windows-build-dll`도 제공합니다.
- 이 저장소는 빌드 전에 Windows 컴파일러, WebView2 헤더, 로더 라이브러리를 자동으로 선택하거나 검증하는 `scripts/select_windows_cc.sh`, `scripts/preflight_windows_vst3.sh`, `just windows-preflight`도 제공합니다. 선택에 실패하면 헬퍼가 어떤 후보를 시도했고 각각 왜 거부되었는지 출력합니다.
- `just windows-smoke`는 가짜 툴체인 shim으로 selector, preflight, 빌드, 패키징, 번들 검사 스크립트를 실행하여 비 Windows host에서도 회귀를 잡을 수 있게 합니다.
- `just windows-release`는 preflight를 한 번 실행한 뒤 빌드와 레이아웃 검사를 함께 실행합니다.

## 빌드 기대 사항

- Linux에서의 크로스 컴파일은 Windows C 툴체인과 WebView2 헤더가 있을 때에만 가능합니다.
- `scripts/docker_build_windows.sh [out]`(`just docker-build-windows`)는 어떤 Docker host에서든 이를 수행합니다. mingw-w64와 WebView2 NuGet 패키지(`WEBVIEW2_VERSION`, `vst3go-windows-cross` 볼륨에 캐시됨)를 설치하고, `windows/amd64`에 대해 `go vet`을 실행하고, 모든 검증용 플러그인을 `.vst3` DLL로 빌드한 뒤 이를 `<name>.clap`으로 복사하며(Windows에서 CLAP 플러그인은 일반 DLL입니다), `GetPluginFactory`/`InitDll`/`ExitDll`/`clap_entry` export를 확인하고, 각 플러그인을 스탠드얼론 애플리케이션(`<name>.exe`, console 서브시스템, miniaudio와 `internal/midiport`를 통한 WASAPI/WinMM)으로 빌드합니다. 플러그인이나 애플리케이션을 실행하지는 않습니다.
- WebView2 에디터는 `ole32`와 `user32`를 링크합니다. `WebView2Loader.dll`은 에디터를 열 때 플러그인 바이너리 폴더(번들이 이 파일을 넣어 두는 곳입니다. host는 플러그인을 `LoadLibraryW`로 로드하는데, 이 함수는 그 폴더를 찾지 않습니다)에서, 없으면 일반 검색 경로에서 로드합니다. 그래서 로더가 없으면 에디터만 쓸 수 없습니다.
- 크로스 빌드는 `moduleinfo.json`을 생성하지 않습니다. 이 파일을 만들려면 대상 플랫폼에서 바이너리를 로드해야 합니다.
- 현재 Windows 패키징은 `amd64`와 `x86_64-win` 번들 레이아웃을 대상으로 합니다.
- Windows 스크립트는 상대 경로로 주어진 번들 루트, 번들 입력, 빌드 헬퍼를 저장소 루트 기준으로 해석하므로 어느 작업 디렉터리에서든 호출할 수 있습니다.
- 저장소 수준의 Go 테스트는 계속해서 현재 플랫폼에서 공용 런타임 코드를 검증합니다.
- Windows 전용 패키징 검증은 실제 Windows 툴체인에서 수행해야 합니다.

## Windows 위험 요소

- 공유 라이브러리 로딩 동작은 host마다 다를 수 있으며, 특히 플러그인을 반복해서 다시 로드할 때 그렇습니다.
- 스케줄러와 메시지 루프 타이밍 때문에 WebView2의 포커스나 크기 조절 버그가 간헐적으로 보일 수 있습니다.
- Windows에서는 파일 잠금이 더 엄격한 경우가 많으므로, 다시 패키징하기 전에 생성된 DLL을 클린 빌드해야 할 수 있습니다.
- 실제 플러그인 설치 위치에서의 경로 길이 엣지 케이스도 여전히 확인할 가치가 있습니다.
- WebView2 host는 다시 로드할 때와 프로필 잠금으로 인한 마찰을 줄이기 위해 에디터마다 임시 사용자 데이터 폴더를 사용하며, 가능하면 해제 시 그 폴더를 정리합니다.
- WebView2 host는 플러그인답게 동작하도록 기본 컨텍스트 메뉴, devtools, 상태 표시줄을 비활성화한 상태로 유지합니다.

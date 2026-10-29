[English](windows-validation.md) | 한국어

# Windows 검증 체크리스트

이 문서는 실제 Windows 검증을 위한 인계용 체크리스트입니다. 이 저장소는 이미 Go 테스트로 공용 런타임의 정확성을, 비 Windows host에서의 스크립트 수준 Windows 스모크 테스트로 기본적인 Windows 경로를 검증하고 있습니다. 남은 작업은 실제 Windows 툴체인, 번들, WebView2 기반 에디터가 실제 Windows 머신에서 올바르게 동작하는지 확인하는 것입니다.

바로 채워 넣을 수 있는 실행 기록이 필요하면 `docs/windows-validation-report-template.md`([한국어](windows-validation-report-template.ko.md))를 사용하세요.
현재 host 메타데이터가 미리 채워진 실행 기록을 만들려면 `just windows-init-report`를 실행합니다.
Windows 머신에서 전체 절차를 실행하고 결과를 기록하려면 `just windows-validate`를 사용합니다.
이 러너는 첫 번째로 실패한 단계에서 중단하며, 완료된 단계 목록, 로그 디렉터리, 실패 지점을 리포트에 기록합니다.
요약된 실행 경로는 `docs/windows-validation-quickstart.md`([한국어](windows-validation-quickstart.ko.md))를 참고하세요.

## 검증 목표

- `just windows-preflight`가 사용 가능한 Windows CGO 컴파일러를 선택하거나 받아들이는지 입증합니다
- `just windows-build-dll`이 DLL과 생성된 헤더 사이드카를 만드는지 입증합니다
- `just windows-build`와 `just windows-check-bundle`이 기대한 `x86_64-win` 번들 레이아웃을 만드는지 입증합니다
- 최소 하나 이상의 실제 Windows VST3 host에서 플러그인이 로드되는지 입증합니다
- 임베디드 웹 에디터가 열리고, 파라미터를 갱신하며, 반복해서 다시 열어도 문제가 없는지 입증합니다

## 최소 환경

- `amd64` 기반 Windows 10 이상
- 저장소가 지원하는 버전과 일치하는 Go 툴체인
- `PATH`에 있는 MinGW-w64 툴체인
- 빌드에서 사용할 수 있는 Microsoft WebView2 SDK 헤더와 로더 라이브러리
- 테스트 머신에 설치된 WebView2 런타임
- 스모크 검증용 VST3 host 최소 하나

## 필수 명령

저장소 루트에서 다음을 실행합니다:

```bash
just windows-init-report
go test -timeout=30000s ./...
just windows-preflight
just windows-build-dll
just windows-build dist/windows/vst3go.dll
just windows-check-bundle
just windows-validate
```

DLL이 다른 경로에 생성되면 그 경로를 `just windows-build`에 명시적으로 전달합니다.

## 빌드와 패키징 점검

다음을 모두 확인합니다:

- `just windows-preflight`가 헤더 누락이나 로더 누락 오류 없이 성공함
- 생성된 DLL이 존재함
- 생성된 C 헤더 사이드카가 DLL 옆에 존재함
- 패키징된 번들이 기대한 `Contents/x86_64-win` 레이아웃을 사용함
- 패키징된 번들에 플러그인 DLL과 `WebView2Loader.dll`이 포함됨
- `just windows-check-bundle`이 성공적으로 종료됨
- `just windows-validate`가 완료 및 실패 단계 목록이 담긴 리포트 파일을 작성함

## Host 검증 점검

생성된 `.vst3` 번들을 Windows VST3 위치에 설치하고 다음을 확인합니다:

- host가 로더 크래시 없이 플러그인을 발견함
- 플러그인을 트랙에 삽입할 수 있음
- 오디오 처리가 즉각적인 불안정 없이 시작됨
- 에디터 창이 정상적으로 열림
- WebView2 기반 UI가 임베디드 HTML/CSS/JS 셸을 렌더링함
- UI의 파라미터 변경이 프로세서에 전달됨
- host가 주도하는 파라미터 오토메이션이 UI에 다시 반영됨
- 에디터를 반복해서 열고 닫아도 플러그인이 사용 불가능해지지 않음
- 플러그인을 언로드하고 다시 스캔해도 예기치 않은 파일 잠금 때문에 실패하지 않음

## 웹 에디터 집중 점검

Windows 에디터 host가 가장 큰 플랫폼 특화 위험 요소이므로, 다음을 명시적으로 확인합니다:

- host 스캔 후 첫 번째 열기
- 크기 조절 동작
- host와 에디터 간 포커스 전환
- 에디터를 닫은 뒤 다시 열기
- 한 프로젝트에서 여러 플러그인 인스턴스 생성
- host 프로젝트를 저장하고 다시 연 뒤의 상태 복원

## 산출물 수집

검증 실행과 함께 다음 산출물을 보관합니다:

- 정확한 Windows 버전
- Go 버전
- MinGW-w64 컴파일러 버전
- WebView2 SDK 버전 또는 설치 출처
- WebView2 런타임 버전
- host 이름과 정확한 host 버전
- `just windows-preflight`의 터미널 출력
- `just windows-build-dll`의 터미널 출력
- `just windows-check-bundle`의 터미널 출력
- 에디터가 정상적으로 열리는 스크린샷 또는 영상
- 검증이 실패한 경우 크래시 로그나 host 진단 정보

## 통과 기준

다음 조건을 만족하면 MinGW-w64 마일스톤에 대한 Windows 검증 작업이 완료된 것입니다:

- 모든 필수 명령이 실제 Windows 머신에서 성공함
- 패키징된 번들이 `just windows-check-bundle`을 통과함
- 최소 하나 이상의 Windows VST3 host에서 플러그인이 로드되고 에디터가 열림
- 임베디드 웹 에디터가 파라미터를 올바르게 갱신함
- 기본적인 삽입, 다시 열기, 저장, 다시 로드 흐름을 막는 재현 가능한 크래시가 없음

MinGW-w64 검증이 안정화되고 구체적인 호환성 요구가 생기기 전까지 MSVC는 범위 밖으로 둘 수 있습니다.

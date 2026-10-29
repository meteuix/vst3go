[English](windows-validation-quickstart.md) | 한국어

# Windows 검증 퀵스타트

실제 Windows `amd64` 머신에서 검증까지 가장 짧은 경로가 필요할 때 이 페이지를 사용합니다.

## 1분 경로

1. `bash`와 저장소 도구를 제공하는 셸을 엽니다.
2. 저장소 루트로 `cd` 합니다.
3. `just windows-init-report`를 실행합니다.
4. `just windows-preflight`를 실행합니다.
5. `just windows-validate`를 실행합니다.
6. 생성된 리포트를 열고 수동 확인이 필요한 host 검증 항목을 채웁니다.

## 기대 결과

- `just windows-preflight`는 Windows 컴파일러, 헤더, 로더를 사용할 수 있는지 확인합니다.
- `just windows-validate`는 리포트 파일을 작성하며, 누락된 것이 있으면 첫 번째 실패 지점에서 중단합니다.
- 검증에 통과하면 리포트에 로그 디렉터리와 완료된 단계 목록이 포함됩니다.

## 실패했을 때

- 컴파일러가 없으면 MinGW-w64를 설치한 뒤 `just windows-preflight`를 다시 실행합니다.
- `WebView2.h` 또는 `WebView2Loader`가 없으면 WebView2 SDK를 설치한 뒤 다시 시도합니다.
- host가 플러그인을 로드하지 못하면 리포트를 보관하고 host 로그와 크래시 상세 정보를 첨부합니다.

## 필수 증빙 자료

- Windows 버전
- Go 버전
- MinGW-w64 버전
- WebView2 SDK 버전 또는 출처
- WebView2 런타임 버전
- host 애플리케이션 이름과 버전
- `just windows-validate`로 생성된 리포트

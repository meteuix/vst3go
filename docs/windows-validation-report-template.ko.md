[English](windows-validation-report-template.md) | 한국어

# Windows 검증 리포트 템플릿

`docs/windows-validation.md`([한국어](windows-validation.ko.md))의 실제 Windows 검증 흐름을 실행할 때 이 템플릿을 사용합니다.

## 요약

- 날짜:
- 검증자:
- 결과: pass / fail / blocked
- 범위: MinGW-w64 / host load / editor reopen / save-restore

## 환경

- Windows 버전:
- CPU 아키텍처:
- Go 버전:
- MinGW-w64 버전:
- WebView2 SDK 버전 또는 출처:
- WebView2 런타임 버전:
- Host 애플리케이션 이름:
- Host 애플리케이션 버전:

## 명령

### `go test -timeout=30000s ./...`

- 결과:
- 비고:

```text
paste output here
```

### `just windows-preflight`

- 결과:
- 비고:

```text
paste output here
```

### `just windows-build-dll`

- 결과:
- 출력 DLL 경로:
- 생성된 헤더 경로:
- 비고:

```text
paste output here
```

### `just windows-build dist/windows/vst3go.dll`

- 결과:
- 번들 경로:
- 비고:

```text
paste output here
```

### `just windows-check-bundle`

- 결과:
- 비고:

```text
paste output here
```

## 번들 검증

- `Contents/x86_64-win`에 DLL이 존재함: yes / no
- 생성된 헤더 사이드카가 존재함: yes / no
- `WebView2Loader.dll`이 존재함: yes / no
- 번들 레이아웃이 기대와 일치함: yes / no

## Host 검증

- host에서 플러그인이 발견됨: yes / no
- 트랙에 플러그인이 삽입됨: yes / no
- 오디오 처리가 시작됨: yes / no
- 에디터가 열림: yes / no
- 임베디드 웹 UI가 렌더링됨: yes / no
- UI의 파라미터 변경이 프로세서에 전달됨: yes / no
- host 오토메이션이 UI에 반영됨: yes / no
- 닫은 뒤 다시 열기가 동작함: yes / no
- 여러 인스턴스가 동작함: yes / no
- 프로젝트를 저장하고 다시 열면 상태가 복원됨: yes / no
- 다시 스캔하거나 다시 로드해도 파일 잠금 문제가 발생하지 않음: yes / no

## 증빙 자료

- 스크린샷 경로:
- 영상 경로:
- 크래시 로그 경로:
- 추가 비고:

## 이슈

### 이슈 1

- 제목:
- 재현 절차:
- 기대 결과:
- 실제 결과:
- 심각도:
- 첨부 파일:

### 이슈 2

- 제목:
- 재현 절차:
- 기대 결과:
- 실제 결과:
- 심각도:
- 첨부 파일:

## 최종 결정

- MinGW-w64 검증 완료됨: yes / no
- Windows를 릴리스 범위에 유지할 준비가 됨: yes / no
- 후속 작업:

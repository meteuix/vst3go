[English](release-checklist.md) | 한국어

# 릴리스 체크리스트

새 `vst3go` 릴리스에 태그를 달기 전에 이 체크리스트를 사용합니다.

## 범위 결정

- 릴리스가 여전히 `README.md`에 명시된 저장소 경계와 일치하는지 확인합니다
- 제품 전용 로직(라이선스, 인스톨러, 브랜딩)이 이 저장소로 흘러 들어오지 않았는지 확인합니다
- 의도한 릴리스 범위가 여전히 VST3 바인딩/런타임 계층, DSP 라이브러리, 쇼케이스 플러그인, 웹 렌더링 에디터 셸, Windows 빌드 하네스인지 확인합니다

## 런타임 검증

- `go build ./...`를 실행합니다
- `go test -timeout=30000s ./...`를 실행합니다
- `go test -race -p 1 -timeout=30000s ./...`를 실행합니다
- `git diff --check`가 통과하는지 확인합니다

## Windows 검증

- 해당되는 경우 현재 비 Windows 개발 환경에서 `bash scripts/windows_toolchain_smoke.sh`를 실행합니다
- 가장 최근의 실제 Windows 검증이 `docs/windows-validation.md`를 따랐는지 확인합니다
- 대상 릴리스 후보에 대해 작성이 완료된 `docs/windows-validation-report-template.md` 기록이 있는지 확인합니다
- Windows가 릴리스 범위에 남아 있다면 실제 Windows 머신에서 MinGW-w64 검증이 통과했는지 확인합니다

## 문서

- `README.md`가 여전히 현재 패키지 범위와 릴리스 범위를 반영하는지 확인합니다
- `docs/runtime-contracts.md`가 여전히 런타임 동작과 일치하는지 확인합니다
- `docs/web-editor-bridge.md`가 여전히 에디터 셸 계약과 일치하는지 확인합니다
- `docs/windows-build.md`와 `docs/windows-validation.md`가 여전히 현재 스크립트 및 번들 흐름과 일치하는지 확인합니다
- `PLAN.md`가 남은 작업을 정확히 반영하는지 확인합니다

## 사용자(consumer) 준비 상태

- 모듈 경로가 여전히 `github.com/meteuix/vst3go/v2`인지 확인합니다
- 지원하는 다운스트림 API 패키지가 태그를 달 만큼 충분히 안정적인지 확인합니다
- 의도적으로 호환성을 깨는 변경 사항이 릴리스 노트에 명시되어 있는지 확인합니다

## 릴리스 산출물

- 릴리스 버전 또는 태그 후보를 기록합니다
- 검증에 사용한 정확한 Go 버전을 기록합니다
- 태그를 달 정확한 커밋 SHA를 기록합니다
- Windows가 포함된다면 최신 Windows 검증 산출물을 수집합니다

## Go / No-Go

다음 조건을 만족하면 릴리스 준비가 된 것입니다:

- 런타임 검증이 모두 통과함
- 문서가 배포되는 동작과 일치함
- Windows가 범위에 포함된 릴리스라면 Windows 증빙 자료가 있음
- 지원 API 범위에 알려진 차단 이슈가 남아 있지 않음

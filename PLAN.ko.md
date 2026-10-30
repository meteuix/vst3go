[English](PLAN.md) | 한국어

# VST3Go TODO

## 저장소 경계

- [x] VST3 바인딩/런타임 계층, DSP 라이브러리, 쇼케이스 플러그인을 이 저장소에 둡니다.
- [x] 모듈 이름을 `github.com/th-release/vst3go/v2`로 바꿉니다.
- [x] 이 포크가 원본 프로젝트와 의도적으로 다르다는 점을 README에 명시합니다.
- [x] 유지하는 패키지 경계를 한 번 더 검토합니다.
  - [x] `pkg/vst3`
  - [x] `pkg/plugin`
  - [x] `pkg/midi`
  - [x] `pkg/framework/{bus,param,plugin,process,state}`
  - [x] `pkg/dsp/...`, `pkg/framework/{dsp,voice,debug}`
  - [x] 제품 전용 코드(인스톨러, 라이선스, 브랜딩)는 사용하는 쪽 프로젝트에 남아 있는지 확인합니다.

## 규칙

- [x] C 브리지를 최소한으로, 직접적으로 유지합니다.
- [x] 비즈니스 로직은 C가 아니라 Go에 둡니다.
- [x] DSP는 메모리 할당 없이 `ProcessAudio`에서 안전하게 호출할 수 있도록 유지합니다.
- [x] 쇼케이스 플러그인은 `examples/`에 두고, 각각 오프라인 테스트(`pkg/plugintest`)를 갖춥니다.
- [x] 제품별 로직은 이 저장소에 두지 않습니다.
- [x] 이 규칙들을 문서와 향후 계획 업데이트에 반영된 상태로 유지합니다.

## 현재 기준선

- [x] 플러그인 구현은 `pkg/plugin`만 import하며, C 브리지는 그 패키지의 일부로 컴파일됩니다.
- [x] 버스, 파라미터, 처리 컨텍스트, 상태, MIDI/이벤트, 플러그인 런타임, VST3 바인딩을 위한 핵심 런타임 패키지가 있습니다.
- [x] 샘플 단위로 정확한 파라미터 오토메이션이 구현되어 있습니다.
- [x] MIDI/이벤트 처리가 구현되어 있습니다.
- [x] 고급 버스 지원이 구현되어 있습니다.
- [x] 파라미터 ID는 `ParamXxx` 명명 규칙을 따르는 타입이 지정된 `uint32` 상수로 표준화되어 있습니다.
- [x] 대부분의 런타임 리셋 경로가 비활성화 시점에 갖춰져 있습니다.
- [x] 분리와 모듈 이름 변경 이후에도 `go test ./pkg/...`가 통과합니다.
- [x] 남아 있는 브리지 경고를 정리합니다.
  - [x] `bridge.c` / `bridge.h`의 `GoCreateInstance` 주변 `const` 한정자 불일치를 해결합니다.

## 1단계: 런타임 API 정리

- [x] 명명 불일치를 해결합니다.
  - [x] `ResultOK`를 표준으로 삼고 별칭은 호환성 용도로만 남겨 중복된 상수 철자와 대소문자 변형을 없앱니다.
  - [x] 명시적인 `GetNormalized`/`SetNormalized` 및 `GetPlain`/`SetPlain` 접근자를 추가하여 normalized/plain 파라미터 명명의 의미를 표준화합니다.
  - [x] `Stereo`, `Mono`, `EffectStereo`, `Generator`, `MIDIEffect` 같은 기본 생성자를 추가하여 표준 버스 구성 진입점을 정립합니다.
- [x] 인터페이스 계층 구조를 명확히 합니다.
  - [x] 플러그인, 프로세서, 선택적 stateful 프로세서 동작 간의 관계를 코드 주석으로 문서화합니다.
  - [x] 선택적 인터페이스를 암묵적이 아니라 명시적으로 유지합니다.
- [x] 파라미터와 레지스트리 API를 다듬습니다.
  - [x] 유지하는 런타임 코드에서 명백히 안전하지 않은 파라미터 접근 패턴을 점검하고 정리합니다.
  - [x] `GetOK`, `Has`, `GetNormalized`, `GetPlain` 같은 안전한 레지스트리 헬퍼를 추가하여, nil이 가능한 곳에서는 검사를 거친 접근을 우선합니다.
  - [x] `Registry.Add`가 중복 ID에 대해 명시적으로 실패하도록 하여 중복 등록 동작과 실패 방식을 명확히 합니다.
- [x] 오류 처리를 다듬습니다.
  - [x] 상태/레지스트리 계층에서 일반적인 오류 반환이 적절한 곳의 조용한 실패를 대체합니다.
  - [x] 상태 저장/로드와 핵심 런타임 설정 경로에서 유용한 컨텍스트로 오류를 감쌉니다.

## 2단계: 런타임 아키텍처 강화

- [x] 패키지 경계를 재평가합니다.
  - [x] VST3와 맞닿은 코드, 브리지 글루, 범용 런타임 기본 요소를 명확히 분리합니다.
  - [x] 저수준과 상위 수준 관심사가 얽힌, 책임이 섞인 코드를 제거합니다.
- [x] 라이프사이클 동작을 검토합니다.
  - [x] 유지하는 런타임 코드에서 `SetActive(false)`와 관련 리셋 동작을 점검합니다.
  - [x] 유지하는 런타임 경로에서 상태, 파라미터, 이벤트 처리가 결정적으로 리셋되도록 보장합니다.
- [x] 처리 계층 구조를 검토합니다.
  - [x] 가독성이 떨어지는 지나치게 큰 런타임 처리 함수를 리팩터링합니다.
  - [x] 트랜스포트 갱신, 버퍼 매핑, 오토메이션 수집을 더 작은 런타임 헬퍼로 분리합니다.
- [x] 헬퍼 추상화를 정직하게 유지합니다.
  - [x] 핵심 VST3 메커니즘을 숨기지 않으면서 보일러플레이트를 줄이는 경우에만 기본 헬퍼를 추가합니다.

## 3단계: 테스트, 검증, 성능

- [x] 유지하는 패키지의 자동화 테스트 범위를 넓힙니다.
  - [x] `pkg/framework/param`
  - [x] `pkg/framework/process`
  - [x] `pkg/framework/state`
  - [x] `pkg/midi`
  - [x] 실용적인 범위에서 `pkg/plugin`
  - [x] `pkg/vst3`
- [x] 스레드 안전성 가정이 단순하지 않은 곳에 race detector 검사를 추가합니다.
- [x] 메모리 할당에 민감한 경로를 측정 가능하게 유지합니다.
  - [x] 처리 컨텍스트 내부를 벤치마크합니다.
  - [x] 유용한 경우 런타임 핫 패스를 벤치마크합니다.
  - [x] 제한이 너무 경직될 수 있는 고정 크기 내부 버퍼를 다시 검토합니다.
- [x] 검증 기대 사항을 명시적으로 유지합니다.
  - [x] 런타임 계층 자체에 대한 최소 검증 기대 사항을 정의합니다.
  - [x] 다운스트림 저장소가 스스로 검증해야 하는 항목을 문서화합니다.

## 4단계: 문서와 사용자 경험

- [x] 유지하는 범위를 명확히 문서화합니다.
  - [x] README와 PLAN에 분리 경계를 명시합니다.
- [x] API 문서를 개선합니다.
  - [x] 패키지 수준 문서
  - [x] 라이프사이클 기대 사항
  - [x] 스레드 안전성 보장
  - [x] 상태/영속성 기대 사항
- [x] 포크에 대한 마이그레이션 가이드를 추가합니다.
  - [x] 원본 프로젝트와의 의도적인 차이를 명확히 합니다.
  - [x] 새 모듈 경로를 명확히 합니다.
  - [x] 버전에 태그가 붙은 뒤 다운스트림 저장소가 이 포크를 어떻게 사용해야 하는지 문서화합니다.
- [x] 유지하는 런타임 저장소의 빌드 및 검증 지침을 정확하게 유지합니다.

## 5단계: 웹 렌더링 플러그인 UI

- [x] 에디터 범위와 렌더링 범위를 정의합니다.
  - [x] 현재 플러그인 UI 진입점과 `createView`가 노출해야 할 내용을 파악합니다.
  - [x] 어떤 컨트롤, 레이아웃 블록, 파라미터 바인딩을 먼저 렌더링해야 하는지 결정합니다.
- [x] 웹 기반 화면으로 에디터를 렌더링할 수 있게 합니다.
  - [x] 독립된 웹사이트 대신 VST 에디터를 위한 웹 렌더링 뷰 경로를 추가합니다.
  - [x] 렌더링된 UI를 현재 플러그인 디자인 및 컨트롤과 일치시킵니다.
  - [x] 로컬 편집과 파라미터 변경이 뷰에 즉시 보이게 합니다.
  - [x] 저장/복원과 향후 React 하이드레이션을 위한 명시적인 에디터 스냅샷을 유지합니다.
  - [x] HTML, CSS, JS 빌드 결과물이 에디터 셸에 연결되는 방식을 문서화합니다.
- [x] 웹 렌더링 에디터를 플랫폼 확장 작업의 선행 조건으로 취급합니다.

## 6단계: 크로스 플랫폼 Windows 지원

- [x] Windows용으로 빌드 시스템을 갱신합니다.
  - [x] Windows를 깔끔하게 감지합니다.
  - [x] 올바른 라이브러리 형식과 번들 레이아웃을 생성합니다.
- [x] Windows 진입점을 위해 브리지를 확장합니다.
  - [x] 필요한 DLL 진입 처리를 추가합니다.
  - [x] `GetPluginFactory`를 export합니다.
  - [x] Windows에서 WebView2로 웹 렌더링 에디터를 호스팅합니다.
  - [x] Windows 에디터 뷰 스캐폴드를 마련합니다.
- [x] 플랫폼별 CGO 지시문을 검토합니다.
  - [x] Windows 전용 플래그는 필요한 곳에만 추가합니다.
  - [x] 플랫폼 간 차이를 최소화합니다.
- [ ] 실제 툴체인과 host에서 검증합니다.
  - [x] MinGW-w64부터 시작합니다: 크로스 빌드, Wine에서 실행하는 hostcheck, clapcheck,
        스탠드얼론 애플리케이션(`docker_check_windows_wine.sh`).
  - [ ] 실제 Windows host에서 실행합니다(`docs/windows-validation.md`).
  - [ ] MSVC는 타당한 경우 이후의 개선 사항으로 둡니다.
- [x] Windows 특유의 위험 요소를 기록합니다.
  - [x] Go와 함께 쓸 때의 공유 라이브러리 동작.
  - [x] 스케줄러 차이.
  - [x] 파일 잠금과 다시 로드할 때의 마찰.
  - [x] 경로 길이 엣지 케이스.

## 7단계: 릴리스 준비

- [x] 아키텍처 가드레일이 지켜지도록 유지합니다.
- [x] 유지하는 런타임 패키지를 테스트되고 문서화된 상태로 유지합니다.
- [x] 공개 API 경계가 다운스트림 저장소에 충분히 안정적인지 확인합니다.
- [x] 지원하는 릴리스 범위를 명시적으로 결정합니다.
  - [x] Linux/macOS만 지원할지, Windows도 포함할지.
  - [x] 런타임과 함께 DSP 라이브러리와 쇼케이스 플러그인을 포함.
- [x] 반복 가능한 태그 전 릴리스 체크리스트를 마련합니다.

## 8단계: 프레임워크 동등성

DSP를 포함해, 플러그인 작업에서 JUCE를 대체할 수 있는 플러그인 프레임워크로 런타임을
키웁니다.

- [x] 파라미터 스무딩(`param.Ramp`, `ctx.Smoothed`)과 host 편집 제스처.
- [x] `.vstpreset` 파일과 웹 에디터의 프리셋 브라우저(`pkg/preset`).
- [x] host 없이 플러그인을 테스트하기 위한 오프라인 러너와 `pkg/plugintest`.
- [x] 같은 바이너리에서 제공하는 CLAP 플러그인 포맷(`clap_entry`). macOS와 Linux에서
      clap-validator와 `hostcheck/clapcheck.c`로 검증.
  - [x] 기존 웹 뷰 위의 CLAP 에디터(`gui` 확장). macOS와 Linux의 실제 창에서 검사
        (`editorcheck_darwin.m`, `editorcheck.c`).
  - [x] 사용자 `.vstpreset` 폴더를 위한 CLAP preset discovery와 `preset-load`
        (clap-validator `preset-discovery-*`, clapcheck).
- [x] 웹 에디터용 JavaScript SDK(`window.vst3go`): 크기 제한과 `vst3go.resize`를 지원하는
      커스텀 에디터 페이지(`EditorProcessor`), 편집 제스처, 프리셋, 프로세서와의 커스텀
      메시지, 타입이 지정된 TypeScript 파라미터 바인딩(`plugin.EditorBindings`). node/tsc와
      macOS 및 Linux의 실제 웹 뷰에서 검사.
- [x] 스탠드얼론 애플리케이션 래퍼(`pkg/standalone`): 플러그인 자체의 CLAP 엔트리를 통해
      호스팅되며, miniaudio 장치 I/O, Core MIDI/ALSA/WinMM 포트(`internal/midiport`), 창에
      띄운 에디터, 실행 간에 유지되는 설정과 상태를 제공. null 장치, 가상 MIDI 왕복,
      macOS와 Linux(Xvfb)의 에디터 창으로 검사.
- [x] macOS의 AUv2 래퍼(`au_darwin.c`): 모든 Audio Unit이 플러그인의 CLAP 엔트리를
      호스팅하며, 파라미터, ClassInfo 상태, 프로그램에서 만든 팩토리 프리셋, MIDI 입출력,
      SysEx, 예약된 파라미터, host 트랜스포트, 레이턴시/테일, 사이드체인, Cocoa 에디터를
      지원. `auval -strict`와 `hostcheck/aucheck_darwin.m`을 통과.
- [x] `vst3go` 명령(`go tool vst3go`): `new`는 테스트를 포함한 플러그인 모듈(이펙트, 악기,
      MIDI 이펙트)을 스캐폴딩하고, `build`는 임의 모듈의 패키지에 대해 모든 포맷(VST3,
      CLAP, Audio Unit, 스탠드얼론 앱)을 기록하고 사용자 플러그인 폴더에 설치.
- [x] Linux의 LV2 플러그인 포맷(`lv2_linux.c`, `lv2ui_linux.c`): 모든 LV2 플러그인이
      플러그인의 CLAP 엔트리를 호스팅하며, 포트, 단위, scale point, 프로그램에서 만든 프리셋,
      상태, MIDI 및 트랜스포트 atom, 레이턴시, 사이드체인, X11 에디터 UI를 담은 Turtle을
      생성. lv2_validate, lv2lint, `hostcheck/lv2check.c`(lilv)를 통과.
- [x] 오디오 파일(`pkg/audiofile`): 스트리밍, 탐색, 루프, 루트 노트를 지원하는 WAV, AIFF,
      FLAC, Ogg Vorbis, MP3 디코딩, WAV, AIFF, FLAC 인코딩,
      `plugintest.LoadAudio`/`SaveAudio`. afconvert, flac, oggenc, lame으로 만든 파일과
      각 디코더로 검사.
- [x] DSP 라이브러리(`pkg/dsp`: 필터, 다이내믹스, 딜레이, 모듈레이션, 리버브,
      디스토션, 오실레이터, 엔벨로프, 팬, 믹스, 보간, 분석), 프로세서 체인
      (`pkg/framework/dsp`), 보이스 할당(`pkg/framework/voice`), 디버그/프로파일링 헬퍼
      (`pkg/framework/debug`)와 그 단위 테스트.
- [x] 고급 DSP: FFT/STFT, 오버샘플링과 샘플 레이트 변환, 분할 컨볼루션, 피치 시프트,
      타임 스트레치와 피치 검출, 웨이브테이블과 FM 오실레이터, 그래뉼러 합성과 샘플 재생,
      각각 쇼케이스 플러그인 포함(`saturator`, `convreverb`, `pitchshifter`, `tuner`,
      `wavetablesynth`, `fmsynth`, `granulator`, `sampler`).
- [x] 파라미터 곡선(`Logarithmic`, `Center`), 위상이 맞춰진 Linkwitz-Riley 크로스오버와
      `multiband` 컴프레서, 스트리밍 타임 스트레치, 레이어·라운드 로빈·루프 모드를
      지원하는 샘플러의 SFZ 악기.
- [x] 데드라인 가드(기본으로 켜짐): 블록을 전용 실시간 스레드에서 실행해 Go가
      멈추더라도 host 오버런 대신 무음 블록 하나로 끝나게 하고(VST3, CLAP, AU, LV2,
      standalone), host의 오디오 스레드는 Go에 들어가지 않습니다(flush와 reset은 가드
      작업으로 실행, 들어가지 않는 입력은 다음 블록으로 넘김, LV2는
      `lv2:hardRTCapable`을 선언하고 `lv2:freeWheeling`을 따름, standalone 오디오
      콜백은 C). 모든 host 검사에서 `stall` 검증 플러그인으로 확인하며, Audio Unit
      렌더링은 메인 스레드를 기다리지 않습니다.
- [x] 가비지 컬렉션 여유분: 컬렉터가 Go의 힙 두 배 페이싱 대신 64 MB의 가비지가 쌓인
      뒤에 실행되고, 활성화할 때 첫 블록 전에 수집합니다(`plugin.SetGCHeadroom`).
- [x] 포맷별 빈틈: MIDI 2.0 입력(CLAP `midi2` dialect, AU MIDI 이벤트 리스트, 요청하는
      AU host로의 MIDI 2.0 출력), host와 협상하는 레이아웃(CLAP
      `configurable-audio-ports`와 `surround`, AU 채널 구성과 레이아웃 태그), 플로팅
      CLAP 에디터, 트랜스포트와 오디오·MIDI 설정 창이 있는 standalone 툴바
      (`plugin.Panel`).
- [x] 샘플러의 디스크 스트리밍(`sampler.Streamer`)과 Neural Amp Modeler 모델
      (`pkg/dsp/nam`, `examples/namamp`), NeuralAmpModelerCore의 출력과 대조해 검사.
- [x] macOS와 iOS용 AUv3 앱 확장(`-format auv3`, `-platform`, `-sign`), auval,
      `aucheck`, `auv3check`로 프로세스 밖과 iOS 시뮬레이터에서 검사.
      `vst3go.chooseFile`은 플랫폼의 파일 대화상자를 열고 고른 파일에 대한 샌드박스 접근을
      유지합니다.
- [x] VST3, CLAP, Audio Units를 통한 ARA 2(`pkg/ara`, `plugin.ARAPlugin`,
      `examples/araclip`), 최소 ARA host인 `aracheck`로 ARM과 x86(패킹된 구조체)에서 검사.
- [ ] AAX: 막힘. Avid SDK(개발자 계정)와 PACE 서명이 필요합니다
      (`docs/plugin-capabilities.ko.md`의 알려진 제한 사항 참고).
- [x] 현재 런타임에 맞춘 `examples/`의 쇼케이스 플러그인(`standalone.Main`, 메모리 할당 없는
      처리, `ctx.SetOutputParameter`를 통한 읽기 전용 미터), 각각 오프라인 `pkg/plugintest`
      테스트 포함.

## 지속 규칙

- [ ] 별도의 로드맵 문서를 만들지 말고 계획은 이곳으로 다시 합칩니다.
- [ ] 새 추상화를 추가하기 전에 그것이 `vst3go`에 속하는지 사용하는 쪽 프로젝트에 속하는지 확인합니다.

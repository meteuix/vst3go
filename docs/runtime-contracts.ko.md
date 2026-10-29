[English](runtime-contracts.md) | 한국어

# 런타임 계약

이 저장소는 재사용 가능한 VST3 바인딩과 런타임 계층, `ProcessAudio` 안에서 쓰는 DSP 라이브러리, 그리고 이 둘 위에 만든 쇼케이스 플러그인을 제공합니다. 제품 전용 로직(라이선스, 인스톨러, 브랜딩)은 사용하는 쪽 프로젝트에 둡니다.

## 패키지 범위

다운스트림 프로젝트는 다음 패키지를 지원되는 API 범위로 취급해야 합니다:

- `pkg/plugin`
- `pkg/vst3`
- `pkg/midi`
- `pkg/framework/bus`
- `pkg/framework/param`
- `pkg/framework/plugin`
- `pkg/framework/process`
- `pkg/framework/state`
- `pkg/preset`
- `pkg/plugintest`
- `pkg/audiofile`
- `pkg/standalone`
- `pkg/dsp/...`
- `pkg/framework/dsp`
- `pkg/framework/voice`
- `pkg/framework/debug`

`pkg/plugin`의 C 브리지 소스, 임베디드 에디터 에셋, 플랫폼별 에디터 shim은 이 API 범위를 뒷받침하며 직접 호출하지 않습니다.

`pkg/dsp`의 DSP 프로세서는 오디오 스레드 밖(생성자나 `Initialize`)에서 만들고 크기를 정하며, 샘플/버퍼 단위 메서드는 메모리를 할당하지 않으므로 `ProcessAudio`에서 안전하게 호출할 수 있습니다. 각 인스턴스는 하나의 프로세서가 소유하며 동시에 사용하기에 안전하지 않습니다. 예외는 `pkg/dsp/analysis`입니다. 이 패키지의 미터와 스코프는 다른 스레드가 값을 읽을 수 있도록 뮤텍스로 상태를 보호하므로 `Process`가 락을 잡습니다. 오디오 스레드에서 값을 읽어 읽기 전용 출력 파라미터로 내보내거나, 읽는 쪽이 오디오 스레드를 잠깐 막을 수 있다는 점을 감수하세요.

## 라이프사이클 기대 사항

- `plugin.Plugin`은 팩토리입니다. `CreateProcessor`는 래퍼 인스턴스마다 새로운 프로세서 인스턴스를 반환해야 합니다.
- `plugin.Processor.Initialize`는 블록 크기와 샘플 레이트에 의존하는 상태를 준비합니다.
- `plugin.Processor.SetActive(true)`는 이후 메모리 할당에 민감한 추가 설정 없이도 프로세서가 실시간 작업을 수행할 수 있는 상태가 되도록 해야 합니다.
- `plugin.Processor.ProcessAudio`는 정상 동작 중에 반복 호출되는 오디오 콜백이며, 피할 수 있는 메모리 할당과 블로킹 작업은 피해야 합니다.
- `plugin.Processor.SetActive(false)`는 트랜스포트 상태, 대기 중인 이벤트, 스무딩 상태 및 그 밖의 런타임 전용 캐시를 리셋하는 경계입니다.
- `plugin.StatefulProcessor`는 선택 사항이며, 파라미터 값만으로는 프로세서 동작을 복원하기에 충분하지 않을 때에만 구현해야 합니다.
- `Initialize`는 host가 컴포넌트를 초기화할 때 한 번 실행되고, 이후 `setupProcessing`이 호출될 때마다 host의 샘플 레이트와 블록 크기로 다시 실행됩니다. 버스 배치는 `SetActive(true)`가 호출되는 시점에 확정됩니다.
- 선택적 host 기능(64비트 처리, 버스 배치, MIDI 매핑/learn, 노트 익스프레션, 키스위치, 프로그램, 유닛, 채널 정보, host 메시지)은 프로세서에서 선택적으로 구현하는 인터페이스이며, `docs/plugin-capabilities.md`에 설명되어 있습니다.
- `plugin.Register`는 플러그인마다 한 번씩 호출할 수 있으며, 하나의 바이너리가 여러 플러그인을 제공할 수 있습니다.
- `DistributedPlugin`에는 플러그인 인스턴스마다 프로세서 인스턴스가 두 개 생성됩니다. 프로세서 쪽은 위의 라이프사이클을 실행하고, 컨트롤러 쪽은 파라미터, 에디터, 컨트롤러 쿼리만 담당하며 `Initialize`, `SetActive`, `ProcessAudio`는 호출되지 않습니다.
- CLAP 인스턴스는 동일한 라이프사이클로 프로세서 하나를 실행합니다. CLAP `init`은 `Initialize`를 호출하고, `activate`는 host의 샘플 레이트와 최대 블록 크기로 `Initialize`를 다시 호출한 뒤 `SetActive(true)`를 호출하며, `deactivate`는 `SetActive(false)`를 호출합니다. 오디오 스레드에서 호출되는 CLAP `reset`은 재할당 없이 `SetActive(false)`와 `SetActive(true)`를 호출합니다. `DistributedPlugin`은 하나의 CLAP 객체로 실행됩니다.
- Audio Unit 인스턴스(macOS)는 CLAP 인스턴스 하나를 호스팅합니다. `AudioUnitInitialize`는 출력 샘플 레이트와 슬라이스당 최대 프레임 수로 이를 활성화하고, 그 이후의 첫 번째 렌더에서 처리가 시작됩니다. `AudioUnitUninitialize`는 이를 중지하고 비활성화하며, `AudioUnitReset`은 CLAP `reset`을 호출합니다. 렌더, 파라미터 flush, 재활성화는 하나의 락으로 직렬화되며, 플러그인 콜백(`request_callback`, 파라미터 리스너를 위한 편집, 에디터 크기 조정)은 메인 디스패치 큐에서 실행됩니다.
- LV2 인스턴스(Linux)는 CLAP 인스턴스 하나를 호스팅합니다. `activate`는 인스턴스화 시점의 샘플 레이트와 `bufsz:maxBlockLength`(옵션이 없으면 4096)에 맞춰 이를 활성화하고, 첫 번째 `run`에서 처리가 시작되며, `deactivate`는 이를 중지하고 비활성화합니다. `run`은 오디오 스레드이며, 인스턴스화, 활성화, 상태 호출, UI의 idle 콜백은 메인 스레드로 취급되고, 플러그인의 `request_callback` 작업은 idle 콜백에서(그리고 활성화 및 상태 호출 전후에) 실행됩니다. 재시작 요청(레이턴시 변경)은 다음 `run`의 시작 시점에 플러그인을 다시 활성화합니다.
- 스탠드얼론 애플리케이션(`pkg/standalone`)은 자체 플러그인을 위한 CLAP host입니다. 프로세스당 인스턴스는 하나이며, 오디오 장치의 샘플 레이트와 period로 활성화되고, 플러그인이 재시작을 요청하면 비활성화되었다가 다시 활성화됩니다. 메인 스레드는 메인 고루틴이며(이 패키지는 `init`에서 이를 프로세스 메인 스레드에 고정합니다), 이 고루틴이 창과 이벤트 루프를 실행합니다. 오디오 장치의 스레드는 `process`를 실행합니다.

## 스레드 안전성 기대 사항

- `pkg/framework/param.Registry`는 파라미터 조회 및 등록 헬퍼에 내부 락을 사용합니다.
- 개별 파라미터의 읽기와 쓰기는 이 저장소가 사용하는 런타임 경로에서 안전하도록 설계되어 있지만, 호출자는 레지스트리 접근 주변에서 긴 임계 영역을 두거나 외부 락과의 순서 관계를 만드는 일은 여전히 피해야 합니다.
- `pkg/framework/process.Context`는 한 번에 하나의 처리 패스가 소유하며, 워커 고루틴 간에 동시에 공유해서는 안 됩니다.
- `ProcessAudio`는 host 오디오 스레드에서 실행되며, 그 밖의 모든 `Processor` 메서드(MIDI learn, 채널 정보, `ReceiveMessage` 포함)와 모든 `Host` 호출은 host/UI 스레드에서 실행됩니다. 런타임은 인스턴스별로 host 스레드의 라이프사이클 호출을 직렬화합니다. CLAP에서는 `Host`를 통한 편집이 큐에 쌓였다가 다음 `process` 또는 `flush` 호출에서 host에 보고됩니다.
- 런타임은 각 host 블록을 파라미터 변경 오프셋에서 분할하고 구간마다 `ProcessAudio`를 한 번씩 호출하므로, 프로세서는 한 번의 호출 안에서는 파라미터 값이 일정하다는 점을 전제로 할 수 있습니다.
- `pkg/framework/state.Manager`는 직렬화 헬퍼이며, 동시에 실행되는 실시간 오디오 경로가 아니라 host 상태 콜백에서 호출해야 합니다.
- 플랫폼 에디터 브리지는 Go 측 파라미터 모델을 단일 진실 공급원(source of truth)으로 취급해야 하며, 병렬 상태를 직접 변경하는 대신 UI 변경을 일반적인 파라미터 업데이트 흐름을 통해 되돌려 전달해야 합니다.

## 상태와 영속성

- 파라미터 상태가 기본 영속성 계층입니다.
- `pkg/framework/state.Manager`는 헤더, 버전, 순서대로 나열된 파라미터 값, 선택적 커스텀 페이로드로 이루어진, 이 저장소가 직접 정의한 단순한 바이너리 형식으로 기록합니다.
- 알 수 없는 파라미터 ID는 순방향 호환성(forward compatibility)을 위해 로드할 때 무시됩니다.
- 프로그램 선택기(`plugin.ProgramParamID`)는 다른 파라미터와 마찬가지로 저장되며, 이를 복원해도 복원된 값 위에 프로그램이 다시 적용되지는 않습니다.
- host가 프로그램 목록 슬롯(`IProgramListData`)에 기록하는 프로그램 데이터는 별도의 불투명(opaque) 형식을 사용하며 컴포넌트 상태에 포함되지 않습니다.
- 커스텀 상태는 `StatefulProcessor` 또는 `state.Manager`의 커스텀 저장/로드 훅을 통해서만 추가해야 합니다.
- 웹 에디터 스냅샷은 레지스트리 상태를 UI 쪽에 반영한 미러로, 로컬 에디터 복원/하이드레이션에는 유용하지만, host에서 볼 수 있는 플러그인 상태는 여전히 VST3 `GetState` / `SetState` 콜백을 통해 전달되어야 합니다.

## 다운스트림 사용

다운스트림 저장소가 이 포크를 사용할 때는 다음을 따릅니다:

- `github.com/th-release/vst3go`를 import합니다
- 위에 나열한 지원 패키지를 기반으로 플러그인/런타임 로직을 구축합니다
- 신호 처리에는 `pkg/dsp`를, 참조 플러그인으로는 `examples/`를 사용합니다
- 제품 전용 코드(라이선스, 인스톨러, 브랜딩)는 다운스트림 저장소에 둡니다
- host별 동작, 인스톨러 레이아웃, 서명, DAW 호환성은 다운스트림 저장소에서 검증합니다

태그가 게시되면 다운스트림 저장소는 계속 변하는 브랜치 대신 태그가 지정된 버전에 고정하여, 런타임과 브리지 변경 사항을 신중하게 업그레이드할 수 있도록 해야 합니다.

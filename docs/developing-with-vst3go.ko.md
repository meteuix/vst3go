[English](developing-with-vst3go.md) | 한국어

# `vst3go`로 개발하기

이 가이드는 첫 번째 `Plugin`/`Processor` 쌍부터 에디터 메타데이터, 상태 복원, 릴리스 검증까지 `vst3go` 위에서 플러그인을 만드는 방법을 보여 줍니다.

이 가이드는 의도적으로 규범적입니다. 이 가이드를 따른다면 아래의 레이아웃, 명명, 상태 흐름을 기본 구현 경로로 취급하세요.

전체 가이드를 순서대로 따라가면 하나의 동작하는 플러그인이 완성됩니다. 메타데이터 정의 하나, 프로세서 하나, 에디터 셸 하나, 상태 경로 하나, 검증 흐름 하나입니다.

요약하면 다음과 같습니다:

- `vst3go`는 런타임이자 VST3 셸입니다.
- `pkg/dsp`는 신호 처리 빌딩 블록(필터, 다이내믹스, 딜레이, 모듈레이션, 리버브, 디스토션, 오실레이터, 엔벨로프)을 제공합니다.
- 실제 이펙트/악기 동작과 그 블록들을 조합하는 방식은 다운스트림 저장소가 소유합니다.
- `examples/`에는 이렇게 만든 완성된 플러그인이 있습니다. 가장 비슷한 것에서 시작하세요.

멘탈 모델이 필요하다면 `vst3go`를 다음 질문에 답하는 계층으로 생각하세요:

- 플러그인의 이름은 무엇인가?
- 어떤 파라미터를 노출하는가?
- 어떤 오디오 버스와 이벤트 버스를 가지는가?
- 오디오 블록 하나를 어떻게 처리하는가?
- 상태를 어떻게 저장하고 복원하는가?
- 네이티브 에디터가 파라미터를 어떻게 반영하고 갱신하는가?

완전한 DAW 프레임워크가 되려고 하지는 않습니다.

지원하는 host 기능의 전체 목록(악기, MIDI CC와 피치 벤드, MIDI 출력, 사이드체인, 버스
레이아웃, 레이턴시, 출력 미터, 오프라인 렌더링)은
[`plugin-capabilities.ko.md`](plugin-capabilities.ko.md)를 참고하세요. 동작하는 참조
플러그인은 `internal/validation/`에 있습니다.

## 1. 이해해야 할 세 가지 계층

이 저장소로 플러그인을 만들 때는 세 개의 분리된 계층이 있습니다:

1. **메타데이터 계층**
   - `pkg/framework/plugin.Info`에는 이름, ID, vendor, 버전, 카테고리가 들어 있습니다.
   - `pkg/plugin.Plugin.GetInfo()`가 그 메타데이터를 host에 반환합니다.

2. **런타임 계층**
   - `pkg/plugin.Processor`가 핵심 계약입니다.
   - 파라미터, 버스, 라이프사이클, 블록 처리를 소유합니다.
   - `pkg/plugin`의 래퍼가 이를 VST3 컴포넌트/컨트롤러 쌍으로 바꿉니다.

3. **에디터/상태 계층**
   - `pkg/plugin.EditorModel`과 `pkg/plugin.EditorSnapshot`은 브라우저로 렌더링되는 에디터가 보는 내용을 기술합니다.
   - `pkg/framework/state.Manager`와 `StatefulProcessor`는 파라미터 값 이외의 영속성을 처리합니다.
   - 웹 에디터 셸은 같은 파라미터/상태 모델의 뷰일 뿐입니다.

중요한 규칙은 오디오 프로세서가 단일 진실 공급원(source of truth)이라는 것입니다. 에디터가 프로세서를 반영하며, 그 반대가 아닙니다.

## 2. 실제 플러그인의 모습

플러그인에는 최소한 다음이 필요합니다:

- `pkg/plugin.Plugin` 구현
- `pkg/plugin.Processor` 구현
- `pkg/framework/param.Registry`
- `pkg/framework/bus.Configuration`
- 파라미터 값만으로 충분하지 않을 때의 커스텀 상태 훅

런타임 래퍼는 다음 흐름을 따릅니다:

1. host가 플러그인 메타데이터를 요청합니다
2. host가 프로세서 인스턴스를 만듭니다
3. 래퍼가 `Initialize(sampleRate, maxBlockSize)`를 호출합니다
4. host가 처리를 시작합니다
5. `ProcessAudio(ctx)`가 반복해서 호출됩니다
6. 에디터/뷰가 필요할 때 현재 파라미터 상태를 요청합니다
7. host가 `GetState`와 `SetState`를 통해 상태를 저장하거나 복원합니다

## 3. 최소한의 플러그인 뼈대

다운스트림 저장소에서 원하는 형태는 다음과 같습니다:

```go
package myplugin

import (
	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	frameworkplugin "github.com/meteuix/vst3go/v2/pkg/framework/plugin"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
	"github.com/meteuix/vst3go/v2/pkg/framework/process"
	"github.com/meteuix/vst3go/v2/pkg/plugin"
)

type Plugin struct{}

func (p *Plugin) GetInfo() frameworkplugin.Info {
	return frameworkplugin.Info{
		ID:       "com.example.myplugin",
		Name:     "My Plugin",
		Version:  "0.1.0",
		Vendor:   "Example Co",
		Category: frameworkplugin.CategoryFx,
	}
}

func (p *Plugin) CreateProcessor() plugin.Processor {
	return NewProcessor()
}

type Processor struct {
	params *param.Registry
	buses  *bus.Configuration
}
```

이 부분은 일부러 지루하게 만들어져 있습니다. 중요한 작업은 프로세서 자체에 있습니다.

## 4. 실제 파라미터를 중심으로 프로세서 만들기

파라미터는 나중에 덧붙이는 것이 아니라 가장 먼저 설계합니다.

각 컨트롤에 대해 다음을 결정합니다:

- 연속 파라미터인가, 이산 파라미터인가?
- 오토메이션이 가능해야 하는가?
- 숨겨야 하는가?
- 읽기 전용이어야 하는가?
- plain 범위는 무엇인가?
- 에디터에서 어떻게 포맷팅해야 하는가?

`pkg/framework/param`의 빌더 헬퍼는 이를 깔끔하게 유지하기 위한 것입니다:

- `GainParameter`
- `FrequencyParameter`
- `MixParameter`
- `TimeParameter`
- `RatioParameter`
- `QParameter`
- `PanParameter`
- `PhaseParameter`
- `Choice`

### 파라미터 블록 예제

`Eight EQ` 스타일 플러그인의 파라미터 구성은 다음과 같습니다:

- `Input Gain`
- `Band 1`부터 `Band 8`까지
  - 각 밴드에는 `Type`, `Frequency`, `Gain`, `Q`가 있습니다
  - 선택적인 추가 항목은 `Shelf Slope`, `Stereo Link`, `Solo`입니다
- `Output Gain`
- `Analyzer`
- `Bypass`

설정 예제:

```go
func NewProcessor() *Processor {
	params := param.NewRegistry()

	_ = params.Add(
		param.GainParameter(1, "Input Gain").Default(0).Build(),
		param.New(2, "Band 1 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(3, "Band 1 Frequency", 20, 20000, 120).Build(),
		param.GainParameter(4, "Band 1 Gain").Default(0).Build(),
		param.QParameter(5, "Band 1 Q", 0.1, 12.0, 0.707).Build(),
		param.New(6, "Band 2 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(7, "Band 2 Frequency", 20, 20000, 250).Build(),
		param.GainParameter(8, "Band 2 Gain").Default(0).Build(),
		param.QParameter(9, "Band 2 Q", 0.1, 12.0, 0.707).Build(),
		param.New(10, "Band 3 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(11, "Band 3 Frequency", 20, 20000, 500).Build(),
		param.GainParameter(12, "Band 3 Gain").Default(0).Build(),
		param.QParameter(13, "Band 3 Q", 0.1, 12.0, 1.0).Build(),
		param.New(14, "Band 4 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(15, "Band 4 Frequency", 20, 20000, 1000).Build(),
		param.GainParameter(16, "Band 4 Gain").Default(0).Build(),
		param.QParameter(17, "Band 4 Q", 0.1, 12.0, 1.0).Build(),
		param.New(18, "Band 5 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(19, "Band 5 Frequency", 20, 20000, 2000).Build(),
		param.GainParameter(20, "Band 5 Gain").Default(0).Build(),
		param.QParameter(21, "Band 5 Q", 0.1, 12.0, 1.0).Build(),
		param.New(22, "Band 6 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(23, "Band 6 Frequency", 20, 20000, 4000).Build(),
		param.GainParameter(24, "Band 6 Gain").Default(0).Build(),
		param.QParameter(25, "Band 6 Q", 0.1, 12.0, 1.0).Build(),
		param.New(26, "Band 7 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(27, "Band 7 Frequency", 20, 20000, 8000).Build(),
		param.GainParameter(28, "Band 7 Gain").Default(0).Build(),
		param.QParameter(29, "Band 7 Q", 0.1, 12.0, 1.0).Build(),
		param.New(30, "Band 8 Type").Choice("Bell", "Low Cut", "Low Shelf", "Bell", "High Shelf", "High Cut").Build(),
		param.FrequencyParameter(31, "Band 8 Frequency", 20, 20000, 12000).Build(),
		param.GainParameter(32, "Band 8 Gain").Default(0).Build(),
		param.QParameter(33, "Band 8 Q", 0.1, 12.0, 0.707).Build(),
		param.GainParameter(34, "Output Gain").Default(0).Build(),
		param.New(35, "Analyzer").Toggle().Build(),
		param.New(36, "Bypass").Toggle().Bypass().Build(),
	)

	return &Processor{
		params: params,
		buses:  bus.Stereo(),
	}
}
```

`Utility` 스타일 플러그인의 파라미터 구성은 다릅니다:

- `Input Gain`
- `Output Gain`
- `Pan`
- `Width`
- `Mono`
- `Phase Invert L`
- `Phase Invert R`
- `Mute`

모두 사용자가 이미 이해하고 있는 표준 컨트롤이며, 바로 그 점 때문에 이 플러그인이 특수 효과가 아니라 유틸리티 도구처럼 느껴집니다.

## 5. 버스 레이아웃부터 정하기

버스 레이아웃은 host가 플러그인을 어떻게 보는지를 결정합니다.

`pkg/framework/bus`의 헬퍼를 사용합니다:

- 일반적인 스테레오 입력/스테레오 출력 이펙트에는 `bus.Stereo()`
- 단순한 단일 채널 유틸리티나 악기 셸에는 `bus.Mono()`
- 사이드체인이나 멀티버스 레이아웃에는 `bus.NewBuilder()`

### 스테레오 EQ

EQ는 스테레오 메인 입력과 출력으로 시작합니다:

```go
buses := bus.Stereo()
```

### 유틸리티 플러그인

Utility 같은 도구를 만들 때도 스테레오 I/O가 일반적인 선택입니다:

```go
buses := bus.Stereo()
```

처리가 단순하더라도 host가 기대하는 것과 같은 I/O 형태를 유지하세요. 유틸리티 플러그인은 스테레오 이미지, 게인, 위상을 조작하므로 스테레오 입출력이 올바른 기본값입니다.

### 사이드체인 예제

나중에 더킹이나 외부 분석이 필요하면 빌더를 사용합니다:

```go
buses := bus.NewBuilder().
	WithStereoInput("Main In").
	WithStereoOutput("Main Out").
	WithSidechain("Sidechain In").
	MustBuild()
```

여기서 가이드의 규칙은 간단합니다. 버스는 구현에서 출발해 앞으로 나아가며 정하지 말고, 사용자가 보는 동작에서 거꾸로 정하세요.

## 6. `Initialize`, `SetActive`, `ProcessAudio` 구현하기

`vst3go`는 실시간 오디오 제약을 중심으로 설계되었기 때문에 라이프사이클이 중요합니다.

### `Initialize`

host의 샘플 레이트나 블록 크기에 의존하는 것을 할당하거나 설정할 때는 `Initialize(sampleRate, maxBlockSize)`를 사용합니다.

일반적인 역할:

- 필터 계수 초기화
- 엔벨로프나 딜레이 라인 리셋
- 샘플 레이트에 의존하는 상수 캐시
- 룩업 테이블 준비

host와 무관한 플러그인 설정은 여기서 하지 마세요.

### `SetActive`

처리가 시작될 때 `SetActive(true)`를, host가 플러그인을 정지하거나 비활성화할 때 `SetActive(false)`를 사용합니다.

일반적인 역할:

- 스무딩 리셋
- 필요하면 딜레이 라인 비우기
- 대기 중인 MIDI/이벤트 비우기
- 트랜스포트 추종 상태 리셋
- 다음 시작을 깨끗하게 할 수 있도록 프로세서 준비

### `ProcessAudio`

이것은 실시간 콜백이며 다음 성질을 갖습니다:

- 메모리 할당 없음
- 가능한 곳에서는 락 없음
- 분기 최소화
- 결정적(deterministic)

`process.Context`는 다음에 편리하게 접근할 수 있게 해 줍니다:

- `Input`
- `Output`
- `NumSamples()`
- `NumInputChannels()`
- `NumOutputChannels()`
- `Param(id)`
- `ParamPlain(id)`
- `ProcessChannels`
- `ProcessStereo`
- `ProcessSamples`
- `PassThrough`
- `Clear`

### 좋은 기본 패턴

`Eight EQ` 스타일 프로세서라면 다음이 깔끔한 출발점입니다:

```go
func (p *Processor) ProcessAudio(ctx *process.Context) {
	if ctx == nil {
		return
	}

	if ctx.Param(11) >= 0.5 {
		ctx.PassThrough()
		return
	}

	ctx.ProcessStereo(func(ch int, input, output []float32) {
		for i := range input {
			sample := input[i]
			sample = p.processEQChain(sample, ctx)
			sample = applyGain(sample, ctx.ParamPlain(1), ctx.ParamPlain(34))
			output[i] = sample
		}
	})
}
```

`processEQChain`과 `applyGain`은 플러그인 코드입니다. 아래와 같이 `pkg/dsp/filter`와 `pkg/dsp/gain`으로 만듭니다.

## 7. `Eight EQ` 스타일 플러그인 만들기

거의 모든 것을 다루기 때문에 가장 자연스러운 예제입니다:

- 연속 파라미터
- 음색 컨트롤
- 샘플 레이트에 의존하는 계산
- 실시간 오디오 처리
- 에디터 컨트롤
- 상태 영속성

### 7.1 컨트롤 모델 정하기

`Eight EQ` 스타일 플러그인에는 다음이 있습니다:

- 입력 게인
- 독립적으로 조절할 수 있는 여덟 개의 밴드
- 밴드별 필터 타입
- 밴드별 게인, 주파수, Q
- 출력 게인
- 분석기 활성화
- 바이패스

3밴드 EQ와의 중요한 차이는 사용자가 더 이상 세 개의 고정된 음역대로 생각하지 않는다는 점입니다. 사용자는 서로 조합되고, 독립적으로 조정되며, 에디터에 명확하게 표시되는 정밀하거나 음악적인 필터들의 체인으로 생각합니다.

첫 버전을 출시한 뒤에 다음을 추가하세요:

- 출력 미터
- 위상 반전
- 오버샘플링 토글
- 분석기 활성화

첫 버전은 작게 유지하세요.

### 7.2 화면 설계하기

EQ가 노브 더미가 아니라 진짜 악기처럼 느껴지려면 화면 레이아웃이 신호 흐름과 일치해야 합니다.

첫 `Eight EQ` 화면에는 네 개의 시각적 영역이 있습니다:

1. **상단 스트립**
   - 플러그인 이름
   - 프리셋 선택기
   - 다운스트림 앱에 있다면 실행 취소/다시 실행
   - 바이패스
   - 입력/출력 레벨 표시

2. **중앙 그래프**
   - 응답 곡선 뷰
   - 스펙트럼 전체에 배치된 여덟 개의 밴드 노드
   - 분석기 데이터가 있다면 실시간 곡선 응답
   - 주파수 축 레이블
   - 게인 축 레이블

3. **밴드 컨트롤 매트릭스**
   - 넓은 화면에서는 4x2 그리드로 밴드당 카드 하나
   - 각 카드는 게인, 주파수, Q, 타입을 노출
   - 각 카드는 활성 또는 바이패스 상태를 표시
   - 좁은 화면에서는 각 카드가 간결한 행으로 접힘

4. **하단 유틸리티 스트립**
   - 전역 바이패스
   - 출력 게인
   - 오버샘플링
   - 분석기 활성화
   - 포함한다면 모노 합산이나 위상 유틸리티

화면은 왼쪽에서 오른쪽으로 다음과 같이 읽힙니다:

- 입력이 들어오고
- 밴드가 음색을 만들고
- 출력이 나갑니다

이것이 사용자에게 필요한 시각적 이야기입니다.

#### 권장 와이어프레임

```text
┌──────────────────────────────────────────────────────────────┐
│  My EQ   Preset ▼   Bypass   Input ▓▓▓▓▓   Output ▓▓▓░░      │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│                frequency response / analyzer                 │
│          ────────●────────────●────────────●──────          │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│ Band 1     Band 2     Band 3     Band 4                      │
│ Gain FreqQ Gain FreqQ Gain FreqQ Gain FreqQ                  │
├──────────────────────────────────────────────────────────────┤
│ Band 5     Band 6     Band 7     Band 8                      │
│ Gain FreqQ Gain FreqQ Gain FreqQ Gain FreqQ                  │
├──────────────────────────────────────────────────────────────┤
│ Input Gain   Output Gain   Bypass   Analyzer   Oversampling  │
└──────────────────────────────────────────────────────────────┘
```

#### 각 영역의 역할

- 상단 스트립은 제품의 정체성을 고정합니다
- 그래프는 음색 형태에 대한 즉각적인 피드백을 줍니다
- 밴드 카드는 정밀한 제어를 제공합니다
- 하단 스트립은 유틸리티 동작이 방해되지 않게 합니다

#### 이 레이아웃이 효과적인 이유

- 일반적인 파라미터 패널이 아니라 제대로 된 도구처럼 느껴지게 합니다
- 멘탈 모델을 바꾸지 않고도 고급 기능으로 확장할 수 있습니다
- `EditorModel.Sections`에 자연스럽게 대응됩니다
- 밴드 카드가 쌓이므로 작은 창에서도 읽기 쉽습니다

#### 권장 컴포넌트 구성

화면을 의도적으로 이름 붙인 컴포넌트의 집합으로 사용하세요:

- `PluginHeader`
  - 플러그인 이름, 프리셋 이름, 바이패스, 지원된다면 실행 취소/다시 실행을 표시합니다
  - 그래프가 가장 많은 공간을 차지하도록 작게 유지합니다

- `ResponseGraph`
  - 합쳐진 EQ 곡선을 시각화합니다
  - 저음, 중음, 고음 주파수 지점에 드래그 가능한 밴드 노드를 표시합니다
  - 분석기 데이터나 단순한 정적 곡선 미리보기를 겹쳐 표시합니다

- `BandCard`
  - EQ 밴드당 카드 하나
  - 게인, 주파수, Q, 타입 컨트롤을 담습니다
  - `Band 1`, `Band 2` 같은 밴드 레이블을 포함합니다
  - `solo`, `bypass`, `linked` 같은 작은 상태 배지를 표시합니다

- `GlobalControls`
  - 입력/출력 게인, 오버샘플링, 분석기 토글을 처리합니다
  - 전역 바이패스와 유틸리티 토글을 담습니다

- `StatusStrip`
  - 샘플 레이트, 오버샘플링 상태, 제품에 라이선스가 있다면 라이선스 상태를 표시합니다
  - 읽기 쉽되 결코 시끄럽지 않게 유지합니다

UI를 React로 구현한다면 이 컴포넌트들은 독립적인 함수형 컴포넌트나 재사용 가능한 섹션에 대응됩니다. HTML/CSS/JS로 직접 구현하더라도 이들은 여전히 구분된 시각적 영역입니다.

#### 권장 상호작용 모델

EQ 화면은 다음 상호작용을 지원합니다:

- 응답 그래프의 노드를 드래그하면 주파수와 게인이 함께 바뀝니다
- 밴드 카드의 노브를 드래그하면 한 번에 하나의 파라미터만 바뀝니다
- 컨트롤을 더블클릭하면 기본값으로 리셋됩니다
- shift-드래그로 미세 조정을 합니다
- 마우스를 올리면 정확한 값이나 툴팁이 나타납니다
- 밴드 카드를 클릭하면 그래프에서 해당 밴드에 포커스할 수 있습니다
- 밴드 타입 칩을 클릭하면 필터 모양을 순환할 수 있습니다
- 바이패스는 항상 명확하고 즉시 되돌릴 수 있습니다

그래프와 노브 뷰는 동기화된 상태를 유지합니다:

- 사용자가 노드를 움직이면 카드 값이 즉시 갱신됩니다
- host 오토메이션 레인이 파라미터를 움직이면 노드도 움직입니다
- 스냅샷이 복원되면 그래프와 카드가 동시에 복원된 상태로 바뀝니다

이런 즉각적인 시각적 동기화가 화면을 전문적으로 느끼게 만듭니다.

#### 권장 반응형 동작

작은 창에서 EQ를 사용할 수 없게 만들지 마세요.

필수 동작:

- 넓은 화면에서는 위에 그래프를, 그 아래에 여덟 개의 밴드 카드를 4x2 그리드로 표시합니다
- 중간 화면에서는 그래프를 넓게 유지하고 카드를 네 개씩 두 줄로 줄바꿈합니다
- 좁은 화면에서는 밴드 카드를 세로로 쌓고, 그래프는 쓸모 있을 만큼의 높이를 유지합니다
- 아주 좁은 화면에서는 고급 컨트롤을 펼침 행이나 밴드 서랍 뒤로 접습니다

다음과 같은 플랫폼 친화적인 세부 사항도 고려하세요:

- 드래그 가능한 노드의 최소 터치 영역을 보장합니다
- 일반적인 확대 수준에서 노브 레이블을 읽을 수 있게 유지합니다
- 그래프를 너무 빽빽하게 만들어 장식용으로만 남지 않게 합니다
- 그래프가 숨겨지거나 접혀 있어도 파라미터 텍스트는 보이게 유지합니다

#### 권장 화면 상태

EQ UI는 다음 상태를 정의합니다:

- `Idle`
  - 플러그인이 열려 있고 특별한 경고가 없음

- `Bypassed`
  - 그래프를 시각적으로 흐리게 하고 바이패스 상태를 명확하게 표시

- `Analyzing`
  - 분석기 오버레이가 활성화됨

- `Offline/Grace`
  - 상용 제품이라면, 라이선스 유예 기간이나 제한 모드를 설명하는 작업을 막지 않는 배너

- `Narrow Layout`
  - 카드가 쌓이고 그래프가 압축되지만 컨트롤 접근성은 잃지 않음

이 상태 목록은 UI가 숨겨진 가정을 가진 일회성 레이아웃이 되는 것을 막아 주기 때문에 유용합니다.

#### 권장 React 트리

EQ를 React로 구현한다면 컴포넌트 트리는 다음과 같습니다:

```text
<EqEditor>
  <PluginHeader />
  <MainSurface>
    <ResponseGraph />
    <BandCardGrid>
      <BandCard band="1" />
      <BandCard band="2" />
      <BandCard band="3" />
      <BandCard band="4" />
      <BandCard band="5" />
      <BandCard band="6" />
      <BandCard band="7" />
      <BandCard band="8" />
    </BandCardGrid>
    <GlobalControls />
    <StatusStrip />
  </MainSurface>
</EqEditor>
```

각 부분의 실제 역할:

- `EqEditor`
  - 전체 스냅샷 상태를 소유합니다
  - `EditorModel`로부터 하이드레이션합니다
  - 파라미터 변경 메시지를 Go로 되돌려 보냅니다

- `PluginHeader`
  - 플러그인 제목과 프리셋을 표시합니다
  - 바이패스와 상위 수준 동작을 담습니다

- `ResponseGraph`
  - 주파수 응답을 렌더링합니다
  - 현재 밴드 값과 분석기 데이터를 받습니다
  - 주파수/게인 지점에 대한 드래그 업데이트를 내보냅니다

- `BandCardGrid`
  - 개별 밴드 카드를 묶습니다
  - 너비에 따라 그리드와 쌓인 레이아웃을 전환합니다

- `BandCard`
  - 밴드별 컨트롤을 노출합니다
  - 어떤 밴드를 편집하고 있는지 명확히 보여 줍니다
  - 고급 컨트롤을 영향을 주는 밴드 가까이에 둡니다

- `GlobalControls`
  - 출력 게인, 오버샘플링, 분석기 및 비슷한 전역 옵션을 담습니다
  - 밴드와 무관한 설정을 메인 그래프 영역 밖에 둡니다

- `StatusStrip`
  - 관련이 있을 때 샘플 레이트, CPU 힌트, 라이선스 상태를 표시합니다
  - 작고 작업을 막지 않는 상태 메시지를 제공합니다

같은 트리를 일반 HTML/CSS/JS로도 표현할 수 있습니다. 어느 쪽이든 역할은 분리된 상태로 유지됩니다.

#### 권장 HTML/CSS 구조

HTML 셸은 단순하고 예측 가능합니다:

```html
<main class="eq-editor">
  <header class="eq-header"></header>
  <section class="eq-graph-panel"></section>
  <section class="eq-band-grid"></section>
  <section class="eq-global-panel"></section>
  <footer class="eq-status-bar"></footer>
</main>
```

필수 CSS 동작:

- `eq-editor`는 세로 그리드나 flex column을 사용합니다
- `eq-graph-panel`이 화면에서 가장 높은 부분을 차지합니다
- `eq-band-grid`는 넓은 화면에서 반응형 4열 그리드입니다
- `eq-global-panel`은 작고 시각적으로 보조적인 위치에 머뭅니다
- `eq-status-bar`는 작은 텍스트와 낮은 시각적 비중을 사용합니다

필수 시각적 위계:

- 그래프가 첫 번째
- 정밀한 노브가 두 번째
- 전역 동작이 세 번째
- 상태가 마지막

이 위계는 사람들이 실제로 EQ를 다루는 방식과 일치합니다.

#### React 목업

아래는 다운스트림 React 에디터의 출발점입니다. 의도적으로 단순하며, 이 구조가 처음 출시할 형태입니다.

```tsx
type EditorControl = {
  id: number
  normalized: number
  plain: number
  name: string
}

type EditorSnapshot = {
  model: {
    sections: Array<{
      controls: EditorControl[]
    }>
  }
}

type EqBand = {
  id: number
  label: string
  type: number
  gain: number
  frequency: number
  q: number
}

type Props = {
  model: {
    plugin: {
      name: string
      vendor: string
    }
  }
  snapshot: EditorSnapshot
  onParamChange: (id: number, plainValue: number) => void
}

const BAND_PARAMS = [
  { type: 2, frequency: 3, gain: 4, q: 5 },
  { type: 6, frequency: 7, gain: 8, q: 9 },
  { type: 10, frequency: 11, gain: 12, q: 13 },
  { type: 14, frequency: 15, gain: 16, q: 17 },
  { type: 18, frequency: 19, gain: 20, q: 21 },
  { type: 22, frequency: 23, gain: 24, q: 25 },
  { type: 26, frequency: 27, gain: 28, q: 29 },
  { type: 30, frequency: 31, gain: 32, q: 33 },
]

function controlById(snapshot: EditorSnapshot, id: number): EditorControl {
  const controls = snapshot.model.sections[0]?.controls ?? []
  const control = controls.find((candidate) => candidate.id === id)
  if (!control) {
    throw new Error(`missing control ${id}`)
  }
  return control
}

function EqEditor({ model, snapshot, onParamChange }: Props) {
  const bands: EqBand[] = BAND_PARAMS.map((band, index) => ({
    id: index + 1,
    label: `Band ${index + 1}`,
    type: controlById(snapshot, band.type).plain,
    frequency: controlById(snapshot, band.frequency).plain,
    gain: controlById(snapshot, band.gain).plain,
    q: controlById(snapshot, band.q).plain,
  }))

  return (
    <main className="eq-editor">
      <header className="eq-header">
        <div className="eq-title">
          <h1>{model.plugin.name}</h1>
          <span>{model.plugin.vendor}</span>
        </div>
        <button
          className="eq-bypass"
          onClick={() => onParamChange(36, controlById(snapshot, 36).plain)}
        >
          Bypass
        </button>
      </header>

      <section className="eq-graph-panel">
        <ResponseGraph
          bands={bands}
          onBandDrag={(bandId, nextFrequency, nextGain) => {
            const band = BAND_PARAMS[bandId - 1]
            onParamChange(band.frequency, nextFrequency)
            onParamChange(band.gain, nextGain)
          }}
          onBandTypeCycle={(bandId, nextType) => {
            onParamChange(BAND_PARAMS[bandId - 1].type, nextType)
          }}
        />
      </section>

      <section className="eq-band-grid">
        {bands.map((band) => (
          <BandCard
            key={band.id}
            band={band}
            onChange={(field, value) => {
              const bandParams = BAND_PARAMS[band.id - 1]
              if (field === "gain") onParamChange(bandParams.gain, value)
              if (field === "frequency") onParamChange(bandParams.frequency, value)
              if (field === "q") onParamChange(bandParams.q, value)
              if (field === "type") onParamChange(bandParams.type, value)
            }}
          />
        ))}
      </section>

      <section className="eq-global-panel">
        <Knob label="Input" />
        <Knob label="Output" />
        <Toggle label="Analyzer" />
        <Toggle label="Oversampling" />
      </section>

      <footer className="eq-status-bar">
        <span>48 kHz</span>
        <span>Analyzer off</span>
        <span>Host sync OK</span>
      </footer>
    </main>
  )
}
```

헬퍼 함수는 다운스트림에 따라 다르지만, 중요한 것은 구조입니다:

- 에디터는 모델과 스냅샷을 받습니다
- 밴드 카드는 스냅샷 데이터로부터 생성됩니다
- 사용자 편집은 Go 파라미터 브리지로 다시 호출됩니다
- 그래프와 카드는 같은 파라미터 소스를 공유합니다

#### CSS 목업

다음 레이아웃을 사용하세요:

```css
.eq-editor {
  display: grid;
  grid-template-rows: auto minmax(240px, 1fr) auto auto;
  gap: 16px;
  padding: 16px;
  color: #f4f4f5;
  background:
    radial-gradient(circle at top, rgba(255, 255, 255, 0.08), transparent 34%),
    linear-gradient(180deg, #17171b 0%, #101114 100%);
}

.eq-header,
.eq-status-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.eq-graph-panel {
  min-height: 280px;
  border-radius: 20px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.03);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.05);
}

.eq-band-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.eq-global-panel {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.eq-status-bar {
  font-size: 12px;
  opacity: 0.72;
}

@media (max-width: 900px) {
  .eq-band-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .eq-global-panel {
    grid-template-columns: 1fr;
  }
}
```

이 목업은 장식용이 아닙니다. 이 목업은:

- 읽기 쉽고
- 반응형이며
- `EditorModel`에 연결하기 쉽고
- Go 스냅샷 데이터로부터 하이드레이션하기 쉽고
- 나중에 실제 분석기로 확장하기 쉽습니다

#### 권장 화면 문구

EQ를 세련되게 느껴지게 하려면 다음 레이블을 사용하세요:

- `Input Gain`
- `Band 1`부터 `Band 8`까지
- `Bell`
- `Shelf`
- `Cut`
- `Output Gain`
- `Analyzer`
- `Oversampling`
- `Bypass`

더 고급 컨트롤을 노출한다면 사람이 읽기 쉬운 문구를 유지하세요:

- `Freq`보다는 `Frequency`
- `Invert`보다는 `Phase Invert`
- 제품에서 더 잘 읽힌다면 `Width`보다는 `Stereo Width`

문구는 사용자가 UI를 해독하게 만들지 않고도 컨트롤의 의미를 분명히 합니다.

### 7.3 필터 토폴로지 정하기

각 밴드는 `pkg/dsp/filter.Biquad`입니다(밴드당 인스턴스 하나가 모든 채널의 상태를 보관):

- 로우 셜프 밴드에는 `SetLowShelf`
- 벨 밴드에는 `SetPeakingEQ`
- 하이 셜프 밴드에는 `SetHighShelf`
- 컷 밴드에는 `SetHighpass` / `SetLowpass`

```go
bands := make([]*filter.Biquad, 8)
for i := range bands {
	bands[i] = filter.NewBiquad(2)
}
bands[0].SetLowShelf(sampleRate, 120, 0.707, 3)
bands[1].SetPeakingEQ(sampleRate, 1000, 1.0, -2)
```

그런 다음, 채널 버퍼마다:

```go
for _, band := range bands {
	band.Process(output, ch)
}
```

`vst3go` 관점에서 중요한 것은 필터를 어디에 저장하느냐입니다:

- 프로세서 필드에 저장
- `Initialize`에서 재구성
- `SetActive(false)`에서 리셋
- `ProcessAudio` 동안 현재 파라미터 값으로 구동

### 7.4 파라미터 읽기를 가볍게 유지하기

샘플 단위 오토메이션이 정말로 필요하지 않다면 `ProcessAudio`에서 값을 블록당 한 번만 읽으세요.

예제:

```go
inputGain := ctx.ParamPlain(1)
lowGain := ctx.ParamPlain(2)
lowFreq := ctx.ParamPlain(3)
lowQ := ctx.ParamPlain(4)
```

샘플 단위로 정확한 오토메이션이 필요하다면 처리 컨텍스트의 파라미터 변경 흐름을 사용하여 해당 오프셋에서 계수나 목표값을 갱신하세요.

### 7.5 바이패스를 깔끔하게 처리하기

바이패스는 진정한 바이패스 경로입니다:

```go
if ctx.Param(11) >= 0.5 {
	ctx.PassThrough()
	return
}
```

EQ에서는 이것이 기대되는 동작입니다.

### 7.6 상태는 나중에 추가하기

EQ가 파라미터만 사용하고 숨겨진 상태가 없다면 `StatefulProcessor`는 필요하지 않습니다.

다음과 같은 기능을 추가한다면:

- 분석기 정지(freeze)
- 프리셋 이름
- 커스텀 밴드 모드
- 사용자 정의 라우팅

그때 `SaveCustomState`와 `LoadCustomState`를 추가하세요.

## 8. 유틸리티 플러그인 만들기

Ableton Utility 스타일 플러그인은 “작지만 유용한” host 동작을 보여 주기에 더 좋은 예제입니다.

일반적인 Utility 컨트롤:

- 게인
- 팬
- 폭(width)
- 모노 켜기/끄기
- L/R 위상 반전
- 뮤트

### 8.1 파라미터

단순하고 명확한 값을 사용하세요:

```go
_ = params.Add(
	param.GainParameter(1, "Input Gain").Default(0).Build(),
	param.GainParameter(2, "Output Gain").Default(0).Build(),
	param.PanParameter(3, "Pan").Build(),
	param.MixParameter(4, "Width").Default(100).Build(),
	param.New(5, "Mono").Toggle().Build(),
	param.New(6, "Phase Invert L").Toggle().Build(),
	param.New(7, "Phase Invert R").Toggle().Build(),
	param.New(8, "Mute").Toggle().Build(),
)
```

스테레오 확장을 어떻게 정의하느냐에 따라 `Width`를 `MixParameter`로 만들거나 `0..200%` 커스텀 컨트롤로 만드세요.

### 8.2 처리 전략

처리 순서는 다음과 같습니다:

1. 입력 게인 적용
2. 선택적으로 모노로 합치기
3. 팬 적용
4. 폭/스테레오 매트릭스 적용
5. 위상 반전 적용
6. 출력 게인 적용
7. 뮤트 상태라면 출력을 0으로 만들기

이렇게 하면 사용자에게 예측 가능한 멘탈 모델을 제공합니다.

### 8.3 처리 형태 예제

```go
func (p *Processor) ProcessAudio(ctx *process.Context) {
	if ctx.Param(8) >= 0.5 {
		ctx.Clear()
		return
	}

	inputGain := ctx.ParamPlain(1)
	outputGain := ctx.ParamPlain(2)
	pan := ctx.ParamPlain(3)
	width := ctx.ParamPlain(4)
	mono := ctx.Param(5) >= 0.5
	invertLeft := ctx.Param(6) >= 0.5
	invertRight := ctx.Param(7) >= 0.5

	ctx.ProcessStereo(func(ch int, input, output []float32) {
		for i := range input {
			sample := input[i]

			if ch == 0 && invertLeft {
				sample = -sample
			}
			if ch == 1 && invertRight {
				sample = -sample
			}

			sample = applyGain(sample, inputGain, 0)

			if mono {
				// Downstream implementation averages both channels.
				// This branch is just the structural idea.
			}

			if ch == 0 {
				sample = applyPanLeft(sample, pan, width)
			} else {
				sample = applyPanRight(sample, pan, width)
			}

			sample = applyGain(sample, outputGain, 0)
			output[i] = sample
		}
	})
}
```

정확한 게인 계산은 여러분에게 달려 있습니다. 요점은 플러그인 동작을 사용자 중심적이고 단순하게 유지하는 것입니다.

## 9. 에디터 통합

이 저장소의 에디터 경로는 `EditorModel`과 `EditorSnapshot` 위에 구축되어 있습니다.

즉, 플러그인은 다음 관점에서 생각합니다:

- 파라미터 메타데이터
- 현재 정규화된 값
- 표시용 plain 값
- hidden/read-only 의미

### 9.1 에디터가 받는 것

에디터 모델은 파라미터 레지스트리와 플러그인 메타데이터로부터 만들어집니다.

여러분이 할 일은 레지스트리를 깔끔하게 유지하는 것입니다:

- 올바른 ID
- 올바른 이름
- 올바른 min/max/기본값
- 올바른 플래그
- 적절한 짧은 이름과 단위

### 9.2 에디터가 하지 말아야 할 것

에디터는 결코 자체적인 상태 소스를 만들어 내지 않습니다.

대신:

- host가 파라미터를 편집하고
- 런타임이 레지스트리를 갱신하고
- 에디터가 그 레지스트리를 반영하며
- 스냅샷을 저장하고 복원할 수 있습니다

### 9.3 숨김 컨트롤과 읽기 전용 컨트롤

다음 플래그를 신중하게 사용하세요:

- 내부용이거나 지원되지 않는 컨트롤에는 `IsHidden`
- 표시만 하고 편집하지 않는 미터나 값에는 `IsReadOnly`
- host가 값을 기록하는 대상에는 `CanAutomate`

미터를 추가한다면 읽기 전용으로, 오토메이션 불가능하게 만드세요.

## 10. 상태 저장과 복원

파라미터 값으로 충분하다면 커스텀 상태를 추가하지 마세요.

그렇지 않다면 `StatefulProcessor`를 구현합니다:

```go
type Processor struct {
	params *param.Registry
	buses  *bus.Configuration

	presetName string
	lastMode   int32
}

func (p *Processor) SaveCustomState(w io.Writer) error {
	// write custom fields after parameter state
	return nil
}

func (p *Processor) LoadCustomState(r io.Reader) error {
	// restore custom fields after parameter state
	return nil
}
```

커스텀 상태는 다음과 같은 경우에 사용합니다:

- 프리셋 이름
- 파라미터화되지 않은 모드 선택
- 내부 라우팅 선택
- 분석 표시 설정

타당한 이유가 없다면 일반 파라미터 값을 중복 저장하는 데 커스텀 상태를 사용하지 마세요.

## 11. 다운스트림 플러그인 저장소의 권장 파일 레이아웃

저장소는 `vst3go new`로 시작하세요(README의 빠른 시작 참고). 이 명령은 오프라인
테스트를 포함한 동작하는 이펙트, 악기(`-type instrument`), MIDI 이펙트(`-type midi`)
모듈을 작성하며, `go tool vst3go build -install . <Name>`으로 거기서 모든 포맷을 빌드하고
설치합니다. 플러그인이 커지면 다음 레이아웃이 좋습니다:

```text
my-plugin/
  cmd/
  internal/
    dsp/
    ui/
  plugin/
    processor.go
    plugin.go
    state.go
  web/
    editor/
      index.html
      assets/
  justfile
```

권장 분할:

- `plugin/processor.go`는 런타임 프로세서를 소유합니다
- `plugin/plugin.go`는 `Plugin` 엔트리포인트와 메타데이터를 소유합니다
- `plugin/state.go`는 파라미터 값으로 충분하지 않을 때의 커스텀 상태를 소유합니다
- `internal/dsp/`는 `pkg/dsp` 블록으로 만든 플러그인 전용 신호 흐름을 소유합니다
- `web/editor/`는 브라우저로 렌더링되는 UI를 만든다면 그 UI를 소유합니다

`vst3go` 자체는 제품 작업 공간이 아니라 런타임 의존성으로 유지하세요.

## 12. 실제 플러그인 프로젝트에 대해 생각하는 법

새 플러그인을 시작할 때는 다음 질문에 순서대로 답하세요:

1. 사용자는 무엇을 하려고 하는가?
2. 실제로 중요한 컨트롤 5~12개는 무엇인가?
3. 모노, 스테레오, 사이드체인, 멀티버스 중 무엇인가?
4. 처리 토폴로지는 무엇인가?
5. 다시 로드한 뒤에도 유지되어야 하는 상태는 무엇인가?
6. 에디터가 즉시 보여 줘야 하는 것은 무엇인가?
7. 오토메이션이 가능해야 하는 것은 무엇인가?
8. 숨겨지는 것은 무엇인가?

이 순서가 첫 버전을 정의합니다. 이 여덟 가지 답이 확정되기 전까지는 추가 분기를 더하지 마세요.

## 13. 권장 개발 순서

새 플러그인은 다음 순서로 작업하고, 단계를 건너뛰지 마세요:

1. 플러그인 메타데이터 정의
2. 버스 정의
3. 파라미터 정의
4. 프로세서 라이프사이클 구현
5. 기본 오디오 알고리즘 구현
6. 필요하면 상태 저장/복원 추가
7. 에디터 모델 노출
8. 에디터 셸 연결
9. `go test ./...`로 테스트(`pkg/plugintest`가 플러그인을 오프라인으로 렌더링)
10. `go tool vst3go build -install . <Name>`으로 빌드 및 설치하고 host에서 열기
11. Windows가 범위에 포함된다면 Windows 검증 실행

1~11단계를 마치면 빌드하고, 열고, 편집하고, 저장하고, 복원하고, 검증할 수 있는 하나의 다운스트림 플러그인이 완성됩니다.

## 14. 실용적인 예제 워크플로

다운스트림 저장소의 워크플로는 다음과 같습니다:

1. 스테레오 유틸리티 플러그인으로 시작합니다.
2. 에디터에서 게인, 팬, 폭, 뮤트를 확인합니다.
3. 마지막으로 선택한 모드에 대한 상태 저장/복원을 추가합니다.
4. DSP 계층을 교체하여 유틸리티를 `Eight EQ` 스타일 플러그인으로 바꿉니다.
5. 백엔드가 바뀌는 동안 파라미터 계약을 안정적으로 유지합니다.
6. 같은 웹 에디터 셸을 재사용하고, 그 셸에 공급하는 데이터만 바꿉니다.

이 순서를 따르면 서로 관련 없는 실험의 모음이 아니라 하나의 일관된 플러그인이 만들어집니다.

## 15. 피해야 할 것

- 제품 전용 로직(라이선스, 브랜딩, 인스톨러)을 `vst3go`에 넣지 마세요. 범용 DSP는 `pkg/dsp`에 둡니다
- 브라우저 에디터를 별도의 앱으로 취급하지 마세요
- 추가 상태를 보관하려고 파라미터 레지스트리를 우회하지 마세요
- 측정하고 그 트레이드오프를 받아들인 경우가 아니라면 `ProcessAudio`에서 메모리를 할당하지 마세요
- 플랫폼 글루 코드가 비즈니스 로직을 소유하게 하지 마세요
- 중요한 사용자 컨트롤을 문서화되지 않은 동작 뒤에 숨기지 마세요

### 15.1 성능 측정과 튜닝

- `plugintest`나 `plugin.NewOffline` 위에서 `go test -bench`로 프로세서를 벤치마크하고,
  `plugintest.AllocsPerProcess`는 0으로 유지하세요.
- `go run ./internal/rtcheck`는 다른 고루틴이 메모리를 할당하는 동안 실시간 스레드에서
  처리 체인을 돌리고, 호출 시간을 블록 예산과 비교해 보고합니다.
- 프로파일 기반 최적화(PGO): 부하를 건 프로세서의 CPU 프로파일을 플러그인 main 패키지
  디렉터리에 `default.pgo`로 두면(그 디렉터리에서 `go test -bench . -cpuprofile default.pgo`
  실행) `vst3go build`를 포함한 모든 `go build`가 이를 사용합니다. DSP 체인은 보통 몇
  퍼센트 빨라집니다.
- 맞는 블록이 있으면 `pkg/dsp`를 쓰세요. dB 변환에 `math.Pow` 대신 `math.Exp`를 쓰고,
  radix-4 FFT와 비균일 분할 컨볼루션을 쓰며, 저장소에서 벤치마크됩니다.

## 16. 새 플러그인을 위한 검증 체크리스트

플러그인이 “완성”되었다고 말하기 전에 다음을 확인하세요:

- host에서 로드됨
- 크래시나 글리치 없이 오디오를 처리함
- 파라미터가 에디터에 올바르게 반영됨
- 상태가 저장과 다시 로드 후에도 유지됨
- 버스 레이아웃이 의도한 사용 사례와 일치함
- 실시간 경로가 메모리 할당을 의식하고 있음
- 빌드와 패키징 방법이 문서화되어 있음

Windows가 범위에 포함된다면 다음도 확인하세요:

- DLL이 빌드됨
- 번들 레이아웃이 올바름
- 에디터가 WebView2에서 열림
- 반복해서 다시 열어도 안정적임
- 플러그인이 host의 다시 로드와 재스캔에도 문제없이 동작함

## 17. 마치며

가장 좋은 `vst3go` 플러그인 코드는 필요한 곳에서 아주 지루합니다:

- 메타데이터가 명시적이고
- 파라미터 이름이 신중하게 지어지고
- 버스가 명확하고
- 처리가 결정적이고
- 상태가 의도적이며
- 에디터가 런타임을 대체하지 않고 반영합니다

이 규칙들을 지킨다면 단순한 EQ부터 세련된 Utility 스타일 플러그인까지 프레임워크와 씨름하지 않고 무엇이든 만들 수 있습니다.

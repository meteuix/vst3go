[English](plugin-capabilities.md) | 한국어

# 플러그인 기능

런타임이 지원하는 기능과, 프로세서가 각 기능을 선택적으로 사용(opt in)하는 방법을 설명합니다.
`internal/validation/` 아래의 플러그인은 여기에 나오는 모든 항목을 실제로 사용하며,
Steinberg validator의 extensive 모드(`just validate-vst3 <validator>`)를 통과합니다.
또한 CLAP 플러그인으로서는 clap-validator(`just validate-clap <clap-validator>`)를,
Audio Unit으로서는 Apple의 auval strict 모드(`just validate-au`)를,
LV2 플러그인으로서는 lv2_validate와 lv2lint(`just validate-lv2`, Linux)를 각각 통과합니다.

| 플러그인 유형 | Bus 템플릿 | 검증용 플러그인 |
| --- | --- | --- |
| 오디오 이펙트 (모노/스테레오, 서라운드) | `bus.NewEffectStereo`, `NewSurround5_1Effect`, `NewMultiChannelEffect` | `gain` |
| 사이드체인 이펙트, 룩어헤드/레이턴시 | `bus.NewEffectStereoSidechain` | `ducker` |
| 인스트루먼트 (신스, 샘플러, 드럼) | `bus.NewGenerator` | `synth` |
| MIDI 이펙트 (아르페지에이터, 트랜스포저) | `bus.NewMIDIEffect` | `midifx` |
| 애널라이저/미터 | `bus.NewAnalyzer` + 읽기 전용 파라미터 | `gain` (미터) |
| 멀티 출력 (크로스오버, 드럼 아웃) | `bus.NewCrossover`, `NewSplitter`, `WithAuxOutput` | — |

## 플러그인 빌드

플러그인은 스스로를 등록하는 `main` 패키지입니다.

```go
func init() { plugin.Register(myPlugin{}) }
func main() { standalone.Main() } // runs only in standalone application builds
```

`vst3go new [-type effect|instrument|midi] <dir> <Name>` 명령은 이러한 패키지를
독립된 모듈로 생성하며(`pkg/plugintest`를 사용하는 `main_test.go`가 포함되고,
`vst3go`가 `tool` 의존성으로 추가됩니다), `go tool vst3go build [-format
vst3,clap,au,app] [-install] <package> <Name>` 명령은 어떤 모듈에서든 그
패키지를 빌드합니다.

- `vst3`: `-buildmode=c-shared`로 빌드한 macOS(`Contents/MacOS`) 또는
  Linux(`Contents/<arch>-linux`) 번들이며, `Contents/Resources/moduleinfo.json`
  (VST 3.7 모듈 정보)을 포함합니다. 이 파일은 `scripts/moduleinfo.c`로 빌드된
  바이너리를 로드하여 생성합니다.
- `clap`, `au`, `lv2`: 같은 바이너리를 CLAP 플러그인, (macOS) Audio Unit,
  Turtle 파일이 포함된 (Linux) LV2 번들로 패키징합니다.
- `app`: 스탠드얼론 애플리케이션.

`-install`은 플러그인을 사용자 폴더(`~/Library/Audio/Plug-Ins/{VST3,CLAP,Components}`,
`~/.vst3`, `~/.clap`, `~/.lv2`)에 복사합니다.
`-id`(번들 식별자), `-version`, `-tags`는 번들 세부 정보와 빌드 태그를 설정하며,
`GOFLAGS`에 지정된 태그는 유지됩니다. 이 저장소에서는
`scripts/build_{vst3,clap,au,standalone}.sh` 스크립트(`just vst3`, `just clap` 등)가
각각 한 가지 형식에 대해 이 명령을 실행합니다. Windows에서는 mingw 크로스 빌드
(`scripts/docker_build_windows.sh`) 또는 `scripts/build_windows_vst3.sh`를 사용합니다.

하나의 바이너리에 여러 플러그인을 담을 수 있습니다. 플러그인마다 `plugin.Register`를
한 번씩 호출하며(각 플러그인은 고유한 `Info.ID`를 가집니다), 팩토리가 모든 클래스를
나열합니다.

같은 바이너리는 CLAP 플러그인이기도 합니다. `GetPluginFactory`와 함께 `clap_entry`도
export하기 때문입니다. `vst3go build -format clap`은 이를 macOS `.clap` 번들 또는
Linux `.clap` 공유 라이브러리로 패키징하며, Windows 크로스 빌드는 `<name>.clap` DLL을
생성합니다. 런타임이 CLAP에 어떻게 매핑되는지는 [CLAP](#clap) 섹션을 참고하십시오.

macOS에서는 Audio Unit(AUv2)이기도 합니다. `vst3go build -format au`는 등록된
플러그인마다 컴포넌트가 하나씩 들어 있는 `.component` 번들을 생성합니다.
[Audio Units](#audio-units) 섹션을 참고하십시오.

Linux에서는 LV2 플러그인이기도 합니다. `vst3go build -format lv2`는 `.lv2` 번들을
생성합니다. [LV2](#lv2) 섹션을 참고하십시오.

스탠드얼론 애플리케이션으로도 빌드할 수 있습니다: `vst3go build -format app`.
[스탠드얼론 애플리케이션](#스탠드얼론-애플리케이션) 섹션을 참고하십시오.

`plugin.Info.Category`는 host 카테고리를 선택합니다. 예를 들어 `CategoryFxEQ`,
`CategoryFxDynamics`, `CategoryInstrumentSynth`가 있으며, 여러 개를 `|`로 조합할 수 있습니다.

## 플러그인 테스트

`pkg/plugintest`는 `go test` 안에서 host 없이 플러그인을 실행합니다. VST3 host가 구동하는
것과 동일한 런타임(라이프사이클, 샘플 단위로 정확한 오토메이션, MIDI 컨트롤러 매핑,
프로그램, 스무딩, 상태, 프리셋)을 그대로 거칩니다:

```go
func TestSynth(t *testing.T) {
	p := plugintest.New(t, synthPlugin{})
	p.Note(0, 69, 1, 300, 0)                                  // A4 at sample 300
	p.SendController(0, plugin.MIDIControllerPitchBend, 0, 1) // via IMidiMapping
	p.Automate(paramCutoff, 1000, 0.25)                       // sample-accurate
	out := p.RenderSilence(48000, 0, 2)
	if f := plugintest.ZeroCrossingFrequency(out[0][1000:], 48000); ... {}
}
```

- `plugintest.New`는 인스턴스를 생성하고 활성화합니다(샘플 레이트, 블록 크기, 64비트,
  프로세스 모드는 `plugin.OfflineSetup`으로 지정). `Render`, `RenderSilence`,
  `ProcessBuses`(사이드체인, aux 출력), `Process64`, `ProcessEvents`(MIDI 이펙트)는
  임의 길이의 버퍼를 host 크기의 블록 단위로 처리하며, 테스트에서 지정하지 않은 bus는
  무음으로 채웁니다.
- `Transport`는 템포와 재생 상태를 설정하며, 재생 중에는 앞으로 진행됩니다.
- `OutputEvents`, `OutputParameters`(미터), `Latency`, `TakeRestarts`, `TakeMessages`는
  플러그인이 보고한 내용을 노출하며, `State`/`SetState`로 상태를 왕복(round trip)시킬
  수 있습니다.
- 신호 헬퍼(`Sine`, `Impulse`, `Constant`, `Silence`)와 분석 함수(`Peak`, `RMS`,
  `PeakDB`, `FirstNonZero`, `ZeroCrossingFrequency`).
- `LoadAudio`는 렌더에 사용할 녹음된 소재를 디코딩하고, `SaveAudio`는 들어볼 수 있도록
  렌더 결과를 기록합니다(`pkg/audiofile`).

`plugin.Offline`은 `testing` 없이 쓸 수 있는 동일한 엔진으로, 오프라인 렌더러와 배치
도구용입니다. `internal/validation`의 각 플러그인에는 이 방식으로 작성한 테스트가 있습니다.

## 오디오 파일

`pkg/audiofile`은 샘플러, 임펄스 응답, 웨이브테이블, 오프라인 렌더, 테스트에 쓸
오디오 파일을 읽고 씁니다:

```go
b, err := audiofile.Load(path)          // or DecodeBytes(embedded)
// b.SampleRate, b.Channels[ch][frame]

r, err := audiofile.Open(path)          // streaming
info := r.Info()                        // format, rate, channels, frames, bit depth, root note, loops
n, err := r.Read(dst)                   // dst[ch][:n], io.EOF at the end
err = r.SeekFrame(frame)

err = audiofile.Save("render.wav", b, audiofile.Float32)
w, err := audiofile.Create("take.flac", 48000, 2, audiofile.PCM24) // Write blocks, then Close
```

- WAV(RIFF 및 RF64, 8~32비트 PCM, 32/64비트 float, `WAVE_FORMAT_EXTENSIBLE`),
  AIFF 및 AIFF-C(PCM, `sowt`, `fl32`, `fl64`), FLAC, Ogg Vorbis, MP3를 읽으며,
  포맷은 파일 내용으로 판별합니다.
- 샘플 루프와 루트 노트는 WAV `smpl` 청크와 AIFF `INST`/`MARK` 청크에서
  가져옵니다(`Info.Loops`, `Info.RootNote`).
- WAV(16/24/32비트 PCM, 32비트 float, 2채널을 넘으면 extensible), AIFF(PCM, float는
  AIFF-C), FLAC(16/24비트, 최대 8채널)를 쓰며, 정수 샘플은 클리핑합니다. `Encode`는
  임의의 `io.Writer`에 기록합니다.
- 파일 디코딩은 `ProcessAudio`가 아니라 백그라운드 고루틴에서 수행합니다. 디코딩된
  `Buffer`는 `atomic.Pointer`를 통해 프로세서에 넘겨줍니다. 샘플 레이트 변환은 DSP
  영역이므로 이 패키지에 포함되지 않습니다.
- MP3는 2채널로 디코딩되며 인코더 딜레이와 패딩을 그대로 유지합니다(gapless 트리밍
  없음). Vorbis와 무손실 포맷은 샘플 단위로 정확합니다.

테스트는 afconvert, flac, oggenc, lame으로 만든 픽스처
(`pkg/audiofile/testdata/generate.sh`)를 디코딩하고, 기록한 파일을 macOS에서는
afconvert로, Linux에서는 `flac -t`와 sox로 검사합니다.

## DSP 라이브러리

`pkg/dsp`에는 `examples/`의 쇼케이스 플러그인을 구성하는 신호 처리 블록이
있습니다. 프로세서는 생성자나 `Initialize`에서 만들고 크기를 정하며, 처리
메서드는 메모리를 할당하지 않습니다([런타임 계약](runtime-contracts.ko.md) 참고).

| 패키지 | 내용 | 사용하는 예제 |
| --- | --- | --- |
| `filter` | `Biquad`(로우패스, 하이패스, 밴드패스, 노치, 피킹, 셸프), `SVF`, `MultiModeSVF` | `filter`, `auto_params`, `mastercompressor`, `drumbus`, `surround`, `vocalstrip` |
| `dynamics` | `Compressor`(소프트/하드 니, 룩어헤드, 사이드체인), `Limiter`(트루 피크 룩어헤드 브릭월), `Gate`(히스테리시스, 홀드, 키 필터), `Expander`, `TransientShaper` | `mastercompressor`, `masterlimiter`, `studiogate`, `sidechain`, `transientshaper`, `drumbus`, `vocalstrip`, `auto_params`, `chain_fx` |
| `envelope` | ADSR/AR 엔벨로프, 피크/RMS `Detector`, `Follower` | `wavetablesynth`, `fmsynth`, `pkg/dsp/dynamics`, `pkg/dsp/sampler` |
| `delay` | `Line`, `MultiTap`, `Comb`, `Allpass`, `Modulated` | `delay` |
| `modulation` | `LFO`, `Chorus`, `Flanger`, `Phaser`, `Tremolo`, `RingModulator` | `vintagechorus`, `jetflanger` |
| `distortion` | `Waveshaper`, `TubeSaturation`, `TapeSaturation`, `Bitcrusher` | `multidistortion` |
| `reverb` | `Schroeder`, `Freeverb`, `FDN` | |
| `oscillator` | 기본 파형, `BLIT`, 대역 제한 톱니파, 밉맵 `Wavetable`/`WavetableBank`, FM `Operator` | `wavetablesynth`, `fmsynth` |
| `utility`, `gain`, `pan`, `mix`, `interpolation` | 노이즈, DC 블로커, dB/팬 법칙, 믹싱, 보간 | `gain`, `delay`, `filter`, `chain_fx` |
| `fft` | 복소수 및 packed real radix-2 FFT | `pkg/dsp/convolution`, `pkg/dsp/spectral`, `pkg/dsp/pitch` |
| `spectral` | `STFT`: 윈도 overlap-add 프레임 처리 | `pkg/dsp/pitch` |
| `resample` | `Oversampler`(2x~16x 하프밴드 캐스케이드), 스트리밍 `Resampler`와 `Convert`(카이저 윈도 sinc, 임의 비율), 공유 sinc `Kernel` | `saturator`, `pkg/dsp/sampler` |
| `convolution` | `Convolver`: 지연 없는 균일 분할 컨볼루션 | `convreverb` |
| `pitch` | 딜레이 라인 `Shifter`, 페이즈 보코더 `SpectralShifter`, 오프라인 타임 `Stretch`, YIN `Detector` | `pitchshifter`, `tuner` |
| `granular` | `Granulator`: 스프레이, 피치, 스테레오 확산을 갖춘 그래뉼러 딜레이와 프리즈 | `granulator` |
| `sampler` | `Sample`(루프, 절반 레이트 사본), 키/벨로시티 `Map`, 대역 제한 `Voice` | `sampler` |
| `analysis` | FFT, 스펙트럼, 피크/RMS/LUFS(BS.1770-4, EBU R 128 LRA) 미터, 상관도, 위상 스코프 | |

`pkg/framework/dsp`는 프로세서를 체인으로 묶고(`NewChain`, `NewParallelChain`,
빌더와 어댑터, `examples/chain_fx` 참고), `pkg/framework/voice`는 폴리포닉 보이스를
할당하며(`examples/simplesynth`), `pkg/framework/debug`에는 로거, 할당 없는
오디오 프로파일러, 버퍼 분석기가 있습니다(`examples/debug_example`).

## 처리 모델

`ProcessAudio(ctx)`는 오디오 스레드에서 실행됩니다. 락, 블로킹, I/O를 해서는 안 됩니다.

- **버스.** `ctx.Input`/`ctx.Output`은 메인 버스입니다.
  `ctx.InputBus(i)`/`ctx.OutputBus(i)`로 모든 버스에, `ctx.Sidechain()`으로 첫 번째
  aux 입력에 접근합니다. 비활성 버스나 연결되지 않은 버스에는 채널이 없습니다.
- **샘플 단위로 정확한 오토메이션.** 런타임은 host 블록을 파라미터 변경 오프셋에서
  분할하므로, `ctx.Param`/`ctx.ParamPlain`은 항상 현재 구간에서 유효한 값을 담고
  있습니다. `ctx.NumSamples()`는 구간 길이이고, `ctx.BlockOffset()`은 host 블록 안에서
  그 구간이 시작하는 위치입니다.
- **이벤트**는 host 블록 기준 오프셋을 유지합니다. 구간 안에서 이벤트의 샘플
  인덱스는 `int(event.Offset) - ctx.BlockOffset()`입니다.
  `ctx.GetAllInputEvents()`는 해당 구간의 이벤트를 순서대로 반환합니다.
- **스무딩.** `param.New(...).Smooth(seconds)`(선형: 게인, 믹스, 팬) 또는
  `.SmoothMultiplicative(seconds)`(주파수, 시간)로 파라미터에 램프 시간을 지정합니다.
  `ctx.Smoothed(id)`는 plain 단위의 `*param.Ramp`를 반환하며, 이 램프는 각 구간이
  시작될 때 새 목표값으로 재설정됩니다. 샘플마다 `Next()`를 호출하거나 `Fill(buf)`,
  `Skip(n)`으로 진행시킵니다. 램프는 구간과 블록을 넘어 계속 진행되며, 처리가 시작될
  때 현재 값에서 다시 시작합니다. DSP 내부 값에는 `param.NewRamp`를 사용할 수 있습니다.
- **무음 플래그.** `ctx.InputSilenceFlags(bus)`는 무음인 입력 채널을 알려 주고,
  `ctx.SetOutputSilenceFlags(bus, mask)`는 출력이 무음임을 host에 알립니다.
- **출력 파라미터**(미터, 게인 리덕션): `ReadOnly()` 파라미터에
  `ctx.SetOutputParameter(id, normalized)`를 사용합니다. 값은 host와 에디터로 전달됩니다.
- **트랜스포트.** `ctx.Transport`는 템포, 박자표, 음악적 위치, 사이클, 재생/녹음
  상태를 담고 있으며, 각 필드 그룹에는 `Has*` 유효성 플래그가 있습니다.
- **오프라인 렌더링.** `ctx.ProcessMode`는 `ProcessModeRealtime`,
  `ProcessModePrefetch`, `ProcessModeOffline` 중 하나입니다.
- **64비트 처리.** `DoublePrecisionProcessor`를 구현하고
  `SupportsDoublePrecision()`에서 true를 반환합니다. host가 64비트를 선택하면
  `ctx.Is64Bit()`가 true가 되고 오디오는 `ctx.Input64`/`ctx.Output64`
  (`InputBus64`, `OutputBus64`, `Sidechain64`)에 들어 있으며, `ctx.Input`/`ctx.Output`은
  nil입니다. 제네릭 헬퍼(`func apply[T float32 | float64](in, out [][]T)`)를 쓰면 두
  경우 모두 하나의 DSP 경로로 처리할 수 있습니다. `internal/validation/gain`을
  참고하세요.
- 오디오 스레드 경로는 메모리 할당이 없습니다. 이벤트, 파라미터 변경, 구간 뷰는
  `setupProcessing` 시점에 크기가 정해진 버퍼를 재사용합니다.

## MIDI와 노트

입력 이벤트는 `midi.Event` 값(메모리 할당이 없는 단순 구조체)으로 도착하며, `Type`에
따라 의미 있는 필드가 정해집니다:

- `EventTypeNoteOn`/`EventTypeNoteOff`: `Note`, 0~1 범위의 `Velocity`(7비트 값은
  `Velocity7()`), `Tuning`(센트), `Length`, host의 `NoteID`(할당되지 않았으면
  `midi.NoteIDNone`). 양쪽 모두 `NoteID`를 가지고 있다면 노트 오프는 `NoteID`로
  매칭합니다.
- `EventTypePolyPressure`, `EventTypeNoteExpression`(float `Value`, 타입은
  `midi.NoteExpressionVolume`, `...Tuning`, `...Brightness`, ...),
  `EventTypeNoteExpressionInt`(`IntValue`), `EventTypeNoteExpressionText`
  (UTF-8 `Data`, 예: 음소).
- host 코드 트랙에서 오는 `EventTypeChord`(`Root`, `Bass`, `Mask`, 이름은 `Data`)와
  `EventTypeScale`(`Root`, `Mask`, 이름은 `Data`).
- 전체 메시지를 `Data`에 담은 `EventTypeSysEx`.

입력 이벤트의 `Data`는 `ProcessAudio` 호출 동안에만 유효하므로, 보관하려면 복사해야
합니다.

VST3 host는 MIDI CC, 피치 벤드, 채널 애프터터치를 이벤트로 보내지 않습니다. 이를
받는 방법은 두 가지입니다:

- `MIDIControllerProcessor.MIDIControllerChannels() int` — 런타임이 모든 컨트롤러를
  숨겨진 파라미터(`MIDIControllerParamIDBase`부터 시작하는 ID)에 매핑하고, 그 변경을
  다시 샘플 단위로 정확한 `EventTypeControlChange`, `EventTypePitchBend`(-1..+1),
  `EventTypeChannelPressure` 입력 이벤트로 바꿔 줍니다. 플러그인 파라미터 ID는
  `MIDIControllerParamIDBase`보다 작아야 합니다.
- `MIDIMappingProcessor.MIDIControllerParameter(bus, channel, cc)` — 컨트롤러를
  플러그인의 파라미터 중 하나에 직접 매핑합니다(모드 휠을 비브라토 깊이에). 이 매핑이
  우선합니다. 컨트롤러 번호는 0~127에 더해 `MIDIControllerAfterTouch`(128)와
  `MIDIControllerPitchBend`(129)가 있습니다.
- `MIDI2MappingProcessor`는 MIDI 2.0 RPN/NRPN 컨트롤러를 매핑합니다(`IMidiMapping2`).
- `MIDILearnProcessor`/`MIDI2LearnProcessor`는 host의 MIDI learn 모드에서 사용자가
  움직인 컨트롤러를 받습니다(`IMidiLearn`/`IMidiLearn2`). 매핑을 갱신한 뒤
  `Host.RestartComponent(RestartMidiCCAssignmentChange)`를 호출합니다.

MIDI 프로그램 체인지는 이벤트가 아니라 프로그램 목록(아래 참고)을 통해 플러그인에
전달됩니다.

출력 이벤트: 이벤트 출력 버스가 있을 때 `midi.NoteOn(...)`, `midi.ControlChange(...)`,
`midi.SysEx(data)`, ... 와 `.At(offset)`으로 만든 이벤트를 `ctx.AddOutputEvent(event)`로
내보냅니다. 노트, 폴리 프레셔, 노트 익스프레션(float, int, text), SysEx는 VST3 이벤트로
나가고, CC, 피치 벤드, 채널 프레셔, 프로그램 체인지는 레거시 MIDI CC 이벤트로
나갑니다. 메모리 할당 없이 처리하려면 SysEx/텍스트 페이로드 메모리를
`ctx.EventDataBuffer(n)`/`ctx.AppendEventText(utf16)`에서 받아 쓰세요. 코드와 스케일
이벤트는 입력 전용입니다.

## 악기: 노트 익스프레션, MPE, 아티큘레이션

- `NoteExpressionProcessor.NoteExpressions(bus, channel)`은 노트별 익스프레션을
  알립니다(`INoteExpressionController`). Cubase는 이를 알리는 플러그인에만 노트
  익스프레션을 보냅니다. `NoteExpressionInfo.Format`/`Parse`는 host 표시용으로 값을
  변환합니다.
- `PhysicalUIMappingProcessor`는 MPE 차원(X/Y 움직임, 프레셔)을 노트 익스프레션
  타입에 매핑합니다(`INoteExpressionPhysicalUIMapping`).
- `KeyswitchProcessor`는 host 익스프레션 맵을 위해 아티큘레이션 키스위치를
  공개합니다(`IKeyswitchController`).

## 프리셋

`pkg/preset`은 host가 프리셋 브라우저에서 사용하는 형식인 VST3 프리셋 파일
(`.vstpreset`)을 읽고 씁니다:

- `Host.SavePreset(path)`는 현재 상태(파라미터와 `StatefulProcessor` 데이터)를 플러그인
  이름 및 카테고리 메타 정보와 함께 기록합니다. `Host.LoadPreset(path)`는 이
  플러그인의 프리셋을 복원하고 host와 에디터를, 분산 플러그인에서는 반대편까지
  갱신합니다.
- `preset.UserDir(vendor, name)`은 host가 플러그인의 프리셋을 찾는 사용자별
  폴더이며, `preset.List`, `preset.Save`, `preset.PathFor`로 관리합니다. 내장
  에디터에는 이 기능들 위에 만든 프리셋 바가 있습니다.
- `preset.Decode`/`File.Encode`와 `preset.MetaInfo`는 파일 형식을 직접 다룹니다
  (팩토리 콘텐츠 도구, 변환기).

`pkg/preset`으로 기록한 프리셋은 VST3 SDK의 `PresetFile`로 로드되며, 그 반대도
마찬가지입니다(`just validate-vst3`).

## 프로그램과 유닛

- `ProgramListProcessor.ProgramList()`는 팩토리 프리셋을 host의 프로그램 메뉴에
  공개합니다(`IUnitInfo`, `IProgramListData`). 런타임은 `kIsProgramChange` 플래그가
  붙은 리스트 파라미터 `ProgramParamID`를 추가합니다. host, 오토메이션, MIDI 프로그램
  체인지로 프로그램을 선택하면 그 값이 로드됩니다(오디오 스레드에서는 샘플 단위로
  정확하게, 컨트롤러에서는 파라미터 새로 고침과 함께). `Program.Values`에 없는
  파라미터는 기본값을 받고, 읽기 전용 파라미터와 바이패스 파라미터는 그대로
  유지됩니다. `Program.PitchNames`는 노트 이름(드럼 패드, 키스위치)을 지정합니다.
  프로젝트를 복원하면 저장된 값 위에 프로그램을 다시 적용하지 않고 선택 상태만
  복원합니다.
- `UnitProcessor.Units()`는 파라미터를 그룹으로 묶습니다(`param.Parameter.UnitID`).
  유닛 0은 루트이며 프로그램 목록을 소유합니다.

## 버스 레이아웃

- `bus.NewBuilder()`나 템플릿으로 버스를 구성합니다. 메인 버스가 먼저 오고 그 뒤에
  aux 버스가 옵니다. 메인 버스는 기본적으로 활성 상태로 보고되며, aux 버스는 host가
  필요할 때 활성화합니다.
- `WithAudioInputArrangement`/`WithAudioOutputArrangement`
  (`bus.Arrangement51`, `bus.Arrangement71Cine`, ...)로 명시적인 스피커 레이아웃을
  지정합니다.
- `BusArrangementProcessor.AcceptBusArrangements(inputs, outputs) bool`을 구현하면
  host가 제안하는 다른 레이아웃을 받아들일 수 있습니다. true를 반환하면 런타임이
  처리 시작 전에 버스 구성과 채널 수를 갱신합니다.

## 레이턴시와 테일

- `GetLatencySamples()`는 처리 레이턴시(룩어헤드, 선형 위상 필터)를 보고합니다.
  레이턴시가 바뀌면 오디오 스레드가 아닌 스레드에서
  `Host.RestartComponent(plugin.RestartLatencyChanged)`를 호출합니다.
- `GetTailSamples()`는 입력이 멈춘 뒤의 감쇠 길이를 보고합니다. 감쇠하지 않는
  제너레이터는 `plugin.TailInfinite`를 반환합니다.

## Host와의 상호작용

host에 다시 요청을 보내려면 `HostAwareProcessor.SetHost(plugin.Host)`를 구현합니다
(UI/메인 스레드에서만 사용):

- `EditParameter(id, normalized)` — 플러그인 쪽에서 일어난 파라미터 변경(프리셋 로드,
  MIDI learn, 커스텀 컨트롤)을 host가 편집으로 기록하게 합니다.
- `BeginEdit(id)`/`EndEdit(id)` — 연속적인 변경(드래그)을 감싸서 host가 하나의
  오토메이션 패스와 하나의 실행 취소 단계로 기록하게 합니다. 그 사이의
  `EditParameter` 호출은 해당 제스처의 단계가 됩니다. 내장 에디터는 슬라이더
  드래그에 이 방식을 사용합니다.
- `RestartComponent(flags)` — host에 레이턴시, 파라미터 값/제목, I/O 레이아웃, MIDI
  할당, 노트 익스프레션, 키스위치를 다시 읽도록 요청합니다.
- `SendMessage(id, data)` — 분산 플러그인의 반대편에 메시지를 보냅니다.

`ChannelInfoProcessor.SetChannelInfo`는 host 채널의 이름, 색상, 위치를 받습니다
(`IInfoListener`).

## 분산 프로세서/컨트롤러

기본적으로 프로세서와 컨트롤러는 하나의 객체(단일 컴포넌트)입니다.
`DistributedPlugin`을 구현하고 `Distributed()`에서 true를 반환하면 별도의 프로세서
클래스(`kDistributable`)와 컨트롤러 클래스를 공개하므로, host가 이들을 서로 다른
프로세스에서 실행할 수 있습니다. 각 쪽은 자체 `CreateProcessor()` 인스턴스를 받습니다:

- 프로세서 쪽은 `Initialize`, `SetActive`, `ProcessAudio`를 실행합니다.
- 컨트롤러 쪽은 파라미터, 에디터, 컨트롤러 인터페이스만 담당합니다.
- 파라미터 값과 상태는 host를 통해 컨트롤러에 전달됩니다. 프로세서 쪽의 `Host`
  호출(`RestartComponent`, `EditParameter`)은 컨트롤러로 전달되고, 그 밖의 모든 것은
  `Host.SendMessage`와 `MessageReceiver.ReceiveMessage`를 거칩니다. `vst3go.`로 시작하는
  메시지 ID는 예약되어 있습니다.

`internal/validation/ducker`를 참고하세요.

## 에디터

웹 에디터는 macOS에서는 WKWebView, Windows에서는 WebView2로 실행됩니다. Linux에서는
host의 X11 창에 임베드되는 WebKitGTK 뷰이며, `webkitgtk` 빌드 태그로 켜야 합니다
(`libwebkit2gtk-4.1-dev` 필요):

```bash
go tool vst3go build -tags webkitgtk ./myplugin MyPlugin
```

GTK는 host의 `IRunLoop` 타이머에서 구동됩니다. 이 태그 없이 빌드한 Linux 빌드에는
에디터 뷰가 없습니다.

동일한 뷰가 VST3(`IPlugView`)와 CLAP(`gui` 확장, [CLAP](#clap) 참고) 모두에 쓰입니다.
`scripts/check_macos_editor.sh`와 `scripts/docker_check_linux_editor.sh`는 두 포맷 모두에서
게인 및 신스 에디터를 실제 창으로 열고, 페이지 <-> Go 브리지를 양방향으로 검사합니다.

프로세서는 내장 페이지를 자체 페이지로 대체할 수 있습니다(`EditorProcessor`,
`plugin.Editor{HTML, Width, Height, Min/Max sizes, FixedSize}`). 모든 페이지는
`window.vst3go` SDK를 받습니다: 편집 제스처를 지원하는 실시간 파라미터, 프리셋,
`resize`, 프로세서와의 커스텀 메시지(`vst3go.send` -> `EditorMessageProcessor`,
`Host.PostEditorMessage` -> `vst3go.on`). `plugin.EditorBindings` / `WriteEditorBindings`는
타입이 지정된 TypeScript 파라미터 바인딩을 생성하고, `plugintest.EditorBindings`는
이를 최신 상태로 유지합니다. [`web-editor-bridge.ko.md`](web-editor-bridge.ko.md)를
참고하세요.

## 파라미터와 상태

- `param.Choice`는 리스트 파라미터(`IsList`, `N-1` 스텝)를 만듭니다. 스텝 파라미터는
  VST3 규칙에 따라 역정규화할 때 가장 가까운 스텝으로 맞춰집니다.
- `param.BypassParameter`에는 `IsBypass` 플래그가 붙어 host가 이를 자체 바이패스
  스위치에 연결합니다.
- 커스텀 파서가 없으면 문자열에서 값으로의 파싱은 포맷팅된 표시 텍스트("-6.0 dB",
  "Q: 0.71", "-∞ dB")를 받아들입니다.
- 파라미터 값은 자동으로 저장됩니다. 추가 상태(샘플 경로, 웨이브테이블, 시퀀서
  패턴)가 필요하면 `StatefulProcessor`를 구현합니다.

## CLAP

CLAP 인스턴스는 단일 컴포넌트 VST3 인스턴스와 동일한 런타임을 실행하므로, 프로세서에
CLAP 전용 코드가 필요하지 않습니다.

- CLAP 플러그인 ID는 `Info.ID`이고, feature는 `Info.Category`에서 나옵니다
  (`Fx|EQ`는 `audio-effect`, `equalizer`가 되고, `Instrument|Synth`는 `instrument`,
  `synthesizer`가 되며, `Fx|Instrument`는 `note-effect`가 됩니다). 명시적으로 지정하려면
  `plugin.CLAPFeaturesPlugin`을 구현합니다.
- 파라미터는 plain 값을 사용합니다. 연속 파라미터는 `Min..Max` 범위를, 스텝
  파라미터는 스텝 인덱스 `0..StepCount` 범위를 갖습니다. 값 텍스트는 포맷터 출력에
  단위를 붙인 것입니다. 미터 같은 읽기 전용(출력) 파라미터는 노출되지 않습니다. CLAP
  host는 스스로 바뀌는 값을 오류로 취급하기 때문입니다. 이 값들은 여전히 에디터와
  VST3 host에는 전달됩니다.
- 오디오 버스는 오디오 포트가 되고(프로세서가 배정밀도를 지원하면 64비트), 이벤트
  버스는 CLAP 및 MIDI dialect를 지원하는 노트 포트가 됩니다.
- CLAP 노트는 `midi.Event` 노트가 됩니다. host 노트 ID가 없는 노트에는 ID가
  생성되며, 와일드카드 노트 오프와 익스프레션(키, 채널 또는 노트 ID가 -1)은 현재
  울리고 있는 노트를 기준으로 해석됩니다. CLAP 노트 익스프레션은 VST3 타입(볼륨,
  팬, 튜닝, 비브라토, 익스프레션, 브라이트니스)에 매핑되고, 프레셔는 폴리 프레셔가
  됩니다. 원시 MIDI 1.0 컨트롤러는 VST3 host에서와 동일한 MIDI 매핑
  (`MIDIMappingProcessor`, `MIDIControllerProcessor`)을 거치며, 프로그램 체인지는
  프로그램을 선택합니다.
- `Host.EditParameter`/`BeginEdit`/`EndEdit`는 다음 process 또는 flush 호출에서 CLAP
  제스처 및 값 이벤트가 됩니다. `RestartComponent`는 파라미터 재스캔, 레이턴시 변경,
  재시작 요청에 매핑됩니다. 트랙 이름과 색상은 `ChannelInfoProcessor`로 전달되고,
  프로그램 피치 이름은 CLAP 노트 이름이 됩니다.
- 출력 constant 플래그는 `ctx.SetOutputSilenceFlags`에서 옵니다.
- 에디터는 `gui` 확장이며, 플랫폼의 창 API(`cocoa`, `win32`, `webkitgtk` 빌드의 `x11`)를
  사용하는 임베드 방식만 지원합니다. 에디터 뷰가 없는 빌드는 이를 제공하지 않습니다.
  Linux에서는 뷰의 타이머가 host의 `timer-support`에서 실행됩니다. host 오토메이션은
  메인 스레드(`request_callback`)에서 열려 있는 페이지에 전달되며, 상태 로드, 프리셋 로드,
  프로그램 변경도 마찬가지입니다.
- 프리셋: preset discovery 팩토리는 플러그인마다 provider를 하나씩 가지며, 이
  provider는 `.vstpreset` 파일 타입과, 존재하는 경우 플러그인의 사용자 프리셋 폴더(에디터의
  프리셋 브라우저와 VST3 host가 사용하는 폴더)를 선언합니다. 프리셋은 CLAP 및 VST3
  플러그인 ID, 수정 시각, feature로서의 악기/스타일/캐릭터 태그, extra info로서의 그 밖의
  메타 정보 속성과 함께 보고됩니다. `preset-load` 확장은 파일을 로드하고 host에
  `loaded` 또는 `on_error`를 보고하며, 사용자 폴더에서 로드한 프리셋은 에디터의 현재
  프리셋이 됩니다.

`scripts/validate_clap.sh <clap-validator> [out]`는 모든 검증용 플러그인을 `.clap`으로
빌드하고 clap-validator를 실행한 뒤 `internal/validation/hostcheck/clapcheck.c`를
실행합니다. clapcheck는 샘플 단위로 정확한 오토메이션, 64비트 오디오, ID 없는 노트,
와일드카드, 노트 익스프레션, 원시 MIDI, MIDI 이펙트 출력, 프로그램, 노트 이름, 레이턴시,
상태, preset discovery를 검사합니다. 두 도구 모두 임시 폴더를 `HOME`으로 두고 실행됩니다.
clapcheck가 그곳의 사용자 프리셋 폴더에 프리셋을 저장하면, clap-validator의
`preset-discovery-*` 테스트가 이를 탐색하고 로드합니다.

## Audio Units

macOS에서는 모든 플러그인 바이너리에 AUv2 컴포넌트 팩토리(`VST3GoAUFactory`,
`pkg/plugin/au_darwin.c`)도 들어 있습니다. 각 Audio Unit 인스턴스는 플러그인 자체의
CLAP 엔트리 인스턴스 하나를 호스팅하므로, CLAP host와 동일한 런타임과 스레딩으로
실행되며 프로세서에 AU 전용 코드가 필요하지 않습니다.

- 컴포넌트 코드: 타입은 버스와 카테고리를 따릅니다(`aufx` 이펙트, `aumf` 노트 입력이
  있는 이펙트, `aumu` 악기, `augn` 제너레이터, `aumi` 오디오 없는 MIDI 프로세서).
  manufacturer 코드는 vendor의 첫 네 글자(첫 글자는 대문자)이고 subtype은 `Info.ID`의
  해시입니다. 두 값을 직접 지정하려면 `plugin.AudioUnitPlugin`을 구현합니다(ASCII 네
  글자, Apple은 모두 소문자인 manufacturer 코드를 예약해 두고 있습니다). host는 이 코드로
  프리셋과 세션을 식별하므로, 한 번 릴리스한 뒤에는 바꾸지 마세요. `vst3go build`는
  빌드된 바이너리를 로드하여(`scripts/auinfo.c`) 이 코드를 번들의 `Info.plist`에
  기록합니다.
- 오디오: CLAP 오디오 포트는 포트의 채널 수를 갖는 32비트 float non-interleaved AU
  element가 됩니다(메인 버스가 먼저, 그 뒤에 사이드체인과 aux 출력). 입력은 렌더
  콜백이나 연결에서 가져오며, 연결되지 않은 사이드체인은 무음입니다. 다중 출력
  플러그인은 타임스탬프의 첫 번째 `AudioUnitRender` 호출에서 모든 버스를 렌더링합니다.
- 파라미터는 plain 값을 갖는 CLAP 파라미터입니다(스텝 파라미터는 value string을
  갖는 인덱스 값). 텍스트는 양방향 모두 포맷터에서 나옵니다.
  `AudioUnitScheduleParameters` 이벤트는 버퍼 오프셋 위치에서 프로세서에 전달되고
  (램프는 시작값과 끝값으로), `AudioUnitSetParameter` 변경은 다음 블록의 시작에
  적용됩니다(유닛이 초기화되지 않은 동안에는 즉시). 에디터의 편집과 제스처는 메인
  스레드에서 AU 파라미터 리스너(`AUEventListener`)에 전달됩니다.
- 상태: `kAudioUnitProperty_ClassInfo`는 표준 프리셋 키 아래에 플러그인 상태를
  담습니다. 팩토리 프로그램은 팩토리 프리셋이 됩니다
  (`kAudioUnitProperty_FactoryPresets`, `PresentPreset`).
- MIDI: 샘플 오프셋을 포함한 `MusicDeviceMIDIEvent`/`SysEx`가 입력되고, 플러그인의
  MIDI 및 노트 출력은 host의 MIDI 출력 콜백으로 전달됩니다.
- host 트랜스포트(`HostCallbacks`: 템포, 비트, 박자표, 재생, 녹음, 사이클)는
  `ProcessContext` 트랜스포트가 됩니다. 레이턴시와 테일은 초 단위로 보고되며, 초기화된
  상태에서 레이턴시가 바뀌면 플러그인을 다시 활성화하고 host에 알립니다. 바이패스
  (이펙트)는 메인 입력을 그대로 통과시키고, 오프라인 렌더링은 CLAP 렌더 모드를
  설정합니다.
- 에디터: `kAudioUnitProperty_CocoaUI`는 번들 안의 뷰 팩토리 클래스를 가리킵니다.
  에디터는 자신의 제한 범위 안에서 host의 크기 조절과 `vst3go.resize`를 따릅니다.
  `vst3go build`는 여러 vst3go Audio Unit이 하나의 host 프로세스를 공유할 수 있도록 그
  Objective-C 클래스 이름을 번들에 맞춰 지정합니다(`VST3GO_AU_VIEW_CLASS`). 이 과정
  없이 빌드한 Audio Unit은 기본 클래스 이름을 공유합니다.

`scripts/validate_au.sh [out]`는 모든 검증용 플러그인을 `.component`로 빌드하고,
`vst3go-check-*.component`라는 이름으로 `~/Library/Audio/Plug-Ins/Components`에 설치한
뒤(종료 시 제거), 각각에 대해 `auval -strict -r 2`를 실행하고 이어서
`internal/validation/hostcheck/aucheck_darwin.m`을 실행합니다. 검사 항목은 파라미터
텍스트 왕복 변환, ClassInfo 저장/복원과 다른 플러그인 상태의 거부, 파라미터를 따르는
이펙트 출력, 악기 노트와 팩토리 프리셋, MIDI 프로세서의 조옥된 MIDI 출력, 사이드체인
덕킹과 레이턴시, 그리고 에디터(번들에서 로드한 뷰 팩토리, AU 리스너와
`AudioUnitGetParameter`에 도달하는 페이지 편집, 페이지에 도달하는 host 변경, 뷰 크기를
조절하는 신스 페이지)입니다.

## LV2

Linux에서는 모든 플러그인 바이너리가 `lv2_descriptor`와 `lv2ui_descriptor`도 export합니다
(`pkg/plugin/lv2_linux.c`, `lv2ui_linux.c`). Audio Unit과 마찬가지로 각 LV2 인스턴스는
플러그인 자체의 CLAP 엔트리 인스턴스 하나를 호스팅합니다. `vst3go build -format lv2`는
빌드된 바이너리를 로드하여(`scripts/lv2info.c`) `manifest.ttl`, `plugins.ttl`(플러그인,
포트, UI), `presets.ttl`을 기록합니다.

- URI: `urn:vst3go:<Info.ID>`. host는 이 URI와 포트 심볼로 세션과 프리셋을
  식별하므로, 한 번 릴리스한 뒤에는 둘 다 바꾸지 마세요.
- 클래스: 카테고리에서 정해집니다(`lv2:InstrumentPlugin`, `lv2:MIDIPlugin`,
  `lv2:EQPlugin`, `lv2:CompressorPlugin`, `lv2:ReverbPlugin`, ...). vendor는 팩토리 URL 및
  이메일(`plugin.SetFactoryInfo`)과 함께 플러그인 `lv2:project`의 maintainer가 됩니다.
- 오디오: 모든 CLAP 오디오 포트의 채널마다 포트가 하나씩 있습니다(`in_<port>_<ch>`,
  `out_<port>_<ch>`). 사이드체인과 aux 출력은 `lv2:connectionOptional`(사이드체인은
  `lv2:isSideChain`)이며 연결되지 않으면 무음입니다. 입력은 복사되므로 host가 in-place
  처리를 해도 되며, `bufsz:maxBlockLength`(또는 4096)보다 긴 블록은 분할됩니다.
- 파라미터: 보이는 파라미터마다 컨트롤 포트가 하나씩 있습니다(`param_<id>`, plain 값
  범위와 기본값, 스텝 파라미터는 `lv2:integer`, 바이패스와 on/off는 `lv2:toggled`,
  리스트는 scale point가 있는 `lv2:enumeration`, 값 텍스트에서 가져온 단위, host가
  오토메이션하면 안 되는 파라미터는 `pprop:notAutomatic`). 읽기 전용 파라미터는 컨트롤
  출력입니다. 포트 변경은 블록 시작 시점에 프로세서에 전달됩니다. 숨겨진 파라미터
  (MIDI 컨트롤러 매핑)는 제외되며, 컨트롤러는 MIDI로 도착합니다.
- 프로그램: 프로그램 목록은 포트가 아닙니다. 각 팩토리 프로그램은 그것이 설정하는
  파라미터 값을 갖는 LV2 프리셋입니다.
- 상태: `state:interface`가 플러그인 상태(`urn:vst3go:state`, `atom:Chunk`)를
  저장하며, host는 이와 함께 컨트롤 포트를 복원합니다.
- 이벤트: `control` atom 입력은 `time:Position`(템포, 마디, 비트, 박자표, 재생 상태,
  위치 이벤트 사이에서 진행됨)과, 노트 입력이 있는 경우 프레임 오프셋을 포함한 MIDI 및
  SysEx를 전달합니다. 노트 출력이 있는 플러그인은 `midi_out` atom 출력을 갖습니다.
  `latency` 포트는 레이턴시를 보고하며(`lv2:reportsLatency`), 레이턴시가 바뀌면 `run`
  안에서 플러그인을 다시 활성화합니다.
- UI: `webkitgtk` 빌드는 host 창에서 에디터를 여는 X11 UI(`ui:X11UI`)를 추가합니다.
  이 UI에는 `instance-access`(에디터가 프로세서와 직접 통신)와 `ui:parent`가 필요하며,
  host가 제공하면 `ui:resize`와 `ui:touch`를 사용합니다. 에디터의 편집은 touch 제스처가
  더해진 포트 쓰기로 host에 전달되고, 플러그인이 스스로 바꾼 값(에디터나 MIDI로
  선택한 프로그램)은 idle 콜백에서 포트 쓰기로 전달됩니다. idle 콜백은 에디터의
  타이머도 실행합니다. 인스턴스당 UI는 하나입니다.

`scripts/validate_lv2.sh [out]`(Linux, lv2-dev와 liblilv-dev 필요)는 모든 검증용 플러그인을
LV2 번들로 빌드하고, 그 Turtle에 대해 lv2_validate를, 설치되어 있으면 lv2lint를, 그리고
`internal/validation/hostcheck/lv2check.c`를 실행합니다. lv2check에서는 lilv가 번들을
로드하고 포트와 그 속성, in-place 및 분할된 블록에 걸쳐 적용되는 게인, Turtle로
저장해 새 인스턴스에 복원한 상태, 신스의 프리셋(Pluck은 릴리스를 80으로 설정)과
정해진 프레임의 노트, MIDI 이펙트의 조옥된 MIDI 및 SysEx 출력, 덕커의 레이턴시 포트와
연결되지 않은 사이드체인을 검사합니다. `scripts/docker_validate_linux.sh`는 lv2lint를 빌드해
그 안에서 실행하고, `scripts/docker_check_linux_editor.sh`는 Xvfb에서 LV2 UI를 엽니다
(`lv2check --ui`). 이때 페이지의 키보드 입력이 touch 제스처가 더해진 포트 쓰기로 host에
도달하는지, 신스 페이지가 720x420을 요청하는지, MIDI 프로그램 체인지가 host 포트에
도달하는지, UI가 크기 조절되고 닫혔다가 다시 열리는지 확인합니다.

## 스탠드얼론 애플리케이션

`pkg/standalone`은 플러그인을 오디오 및 MIDI 장치, 창에 띄운 에디터, 실행 간에
유지되는 상태를 갖춘 애플리케이션으로 실행합니다. 플러그인 패키지의 `main`은
`standalone.Main()`을 호출합니다. 플러그인 빌드(c-shared)는 이를 실행하지 않으며,
`standalone` 태그 없이 빌드한 실행 파일은 스탠드얼론을 빌드하는 방법만 출력합니다.
`vst3go build -format app`은 그 태그(여기에 `GOFLAGS`의 태그, 예: Linux 에디터용
`-tags=webkitgtk`)로 빌드합니다. 결과물은 macOS `.app` 번들(마이크 사용 설명 포함)이나
Linux 실행 파일이며, Windows 크로스 빌드는 `<name>.exe`를 기록합니다.

애플리케이션은 플러그인 자체의 CLAP 엔트리를 통해 플러그인을 호스팅하므로, CLAP host와
동일한 런타임과 스레딩으로 실행됩니다.

- 오디오: miniaudio(`github.com/gen2brain/malgo`)로 Core Audio, WASAPI, DirectSound, ALSA,
  PulseAudio, JACK 또는 `null` 장치(테스트와 서버용 타이머)를 사용합니다. 하나의
  duplex 장치를 사용하며, 메인 입력 버스는 입력 장치를 읽고(모노 장치는 모든 채널에
  공급되며, `-input none`이면 입력 장치를 열지 않음), 메인 출력 버스는 출력 장치로
  재생됩니다(모노 플러그인은 모든 채널로 재생). 나머지 버스에는 무음이 들어갑니다.
  오디오 출력이 없는 플러그인(MIDI 이펙트)은 무음 스테레오 출력 장치를 클록으로
  삼아 실행됩니다.
- MIDI: Core MIDI, ALSA sequencer, WinMM(`internal/midiport`). 모든 입력(또는 지정한
  입력)과, macOS 및 Linux에서는 플러그인 이름을 딴 가상 입력에서 오는 채널 메시지가
  플러그인의 첫 번째 노트 포트로 전달됩니다. 플러그인의 MIDI 출력은 포트로 보낼 수
  있습니다(`-midi-out`). SysEx와 리얼타임 메시지는 버려집니다.
- 에디터: 플러그인의 `gui` 확장을 창에 띄웁니다(Cocoa, Win32, `webkitgtk` 빌드의
  X11). 에디터의 제한 범위 안에서 크기를 조절할 수 있고 `vst3go.resize`를 따릅니다.
  에디터 뷰가 없는 빌드, `-headless`, X 서버가 없는 Linux에서는 창 없이 실행됩니다.
- 설정과 상태: 장치 선택은 `standalone.json`에, 플러그인 상태는 `standalone.state`에
  저장되며, 위치는 `<user config dir>/<vendor>/<name>`입니다(`-config`로 다른
  디렉터리를 지정). 명령줄에서 준 플래그는 저장된 설정을 덮어쓰고 갱신합니다. 상태는
  시작할 때 로드되고(`-reset`이면 건너뜀) 종료할 때 저장됩니다(창 닫기, Cmd+Q,
  SIGINT, SIGTERM, `-duration` 경과).
- 재시작: 활성 상태에서 레이턴시나 포트를 바꾼 플러그인은 장치를 멈춘 상태에서
  비활성화되었다가 다시 활성화됩니다.

```text
-list                 list audio devices and MIDI ports
-plugin id|name       the plugin to run when the binary registers several
-backend name         coreaudio, wasapi, dsound, winmm, alsa, pulseaudio, jack, oss, null
-output / -input name audio devices (exact name or a unique part; -input none)
-sample-rate hz       0: the device rate
-buffer frames        device period (default 256)
-midi-in list         all (default), none, or comma-separated port names
-midi-out name        MIDI output port
-virtual-midi=false   no virtual MIDI input
-headless             no editor window
-config dir, -reset, -duration d
```

`scripts/check_standalone.sh`는 `pkg/standalone` 테스트(host glue를 거친 악기와 이펙트,
null 장치, 설정 및 상태 파일), null 장치에서 애플리케이션으로 실행하는 모든 검증용
플러그인, `internal/validation/hostcheck/standalonecheck`(가상 MIDI 포트를 통해 transpose
플러그인을 거치는 MIDI 왕복: 입력, 플러그인, 출력), 그리고 macOS에서는 페이지의 크기
조절을 따르는 신스 에디터 창을 실행합니다. `scripts/docker_check_linux_standalone.sh`는
Linux를 다룹니다: headless 앱, Xvfb에서의 신스 에디터(페이지가 창을 720x420으로 크기
조절), `SOUND=1`일 때 ALSA를 통한 재생.

## 알려진 제한 사항

- Windows 빌드는 컴파일과 export만 검사하며(`scripts/docker_build_windows.sh`), CI에서
  validator를 실행하지는 않습니다. 실제 머신에서 `docs/windows-validation.md`
  ([한국어](windows-validation.ko.md))를 따르세요.
- `moduleinfo.json`은 네이티브 빌드에서만 생성되며, Windows 크로스 빌드에서는 생성되지
  않습니다.
- 분산 모드에서 host가 `IProgramListData`로 기록한 프로그램 데이터는 프로세서 쪽만
  갱신합니다.
- 코드와 스케일 이벤트는 입력 전용이며, Linux 에디터에는 X11 host가 필요합니다(VST3에는
  Wayland 임베딩이 없습니다).
- AAX 포맷은 없습니다. AAX 플러그인은 Avid의 AAX SDK로 빌드해야 하고 정식 Pro Tools에서
  로드하려면 PACE 서명이 필요한데, vst3go는 둘 다 감싸지 않습니다.
- Audio Unit은 AUv2만 지원하며(AUv3 앱 확장 없음), 채널 구성은 CLAP 포트의 구성을
  따르고(host와 협상하는 레이아웃 없음), MIDI 2.0 이벤트 리스트는 없습니다.
- LV2는 Linux 전용입니다. UI가 열려 있지 않으면 플러그인이 스스로 바꾼 값(MIDI로
  선택한 프로그램)은 host의 컨트롤 포트에 도달하지 않습니다. 플러그인은
  `lv2:hardRTCapable`을 주장하지 않으며(Go 처리에는 실시간 보장이 없음)
  `doap:license`도 없습니다.
- 스탠드얼론 애플리케이션에는 앱 내 오디오 설정 대화상자가 없고(장치는 플래그로
  선택하고 기억됨), 트랜스포트가 없으며(플러그인은 템포나 위치를 받지 못함), Windows
  빌드는 컴파일만 검사합니다. Linux MIDI와 Windows MIDI/오디오 경로는 아직 실제
  하드웨어에서 실행해 보지 않았습니다(Core MIDI, Core Audio, ALSA 재생은 확인함).
- CLAP 에디터는 플러그인 소유 창에 플로팅할 수 없고, CLAP host에는 MIDI learn 모드,
  키스위치, 노트 익스프레션 메타데이터 쿼리가 없습니다. 분산 플러그인은 하나의 CLAP
  객체로 실행되므로 그 경우 `Host.SendMessage`는 오류를 반환합니다.

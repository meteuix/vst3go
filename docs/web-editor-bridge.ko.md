[English](web-editor-bridge.md) | 한국어

# 웹 에디터 브리지

플러그인 에디터는 플랫폼 웹 뷰가 렌더링하여 host의 에디터 창에 임베드하는 웹
페이지입니다. 데이터 모델은 Go가 소유하며, 페이지는 플러그인과 JSON 메시지를
주고받는 vst3go JavaScript SDK(`window.vst3go`)를 통해 이를 렌더링합니다.

## 렌더링 흐름

1. `pkg/plugin/component.go`가 실시간 파라미터 레지스트리로부터 `EditorModel`을 만듭니다.
2. `editorPage`(`pkg/plugin/editor.go`)는 프로세서의 페이지(`Editor.HTML`) 또는 내장 페이지를 가져와 `<head>`의 시작 부분에 부트스트랩 데이터(`window.__vst3goBootstrap`: 모델과 에디터 크기 제한)와 SDK(`editor_assets/vst3go.js`)를 주입합니다. `GoEditControllerGetEditorHTML`이 그 페이지를 반환합니다.
3. 플랫폼 뷰가 HTML을 로드합니다. macOS에서는 `editor_view_darwin.c`가 `WKWebView`로, Windows에서는 `editor_view_windows.c`가 WebView2로, Linux에서는 `editor_view_linux.c`가 WebKitGTK로 로드합니다(빌드 태그 `webkitgtk`, host의 X11 창에 임베드됨).
4. 사용자 입력은 JSON 메시지로 되돌아오며, 플러그인 쪽 변경 사항도 JSON 메시지로 페이지에 전달됩니다.

뷰는 VST3 `IPlugView` 객체입니다. CLAP 인스턴스도 이 뷰를 사용합니다. `pkg/plugin/clap_gui.c`가 CLAP `gui` 확장에서 뷰를 구동하고, `editor_session.go`가 두 포맷 모두를 위해 인스턴스별 페이지 상태(뷰, 메시지, 프리셋 브라우저)를 보관합니다.

## 커스텀 에디터

프로세서는 `EditorProcessor`를 구현하여 내장 에디터를 대체합니다:

```go
//go:embed ui/dist/index.html
var editorHTML string

func (p *Processor) Editor() plugin.Editor {
	return plugin.Editor{
		HTML:  editorHTML, // one document, scripts and styles inline
		Width: 640, Height: 400,
		MinWidth: 480, MinHeight: 320, MaxWidth: 1200, MaxHeight: 900,
	}
}
```

- `HTML`은 하나의 독립된 문서입니다(예: `vite-plugin-singlefile`을 사용한 Vite 빌드).
  SDK는 페이지의 스크립트보다 먼저 실행됩니다. `HTML`이 비어 있으면 내장 에디터가
  유지되므로, `Editor`로 크기만 지정할 수도 있습니다.
- `Width`/`Height`는 초기 크기입니다(기본값 900x640). min/max 필드는 host의 크기 조절
  (`checkSizeConstraint`, CLAP `adjust_size`)과 `vst3go.resize`의 범위를 제한하며, 0은
  제한 없음을 뜻합니다. `FixedSize`는 크기 조절을 끕니다.
- `EditorMessageProcessor.EditorMessage(name, data)`는 메인 스레드에서
  `vst3go.send(name, data)`를 받습니다. 응답은 `Host.PostEditorMessage(name, data)`
  (`HostAwareProcessor.SetHost`에서 받은 Host)로 돌려보내며, 페이지는 이를
  `vst3go.on(name, ...)`으로 받습니다. `DistributedPlugin`에서는 에디터가 컨트롤러 쪽에
  속하며, 프로세서 쪽의 `PostEditorMessage`는 내부 `vst3go.editor` 메시지로 컨트롤러에
  전달됩니다. `plugin.Offline`은 테스트를 위해 게시된 메시지를 기록하고
  (`TakeEditorMessages`) 페이지 메시지를 전달합니다(`EditorMessage`).

`internal/validation/synth`가 참조 구현입니다. `editor.html`(range 입력, 키보드로 조작하는
노브, 선택 버튼, 프로그램 선택, `vst3go.send("status")`로 폴링하는 상태 표시줄, 시작 시
`vst3go.resize`)을 참고하세요.

## JavaScript SDK

`window.vst3go`(선언: `editor_assets/vst3go.d.ts`):

| 멤버 | 의미 |
| --- | --- |
| `version` | `1` |
| `plugin` | id, name, version, vendor, category |
| `params`, `param(idOrName)`, `sections` | 레지스트리 순서의 실시간 파라미터, ID 또는 이름으로 조회, 모델과 같은 방식으로 그룹화 |
| `onParamChange(fn)` | host, 플러그인, 페이지 어디서든 발생한 모든 파라미터 변경. 구독 해제 함수를 반환 |
| `presets` | `list`, `current`, `error`, `refresh()`, `save(name)`, `load(name)`, `onChange(fn)` (사용자 `.vstpreset` 폴더) |
| `send(name, data)` / `on(name, fn)` | 프로세서와의 커스텀 메시지 |
| `size`, `resize(w, h)` | 초기 크기와 제한. host에 새 크기를 요청(제한 범위로 고정됨) |

파라미터(`Vst3goParam`)는 `id`, `name`, `unit`, `kind`, plain `min`/`max`,
`defaultValue`(plain)와 `defaultNormalized`, `stepCount`, `readOnly`, `hidden`, 그리고
현재의 `normalized`, `plain`, `text`(플러그인의 포맷터 결과)를 담고 있습니다. 편집:

- `beginEdit()` / `endEdit()`는 드래그를 감싸서 host가 하나의 오토메이션 패스와 하나의
  실행 취소 단계로 기록하게 합니다. 중첩할 수 있습니다.
- `setNormalized(v)` / `setPlain(v)`는 값을 편집합니다. 제스처 밖에서 호출하면 그 자체로
  완전한 편집(begin, change, end)이 됩니다. `reset()`은 기본값으로 설정합니다.
- `bindGesture(element)`는 요소 위의 포인터 드래그를 제스처로 바꿉니다.
- `onChange(fn)`은 하나의 파라미터를 추적합니다.

### 타입이 지정된 바인딩

`plugin.EditorBindings(p)`는 camelCase로 변환한 파라미터 이름을 키로 하는 `params` 테이블
(ID, 범위, 종류, plain 기본값, 스텝 수, hidden 파라미터는 제외), `ParamKey` 타입, 그리고
실시간 `Vst3goParam`을 반환하는 `param(key)`를 담은 TypeScript 모듈을 생성하므로, 파라미터
이름을 잘못 쓰면 컴파일되지 않습니다. `plugin.WriteEditorBindings(p, dir)`는 이를
`vst3go.d.ts` 옆에 `params.ts`로 기록합니다. 플러그인 테스트에서
`plugintest.EditorBindings(t, p, dir)`는 파일이 오래되었으면 실패하며,
`VST3GO_UPDATE_BINDINGS=1 go test`로 다시 생성합니다. `internal/validation/synth/ui`에는
신스의 바인딩과, 일반적인 에디터 코드를 바인딩에 대해 타입 검사하는 `check.ts`가 있습니다.

## 메시지

SDK는 이 프로토콜을 감싸고 있으며, SDK를 쓰지 않는 페이지는 프로토콜을 직접 사용할 수 있습니다.

페이지에서 플러그인으로: `window.webkit.messageHandlers.vst3go`(macOS, Linux) 또는
`window.chrome.webview`(Windows)로 게시하는 JSON 객체입니다. 모든 플랫폼 뷰는 JSON을
`GoEditorMessage`에 넘깁니다.

| 메시지 | 효과 |
| --- | --- |
| `{ type: "begin-edit", id }` / `{ type: "end-edit", id }` | 드래그를 감쌈: host가 하나의 오토메이션 패스와 하나의 실행 취소 단계로 기록 |
| `{ type: "param-change", id, value }` | 정규화된 `value`를 설정. 제스처 밖에서는 완전한 편집이 됨 |
| `{ type: "preset-list" }` | 프리셋 목록을 요청 |
| `{ type: "preset-save", name }` | 현재 상태를 사용자 프리셋 폴더에 `name`으로 저장(`"Bass/Deep"`은 하위 폴더를 사용) |
| `{ type: "preset-load", name }` | 사용자 프리셋 폴더에서 프리셋을 로드 |
| `{ type: "resize", width, height }` | host에 새 크기를 요청(VST3 `IPlugFrame::resizeView`, CLAP `request_resize`), 제한 범위로 고정됨 |
| `{ type: "message", name, data }` | `EditorMessageProcessor.EditorMessage(name, data)` |

플러그인에서 페이지로: `VST3GoEditorViewPost`가 `window.__vst3goReceive(message)`를
호출합니다(Windows에서는 `PostWebMessageAsJson`과 `message` 이벤트를 통해).

| 메시지 | 의미 |
| --- | --- |
| `{ type: "param-change", id, normalized, plain, text }` | 파라미터가 바뀜(host 오토메이션, 프리셋, 플러그인 편집) |
| `{ type: "presets", presets: [names], current, error }` | 프리셋 메시지 이후의 사용자 프리셋. `error`는 저장/로드 실패 사유를 설명 |
| `{ type: "message", name, data }` | `Host.PostEditorMessage(name, data)` |

## 내장 에디터

`pkg/plugin/editor_assets/`에는 플러그인 바이너리에 임베드되는 내장 페이지(`index.html`,
`editor.css`, `editor.js`)가 있습니다. `editor.js`는 SDK 위에 작성되었으며, 보이는
파라미터마다 종류에 맞는 컨트롤 하나, 프리셋 바, 슬라이더의 제스처를 제공합니다.

## 상태

- **플러그인 상태**는 파라미터 레지스트리와 `StatefulProcessor` 데이터이며, host가 `getState`/`setState`를 통해, 그리고 `.vstpreset` 파일(`pkg/preset`)로 저장합니다.
- **에디터 스냅샷**은 페이지가 시작할 때 사용하는 레지스트리의 부트스트랩 사본이며, `EditorSnapshot.Apply`는 스냅샷 값을 레지스트리에 복원합니다.

## 검사

- `scripts/check_editor_sdk.sh`는 node에서 가짜 페이지로 SDK를 실행하고
  (`hostcheck/sdkcheck.mjs`), 신스의 바인딩이 최신인지 확인하며, 선언, 바인딩,
  `check.ts`를 `tsc`로 타입 검사합니다.
- `scripts/check_macos_editor.sh`와 `scripts/docker_check_linux_editor.sh`는 게인(내장) 및
  신스(커스텀) 에디터를 VST3와 CLAP으로 실제 창에서 엽니다. macOS에서는
  `editorcheck_darwin.m`이 페이지 안의 SDK를 구동합니다. 플러그인에 도달해야 하는 편집
  제스처, SDK의 값, 텍스트, 리스너에 도달해야 하는 host 변경, 신스의 시작 시 크기 조절,
  제한 범위로의 고정, 프로세서와의 `send`/`on` 왕복을 검사합니다. Linux에서는
  `editorcheck.c`가 XTest 키보드 입력으로 편집하고 시작 시 크기 조절을 확인합니다.

## 왜 이런 구조인가

- 플러그인 데이터 모델은 Go가 소유합니다.
- 플랫폼 코드는 브라우저 렌더러를 호스팅하고 JSON을 전달하기만 합니다.
- 페이지는 모든 플랫폼과 포맷에서 하나의 SDK를 사용하므로, 같은 UI가 어디서나 동작합니다.

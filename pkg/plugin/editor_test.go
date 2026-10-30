package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/meteuix/vst3go/v2/pkg/framework/bus"
	"github.com/meteuix/vst3go/v2/pkg/framework/param"
)

// customEditorProcessor has its own editor page and answers editor messages.
type customEditorProcessor struct {
	editorModelTestProcessor
	editor   Editor
	host     Host
	received []string
}

func (p *customEditorProcessor) Editor() Editor    { return p.editor }
func (p *customEditorProcessor) SetHost(host Host) { p.host = host }
func (p *customEditorProcessor) EditorMessage(name string, data json.RawMessage) {
	p.received = append(p.received, name+" "+string(data))
	if name == "ping" {
		_ = p.host.PostEditorMessage("pong", map[string]any{"echo": json.RawMessage(data)})
	}
}

func bootstrapOf(t *testing.T, page string) editorBootstrap {
	t.Helper()
	const prefix = "<script>window.__vst3goBootstrap = "
	start := strings.Index(page, prefix)
	if start < 0 {
		t.Fatal("page has no bootstrap")
	}
	rest := page[start+len(prefix):]
	end := strings.Index(rest, ";</script>")
	var boot editorBootstrap
	if err := json.Unmarshal([]byte(strings.ReplaceAll(rest[:end], `<\/`, "</")), &boot); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return boot
}

func TestBuiltinEditorPage(t *testing.T) {
	c := gestureComponent(t)
	page, err := c.editorPage()
	if err != nil {
		t.Fatal(err)
	}
	for _, placeholder := range []string{"__VST3GO_CSS__", "__VST3GO_JS__"} {
		if strings.Contains(page, placeholder) {
			t.Errorf("placeholder %s left in the page", placeholder)
		}
	}
	sdk := strings.Index(page, "window.vst3go = Object.freeze(sdk)")
	editor := strings.Index(page, "vst3go.sections.forEach")
	if sdk < 0 || editor < 0 || sdk > editor {
		t.Fatalf("the SDK must come before the built-in editor script (sdk %d, editor %d)", sdk, editor)
	}
	if !strings.Contains(page, ".shell") {
		t.Error("built-in CSS missing")
	}
	boot := bootstrapOf(t, page)
	if len(boot.Model.Sections) != 1 || len(boot.Model.Sections[0].Controls) != 1 || boot.Model.Sections[0].Controls[0].Text == "" {
		t.Errorf("bootstrap model %+v", boot.Model)
	}
	if boot.Editor != (editorSizeLimits{Width: defaultEditorWidth, Height: defaultEditorHeight, Resizable: true}) {
		t.Errorf("default editor size %+v", boot.Editor)
	}
}

func TestCustomEditorPage(t *testing.T) {
	registry := param.NewRegistry()
	if err := registry.Add(param.New(1, "Drive").Range(0, 10).Build()); err != nil {
		t.Fatal(err)
	}
	p := &customEditorProcessor{
		editorModelTestProcessor: editorModelTestProcessor{params: registry, buses: bus.NewEffectStereo()},
		editor: Editor{
			HTML:  `<!DOCTYPE html><HTML><Head lang="en"><title>x</title><script>start()</script></Head><body></body></HTML>`,
			Width: 300, Height: 200, MinWidth: 250, MaxWidth: 400, MinHeight: 150,
		},
	}
	c := newTestComponent(t, p)
	// A value whose text would close the script element.
	registry.Get(1).SetFormatter(func(float64) string { return "</script><b>" }, nil)
	page, err := c.editorPage()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(page, `<!DOCTYPE html><HTML><Head lang="en">`+"\n<script>window.__vst3goBootstrap") {
		t.Errorf("SDK not at the start of <head>:\n%.120s", page)
	}
	if strings.Index(page, "window.vst3go = ") > strings.Index(page, "start()") {
		t.Error("the SDK must run before the page's scripts")
	}
	if strings.Count(page, "</script>") != 3 {
		t.Errorf("bootstrap data must not close its script element: %d", strings.Count(page, "</script>"))
	}
	boot := bootstrapOf(t, page)
	if boot.Model.Sections[0].Controls[0].Text != "</script><b>" {
		t.Errorf("text %q", boot.Model.Sections[0].Controls[0].Text)
	}
	want := editorSizeLimits{Width: 300, Height: 200, MinWidth: 250, MinHeight: 150, MaxWidth: 400, Resizable: true}
	if boot.Editor != want {
		t.Errorf("editor size %+v, want %+v", boot.Editor, want)
	}
	if got := injectEditorSDK("<p>no head</p>", []byte("{}")); !strings.HasPrefix(got, "<script>window.__vst3goBootstrap = {};") || !strings.HasSuffix(got, "<p>no head</p>") {
		t.Errorf("page without <head>: %.80s", got)
	}
	if strings.Contains(injectEditorSDK("<header>x</header>", []byte("{}")), "<header>\n<script>") {
		t.Error("<header> is not <head>")
	}
}

func TestEditorSpec(t *testing.T) {
	cases := []struct {
		editor Editor
		want   Editor
	}{
		{Editor{}, Editor{Width: 900, Height: 640}},
		{Editor{Width: 100, Height: 50, MinWidth: 200, MaxHeight: 40}, Editor{Width: 200, Height: 40, MinWidth: 200, MaxHeight: 40}},
		{Editor{Width: 500, Height: 400, FixedSize: true}, Editor{Width: 500, Height: 400, MinWidth: 500, MinHeight: 400, MaxWidth: 500, MaxHeight: 400, FixedSize: true}},
	}
	for _, tc := range cases {
		got := editorSpec(&customEditorProcessor{editor: tc.editor})
		if got != tc.want {
			t.Errorf("editorSpec(%+v) = %+v, want %+v", tc.editor, got, tc.want)
		}
	}
	e := Editor{MinWidth: 200, MaxWidth: 800, MinHeight: 100}
	for _, tc := range []struct{ w, h, ww, wh int }{{100, 50, 200, 100}, {1000, 5000, 800, 5000}, {300, 300, 300, 300}, {-5, 0, 200, 100}} {
		if w, h := e.constrain(tc.w, tc.h); w != tc.ww || h != tc.wh {
			t.Errorf("constrain(%d, %d) = %d, %d; want %d, %d", tc.w, tc.h, w, h, tc.ww, tc.wh)
		}
	}
	if w, h := (Editor{}).constrain(0, -1); w != 1 || h != 1 {
		t.Errorf("sizes stay positive: %d, %d", w, h)
	}
}

func TestEditorCustomMessages(t *testing.T) {
	p := &customEditorProcessor{editorModelTestProcessor: editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewEffectStereo()}}
	c := newTestComponent(t, p)
	w := &componentWrapper{component: c}
	p.SetHost(hostHandle{w})
	w.handleEditorMessage(c, hostHandle{w}, []byte(`{"type":"message","name":"ping","data":{"n":1}}`))
	w.handleEditorMessage(c, hostHandle{w}, []byte(`{"type":"message","name":"","data":1}`))
	w.handleEditorMessage(c, hostHandle{w}, []byte(`{"type":"resize","width":10,"height":10}`)) // no view: nothing to resize
	if len(p.received) != 1 || p.received[0] != `ping {"n":1}` {
		t.Fatalf("received %q", p.received)
	}
	if err := (hostHandle{w}).PostEditorMessage("", 1); err == nil {
		t.Error("PostEditorMessage needs a name")
	}
	msg, err := customEditorMessage("level", []float64{0.5, 0.25})
	if err != nil || string(msg) != `{"type":"message","name":"level","data":[0.5,0.25]}` {
		t.Errorf("message %s, %v", msg, err)
	}
	if _, err := customEditorMessage("bad", func() {}); err == nil {
		t.Error("data must encode as JSON")
	}
}

func TestOfflineEditorMessages(t *testing.T) {
	o, err := NewOffline(editorTestPlugin{}, OfflineSetup{})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if err := o.EditorMessage("ping", map[string]int{"n": 2}); err != nil {
		t.Fatal(err)
	}
	out := o.TakeEditorMessages()
	if len(out) != 1 || out[0].Name != "pong" || string(out[0].Data) != `{"echo":{"n":2}}` {
		t.Fatalf("editor messages %+v", out)
	}
	if len(o.TakeEditorMessages()) != 0 {
		t.Error("TakeEditorMessages must clear")
	}
	plain, err := NewOffline(testPlugin{id: "plain"}, OfflineSetup{})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if err := plain.EditorMessage("ping", nil); err == nil {
		t.Error("a processor without EditorMessage cannot receive editor messages")
	}
}

type editorTestPlugin struct{ testPlugin }

func (editorTestPlugin) CreateProcessor() Processor {
	return &customEditorProcessor{editorModelTestProcessor: editorModelTestProcessor{params: param.NewRegistry(), buses: bus.NewEffectStereo()}}
}

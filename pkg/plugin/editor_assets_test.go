package plugin

import (
	"strings"
	"testing"
)

func TestBuildEditorHTMLInjectsAssetsAndSnapshot(t *testing.T) {
	html := buildEditorHTML("dGVzdC1zbmFwc2hvdA==")

	if strings.Contains(html, "__VST3GO_CSS__") {
		t.Fatal("CSS placeholder was not replaced")
	}
	if strings.Contains(html, "__VST3GO_JS__") {
		t.Fatal("JS placeholder was not replaced")
	}
	if strings.Contains(html, "__VST3GO_SNAPSHOT__") {
		t.Fatal("snapshot placeholder was not replaced")
	}
	if !strings.Contains(html, "dGVzdC1zbmFwc2hvdA==") {
		t.Fatal("encoded snapshot was not embedded")
	}
	if !strings.Contains(html, ".shell") {
		t.Fatal("embedded CSS was not included")
	}
	if !strings.Contains(html, "window.__vst3goReceive") {
		t.Fatal("embedded JS was not included")
	}
	if !strings.Contains(html, "control.hidden") {
		t.Fatal("hidden control handling was not included")
	}
	if !strings.Contains(html, "control.readOnly") {
		t.Fatal("read-only control handling was not included")
	}
}

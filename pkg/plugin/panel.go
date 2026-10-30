package plugin

// #cgo CFLAGS: -I../../include
// #include <stdlib.h>
// #include "panel.h"
import "C"

import (
	"encoding/json"
	"errors"
	"unsafe"
)

// Panel is a web page in a native view of its own that is not a plugin
// editor: a host-side interface such as the standalone application's
// toolbar and settings window. It uses the platform editor views (WebKit,
// WebView2, WebKitGTK), so the page posts to the same message handler
// (window.webkit.messageHandlers.vst3go or window.chrome.webview) and
// receives messages through window.__vst3goReceive. Every method runs on
// the main (UI) thread.
type Panel struct {
	id        uintptr
	html      string
	width     int
	height    int
	onMessage func(data []byte)
	onClosed  func()
	handle    *C.VST3GoPanel
	editorSession
}

var panels instances[Panel]

// ErrNoPanels reports a platform or build without editor views.
var ErrNoPanels = errors.New("no web views on this platform")

// NewPanel creates a panel showing html at width x height logical pixels.
// onMessage gets each message the page posts (JSON); onClosed, when the
// user closes the panel's floating window (OpenWindow), which hides it.
func NewPanel(html string, width, height int, onMessage func(data []byte), onClosed func()) (*Panel, error) {
	p := &Panel{id: nextInstanceID(), html: html, width: width, height: height, onMessage: onMessage, onClosed: onClosed}
	panels.add(p.id, p)
	p.handle = C.vst3go_panel_new(C.uintptr_t(p.id))
	if p.handle == nil {
		panels.remove(p.id)
		return nil, ErrNoPanels
	}
	p.setEditorView(C.vst3go_panel_view(p.handle))
	return p, nil
}

// Attach shows the panel in a native parent view: an NSView* on macOS, an
// HWND on Windows, an X11 window ID on Linux.
func (p *Panel) Attach(parent unsafe.Pointer) error {
	if !C.vst3go_panel_attach(p.handle, parent) {
		return errors.New("panel: attach failed")
	}
	return nil
}

// OpenWindow shows the panel in a floating window of its own.
func (p *Panel) OpenWindow(title string, resizable bool) error {
	ctitle := C.CString(title)
	defer C.free(unsafe.Pointer(ctitle))
	if !C.vst3go_panel_open_window(p.handle, ctitle, C.int32_t(p.width), C.int32_t(p.height), C.bool(resizable)) {
		return errors.New("panel: cannot open a window")
	}
	return nil
}

// ShowWindow shows or hides the floating window of OpenWindow.
func (p *Panel) ShowWindow(visible bool) { C.vst3go_panel_show_window(p.handle, C.bool(visible)) }

// SetSize resizes the panel (and its floating window).
func (p *Panel) SetSize(width, height int) {
	p.width, p.height = width, height
	C.vst3go_panel_set_size(p.handle, C.int32_t(width), C.int32_t(height))
}

// Post sends a message to the page's window.__vst3goReceive as JSON.
func (p *Panel) Post(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	p.postRaw(data)
	return nil
}

// Idle runs the view's periodic work (the GTK pump of X11 views); call it
// from the main thread's run loop, every 10 to 20 ms.
func (p *Panel) Idle() { C.vst3go_panel_idle(p.handle) }

// Close destroys the panel and its window.
func (p *Panel) Close() {
	if p.handle == nil {
		return
	}
	C.vst3go_panel_free(p.handle)
	p.handle = nil
	panels.remove(p.id)
}

// panelSDK is window.vst3goPanel of panel pages: post(message) sends a
// message to the panel's onMessage, onMessage(listener) subscribes to Post.
const panelSDK = `<script>
(function () {
  "use strict";
  const listeners = new Set();
  window.__vst3goReceive = (message) => listeners.forEach((listener) => {
    try { listener(message); } catch (error) { console.error(error); }
  });
  window.vst3goPanel = {
    post(message) {
      const webkit = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.vst3go;
      if (webkit) webkit.postMessage(message);
      else if (window.chrome && window.chrome.webview) window.chrome.webview.postMessage(message);
    },
    onMessage(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
})();
</script>
`

// panelPage puts the panel SDK at the start of the page's <head>.
func panelPage(html string) string {
	if loc := headTag.FindStringIndex(html); loc != nil {
		return html[:loc[1]] + "\n" + panelSDK + html[loc[1]:]
	}
	return panelSDK + html
}

//export GoPanelClosed
func GoPanelClosed(id C.uintptr_t) {
	if p := panels.get(uintptr(id)); p != nil && p.onClosed != nil {
		p.onClosed()
	}
}

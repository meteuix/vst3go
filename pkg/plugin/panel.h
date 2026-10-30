// Web panels: pages in the platform editor views (editor_view.h) that are
// not plugin editors, for host-side interfaces such as the standalone
// application's toolbar and settings window. A panel attaches to a native
// parent or opens in a floating window of its own.
#ifndef VST3GO_PANEL_H
#define VST3GO_PANEL_H

#include <stdbool.h>
#include <stdint.h>

typedef struct VST3GoPanel VST3GoPanel;

// vst3go_panel_new creates the view of panel id; NULL without an editor
// view on this platform.
VST3GoPanel* vst3go_panel_new(uintptr_t id);
// vst3go_panel_attach shows the panel in a native parent: an NSView*, HWND
// or X11 window ID.
bool vst3go_panel_attach(VST3GoPanel* p, void* parent);
// vst3go_panel_open_window shows the panel in a floating window; the user
// closing the window hides it and calls GoPanelClosed.
bool vst3go_panel_open_window(VST3GoPanel* p, const char* title, int32_t width, int32_t height, bool resizable);
void vst3go_panel_show_window(VST3GoPanel* p, bool visible);
void vst3go_panel_set_size(VST3GoPanel* p, int32_t width, int32_t height);
// vst3go_panel_idle fires the view's run-loop timer (X11 editors pump
// GTK from it). Main thread.
void vst3go_panel_idle(VST3GoPanel* p);
void* vst3go_panel_view(VST3GoPanel* p);
void vst3go_panel_free(VST3GoPanel* p);

#endif

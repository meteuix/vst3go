//go:build standalone

// The editor window and main-thread run loop of the standalone application,
// one implementation per platform: window_darwin.c (Cocoa), window_x11.c
// (Linux with the webkitgtk tag), window_windows.c (Win32) and
// window_none.c (no editor window).
#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "clap/clap.h"

// vst3go_sa_window_api is the CLAP window API of the platform's editor
// windows, NULL without them.
const char* vst3go_sa_window_api(void);
// vst3go_sa_window_scale is the scale factor for gui set_scale; 0 when the
// API works in logical pixels (Cocoa).
double vst3go_sa_window_scale(void);
// vst3go_sa_window_open opens the application window: a toolbar strip
// toolbar_height high along the top (in the editor's pixels) over an editor
// area of width x height. It returns the editor area as the editor's parent
// and the toolbar's native parent (NSView*, HWND or X11 window ID), whose
// size it reports to goStandaloneToolbarSize when it changes.
bool vst3go_sa_window_open(const char* title, uint32_t width, uint32_t height, bool resizable, uint32_t toolbar_height,
                           clap_window_t* window, void** toolbar);
// vst3go_sa_window_resize sets the size of the editor area.
void vst3go_sa_window_resize(uint32_t width, uint32_t height);
void vst3go_sa_window_close(void);

// The platform's settings command (the application menu's Settings… on
// macOS) calls goStandaloneOpenSettings.

// vst3go_sa_run runs the main-thread loop, calling goStandaloneTick every
// tick_ms, until vst3go_sa_quit or the user closes the window.
void vst3go_sa_run(uint32_t tick_ms);
void vst3go_sa_quit(void);

// Timers fired from the run loop (vst3go_sa_timer_fire); X11 only.
bool vst3go_sa_timers_supported(void);
bool vst3go_sa_timer_register(uint32_t period_ms, clap_id* id);
bool vst3go_sa_timer_unregister(clap_id id);

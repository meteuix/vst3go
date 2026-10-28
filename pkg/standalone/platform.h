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
// vst3go_sa_window_open opens the application window with a client area of
// width x height and returns it as the editor's parent.
bool vst3go_sa_window_open(const char* title, uint32_t width, uint32_t height, bool resizable, clap_window_t* window);
// vst3go_sa_window_resize sets the client area size.
void vst3go_sa_window_resize(uint32_t width, uint32_t height);
void vst3go_sa_window_close(void);

// vst3go_sa_run runs the main-thread loop, calling goStandaloneTick every
// tick_ms, until vst3go_sa_quit or the user closes the window.
void vst3go_sa_run(uint32_t tick_ms);
void vst3go_sa_quit(void);

// Timers fired from the run loop (vst3go_sa_timer_fire); X11 only.
bool vst3go_sa_timers_supported(void);
bool vst3go_sa_timer_register(uint32_t period_ms, clap_id* id);
bool vst3go_sa_timer_unregister(clap_id id);

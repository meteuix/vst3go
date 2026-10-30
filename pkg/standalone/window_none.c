//go:build standalone && !darwin && !windows && !(linux && webkitgtk)

// Builds without an editor view run headless: the Go side drives the
// main-thread loop.
#include "platform.h"

const char* vst3go_sa_window_api(void) { return NULL; }
double vst3go_sa_window_scale(void) { return 0; }
bool vst3go_sa_window_open(const char* title, uint32_t width, uint32_t height, bool resizable, uint32_t toolbar_height,
                           clap_window_t* window, void** toolbar) {
    (void)title; (void)width; (void)height; (void)resizable; (void)toolbar_height; (void)window; (void)toolbar;
    return false;
}
void vst3go_sa_window_resize(uint32_t width, uint32_t height) { (void)width; (void)height; }
void vst3go_sa_window_close(void) {}
void vst3go_sa_run(uint32_t tick_ms) { (void)tick_ms; }
void vst3go_sa_quit(void) {}
bool vst3go_sa_timers_supported(void) { return false; }
bool vst3go_sa_timer_register(uint32_t period_ms, clap_id* id) { (void)period_ms; (void)id; return false; }
bool vst3go_sa_timer_unregister(clap_id id) { (void)id; return false; }

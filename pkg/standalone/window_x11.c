//go:build standalone && linux && webkitgtk

// X11 application window for the WebKitGTK editor. The run loop pumps the
// X connection and fires the editor's timers (CLAP timer-support), which
// drive GTK.
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <poll.h>
#include <time.h>

#include "_cgo_export.h"
#include "host.h"
#include "platform.h"

enum { MAX_TIMERS = 16 };

static Display* display;
static bool displayTried;
static Window window;
static Atom wmDelete;
static bool quitting;
static uint32_t windowWidth, windowHeight;
static struct {
    clap_id id;
    uint32_t period;
    int64_t next;
    bool used;
} timers[MAX_TIMERS];
static clap_id nextTimer;

static int64_t now_ms(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

// Without an X server the application runs headless.
const char* vst3go_sa_window_api(void) {
    if (!displayTried) {
        displayTried = true;
        display = XOpenDisplay(NULL);
    }
    return display ? CLAP_WINDOW_API_X11 : NULL;
}

double vst3go_sa_window_scale(void) { return 0; }

bool vst3go_sa_window_open(const char* title, uint32_t width, uint32_t height, bool resizable, clap_window_t* out) {
    if (!vst3go_sa_window_api()) return false;
    int screen = DefaultScreen(display);
    window = XCreateSimpleWindow(display, RootWindow(display, screen), 0, 0, width, height, 0,
                                 BlackPixel(display, screen), BlackPixel(display, screen));
    if (!window) return false;
    XStoreName(display, window, title);
    XClassHint* hint = XAllocClassHint();
    if (hint) {
        hint->res_name = (char*)title;
        hint->res_class = (char*)title;
        XSetClassHint(display, window, hint);
        XFree(hint);
    }
    if (!resizable) {
        XSizeHints* size = XAllocSizeHints();
        if (size) {
            size->flags = PMinSize | PMaxSize;
            size->min_width = size->max_width = (int)width;
            size->min_height = size->max_height = (int)height;
            XSetWMNormalHints(display, window, size);
            XFree(size);
        }
    }
    wmDelete = XInternAtom(display, "WM_DELETE_WINDOW", False);
    XSetWMProtocols(display, window, &wmDelete, 1);
    XSelectInput(display, window, StructureNotifyMask);
    XMapWindow(display, window);
    XFlush(display);
    windowWidth = width;
    windowHeight = height;
    out->api = CLAP_WINDOW_API_X11;
    out->x11 = window;
    return true;
}

void vst3go_sa_window_resize(uint32_t width, uint32_t height) {
    if (!window) return;
    XResizeWindow(display, window, width, height);
    XFlush(display);
}

void vst3go_sa_window_close(void) {
    if (!window) return;
    XDestroyWindow(display, window);
    XFlush(display);
    window = 0;
}

static void resized(uint32_t width, uint32_t height) {
    if (width == windowWidth && height == windowHeight) return;
    uint32_t w = width, h = height;
    vst3go_sa_gui_adjust(&w, &h);
    windowWidth = w;
    windowHeight = h;
    if (w != width || h != height) {
        // Snap the window to the size the editor accepts.
        vst3go_sa_window_resize(w, h);
    }
    vst3go_sa_gui_set_size(w, h);
}

static void handle_events(void) {
    while (XPending(display)) {
        XEvent e;
        XNextEvent(display, &e);
        if (e.xany.window != window) continue;
        if (e.type == ClientMessage && (Atom)e.xclient.data.l[0] == wmDelete) {
            vst3go_sa_quit();
        } else if (e.type == ConfigureNotify) {
            resized((uint32_t)e.xconfigure.width, (uint32_t)e.xconfigure.height);
        }
    }
}

void vst3go_sa_run(uint32_t tick_ms) {
    quitting = false;
    int64_t nextTick = now_ms() + tick_ms;
    while (!quitting) {
        handle_events();
        int64_t now = now_ms();
        for (int i = 0; i < MAX_TIMERS && !quitting; i++) {
            if (timers[i].used && now >= timers[i].next) {
                timers[i].next = now + timers[i].period;
                vst3go_sa_timer_fire(timers[i].id);
            }
        }
        if (now >= nextTick) {
            nextTick = now + tick_ms;
            goStandaloneTick();
        }
        if (quitting) break;
        int64_t wake = nextTick;
        for (int i = 0; i < MAX_TIMERS; i++) {
            if (timers[i].used && timers[i].next < wake) wake = timers[i].next;
        }
        XFlush(display);
        struct pollfd fd = { ConnectionNumber(display), POLLIN, 0 };
        int64_t timeout = wake - now_ms();
        poll(&fd, 1, timeout > 0 ? (int)timeout : 0);
    }
}

void vst3go_sa_quit(void) { quitting = true; }

bool vst3go_sa_timers_supported(void) { return vst3go_sa_window_api() != NULL; }

bool vst3go_sa_timer_register(uint32_t period_ms, clap_id* id) {
    for (int i = 0; i < MAX_TIMERS; i++) {
        if (timers[i].used) continue;
        timers[i].used = true;
        timers[i].id = ++nextTimer;
        timers[i].period = period_ms ? period_ms : 1;
        timers[i].next = now_ms() + timers[i].period;
        *id = timers[i].id;
        return true;
    }
    return false;
}

bool vst3go_sa_timer_unregister(clap_id id) {
    for (int i = 0; i < MAX_TIMERS; i++) {
        if (timers[i].used && timers[i].id == id) {
            timers[i].used = false;
            return true;
        }
    }
    return false;
}

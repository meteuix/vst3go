//go:build standalone && windows

// Win32 application window for the WebView2 editor.
#include <windows.h>

#include "_cgo_export.h"
#include "host.h"
#include "platform.h"

static HWND window;
static HWND editorArea;   // the plugin editor's parent, below the toolbar
static HWND toolbarArea;  // the toolbar's parent, along the top
static uint32_t toolbarHeight;
static bool quitting;
static const DWORD windowStyle = WS_OVERLAPPEDWINDOW;
static const UINT_PTR tickTimer = 1;

// frame_size is the window size around a client area.
static SIZE frame_size(uint32_t width, uint32_t height, DWORD style) {
    RECT r = { 0, 0, (LONG)width, (LONG)height };
    AdjustWindowRectEx(&r, style, FALSE, 0);
    SIZE s = { r.right - r.left, r.bottom - r.top };
    return s;
}

static LRESULT CALLBACK window_proc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    switch (msg) {
    case WM_CLOSE:
        vst3go_sa_quit();
        return 0;
    case WM_TIMER:
        if (wp == tickTimer) goStandaloneTick();
        return 0;
    case WM_SIZING: {
        // User drags snap to the sizes the editor accepts.
        RECT* r = (RECT*)lp;
        DWORD style = (DWORD)GetWindowLongPtrW(hwnd, GWL_STYLE);
        SIZE empty = frame_size(0, 0, style);
        uint32_t w = (uint32_t)(r->right - r->left - empty.cx);
        int32_t client = (int32_t)(r->bottom - r->top - empty.cy) - (int32_t)toolbarHeight;
        uint32_t h = client > 0 ? (uint32_t)client : 0;
        vst3go_sa_gui_adjust(&w, &h);
        SIZE s = frame_size(w, h + toolbarHeight, style);
        if (wp == WMSZ_LEFT || wp == WMSZ_TOPLEFT || wp == WMSZ_BOTTOMLEFT) r->left = r->right - s.cx;
        else r->right = r->left + s.cx;
        if (wp == WMSZ_TOP || wp == WMSZ_TOPLEFT || wp == WMSZ_TOPRIGHT) r->top = r->bottom - s.cy;
        else r->bottom = r->top + s.cy;
        return TRUE;
    }
    case WM_SIZE:
        if (wp != SIZE_MINIMIZED) {
            uint32_t w = LOWORD(lp), h = HIWORD(lp) > toolbarHeight ? HIWORD(lp) - toolbarHeight : 0;
            MoveWindow(toolbarArea, 0, 0, (int)w, (int)toolbarHeight, TRUE);
            MoveWindow(editorArea, 0, (int)toolbarHeight, (int)w, (int)h, TRUE);
            vst3go_sa_gui_set_size(w, h);
            goStandaloneToolbarSize(w, toolbarHeight);
        }
        return 0;
    }
    return DefWindowProcW(hwnd, msg, wp, lp);
}

const char* vst3go_sa_window_api(void) { return CLAP_WINDOW_API_WIN32; }

double vst3go_sa_window_scale(void) {
    SetProcessDPIAware();
    HDC dc = GetDC(NULL);
    int dpi = dc ? GetDeviceCaps(dc, LOGPIXELSX) : 96;
    if (dc) ReleaseDC(NULL, dc);
    return dpi > 0 ? dpi / 96.0 : 1;
}

bool vst3go_sa_window_open(const char* title, uint32_t width, uint32_t height, bool resizable, uint32_t toolbar,
                           clap_window_t* out, void** toolbarParent) {
    HINSTANCE instance = GetModuleHandleW(NULL);
    toolbarHeight = toolbar;
    WNDCLASSW area = { 0 };
    area.lpfnWndProc = DefWindowProcW;
    area.hInstance = instance;
    area.hCursor = LoadCursor(NULL, IDC_ARROW);
    area.lpszClassName = L"VST3GoStandaloneArea";
    RegisterClassW(&area);
    WNDCLASSW wc = { 0 };
    wc.lpfnWndProc = window_proc;
    wc.hInstance = instance;
    wc.hCursor = LoadCursor(NULL, IDC_ARROW);
    wc.hbrBackground = (HBRUSH)GetStockObject(BLACK_BRUSH);
    wc.lpszClassName = L"VST3GoStandalone";
    RegisterClassW(&wc);

    wchar_t name[256];
    if (!MultiByteToWideChar(CP_UTF8, 0, title, -1, name, 256)) name[0] = 0;
    DWORD style = resizable ? windowStyle : windowStyle & ~(WS_THICKFRAME | WS_MAXIMIZEBOX);
    SIZE s = frame_size(width, height + toolbar, style);
    window = CreateWindowExW(0, wc.lpszClassName, name, style | WS_CLIPCHILDREN, CW_USEDEFAULT, CW_USEDEFAULT, s.cx, s.cy,
                             NULL, NULL, instance, NULL);
    if (!window) return false;
    DWORD child = WS_CHILD | WS_VISIBLE | WS_CLIPCHILDREN;
    toolbarArea = CreateWindowExW(0, area.lpszClassName, L"", child, 0, 0, (int)width, (int)toolbar, window, NULL, instance, NULL);
    editorArea = CreateWindowExW(0, area.lpszClassName, L"", child, 0, (int)toolbar, (int)width, (int)height, window, NULL, instance, NULL);
    if (!toolbarArea || !editorArea) {
        DestroyWindow(window);
        window = NULL;
        return false;
    }
    ShowWindow(window, SW_SHOW);
    UpdateWindow(window);
    out->api = CLAP_WINDOW_API_WIN32;
    out->win32 = editorArea;
    *toolbarParent = toolbarArea;
    return true;
}

void vst3go_sa_window_resize(uint32_t width, uint32_t height) {
    if (!window) return;
    SIZE s = frame_size(width, height + toolbarHeight, (DWORD)GetWindowLongPtrW(window, GWL_STYLE));
    SetWindowPos(window, NULL, 0, 0, s.cx, s.cy, SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE);
}

void vst3go_sa_window_close(void) {
    if (!window) return;
    DestroyWindow(window);
    window = editorArea = toolbarArea = NULL;
}

void vst3go_sa_run(uint32_t tick_ms) {
    quitting = false;
    SetTimer(window, tickTimer, tick_ms, NULL);
    MSG msg;
    while (!quitting && GetMessageW(&msg, NULL, 0, 0) > 0) {
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }
    KillTimer(window, tickTimer);
}

void vst3go_sa_quit(void) {
    quitting = true;
    if (window) PostMessageW(window, WM_NULL, 0, 0);
}

bool vst3go_sa_timers_supported(void) { return false; }
bool vst3go_sa_timer_register(uint32_t period_ms, clap_id* id) { (void)period_ms; (void)id; return false; }
bool vst3go_sa_timer_unregister(clap_id id) { (void)id; return false; }

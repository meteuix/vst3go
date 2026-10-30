//go:build !darwin && !(windows && amd64) && !(linux && webkitgtk)

#include "editor_view.h"
#include <stddef.h>

const char* const VST3GoEditorPlatformType = NULL;

void* VST3GoCreateEditorView(uintptr_t component) {
    (void)component;
    return NULL;
}

void VST3GoEditorViewPost(void* view, const char* json) {
    (void)view;
    (void)json;
}

void VST3GoEditorViewRequestResize(void* view, int32_t width, int32_t height) {
    (void)view;
    (void)width;
    (void)height;
}

// Without a platform view there is no page to ask.
void VST3GoEditorViewChooseFile(void* view, uint32_t request, const char* title, const char* extensions, int folder) {
    (void)view;
    (void)request;
    (void)title;
    (void)extensions;
    (void)folder;
}

void* VST3GoFloatingWindowCreate(int32_t width, int32_t height, int resizable, VST3GoFloatingCallbacks callbacks) {
    (void)width; (void)height; (void)resizable; (void)callbacks;
    return NULL;
}
void* VST3GoFloatingWindowParent(void* window) { return window; }
void VST3GoFloatingWindowSetTitle(void* window, const char* title) { (void)window; (void)title; }
void VST3GoFloatingWindowSetTransient(void* window, void* owner) { (void)window; (void)owner; }
void VST3GoFloatingWindowSetSize(void* window, int32_t width, int32_t height) { (void)window; (void)width; (void)height; }
void VST3GoFloatingWindowShow(void* window, int visible) { (void)window; (void)visible; }
void VST3GoFloatingWindowDestroy(void* window) { (void)window; }

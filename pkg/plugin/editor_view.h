#ifndef VST3GO_EDITOR_VIEW_H
#define VST3GO_EDITOR_VIEW_H

#include "../../include/vst3/vst3_c_api.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct Steinberg_ViewRect Steinberg_ViewRect;

// VST3GoEditorPlatformType is the IPlugView platform type of the editor
// view on this platform (kPlatformTypeNSView, ...), NULL without one.
extern const char* const VST3GoEditorPlatformType;

void* VST3GoCreateEditorView(uintptr_t component);
// VST3GoEditorViewPost delivers a UTF-8 JSON message to the page's
// window.__vst3goReceive.
void VST3GoEditorViewPost(void* view, const char* json);
// VST3GoEditorViewRequestResize asks the host frame to resize the view
// (IPlugFrame::resizeView); the host then calls onSize.
void VST3GoEditorViewRequestResize(void* view, int32_t width, int32_t height);
// VST3GoEditorViewChooseFile shows the platform's open file dialog
// (vst3go.chooseFile) without blocking: title is the prompt, extensions
// the accepted file name extensions, separated by ';' and without dots
// ("" for any file). With folder it chooses a folder instead
// (vst3go.chooseFolder) and extensions is unused. The answer goes to
// GoEditorFileChosen: the path, NULL when the user cancels, or an error
// when the dialog cannot open. In a sandbox (macOS, iOS) the chosen folder,
// with everything in it, and on macOS the chosen file stay readable in
// later runs (see VST3GoRestoreFileAccess in editor_view_darwin.c); a file
// chosen on iOS is a copy kept in the extension's container.
void VST3GoEditorViewChooseFile(void* view, uint32_t request, const char* title, const char* extensions, int folder);
extern void GoEditorFileChosen(uintptr_t component, uint32_t request, char* path, char* error);

// Floating editor windows (CLAP floating GUIs): a top-level window whose
// parent handle (VST3GoFloatingWindowParent: an NSView*, HWND or X11
// window ID) the editor view attaches to. On the UI thread, closed(ctx)
// runs when the user closes the window, which then is hidden, not
// destroyed, and resized(ctx, width, height) when the user resizes it.
// Platforms without an editor return NULL from Create.
typedef struct {
    void (*closed)(void* ctx);
    void (*resized)(void* ctx, int32_t width, int32_t height);
    void* ctx;
} VST3GoFloatingCallbacks;
void* VST3GoFloatingWindowCreate(int32_t width, int32_t height, int resizable, VST3GoFloatingCallbacks callbacks);
void* VST3GoFloatingWindowParent(void* window);
void VST3GoFloatingWindowSetTitle(void* window, const char* title);
// VST3GoFloatingWindowSetTransient keeps the window above a host window
// (the native handle of a clap_window_t).
void VST3GoFloatingWindowSetTransient(void* window, void* owner);
void VST3GoFloatingWindowSetSize(void* window, int32_t width, int32_t height);
void VST3GoFloatingWindowShow(void* window, int visible);
void VST3GoFloatingWindowDestroy(void* window);
// Editor size from the processor's Editor (editor.go).
extern void GoEditorSize(uintptr_t component, int32_t* width, int32_t* height);
extern int GoEditorCanResize(uintptr_t component);
extern void GoEditorConstrainSize(uintptr_t component, int32_t* width, int32_t* height);
extern char* GoEditControllerGetEditorHTML(uintptr_t component);
extern void GoEditControllerClearEditorView(uintptr_t component, void* view);
// GoEditorMessage handles a JSON message posted by the editor page
// (window.vst3go in editor.js): parameter gestures and plugin messages.
extern void GoEditorMessage(uintptr_t component, char* json, int size);

#ifdef __cplusplus
}
#endif

#endif

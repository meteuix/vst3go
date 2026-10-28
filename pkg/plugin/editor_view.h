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

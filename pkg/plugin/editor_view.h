#ifndef VST3GO_EDITOR_VIEW_H
#define VST3GO_EDITOR_VIEW_H

#include "../../include/vst3/vst3_c_api.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct Steinberg_ViewRect Steinberg_ViewRect;

void* VST3GoCreateEditorView(uintptr_t component);
void VST3GoEditorViewUpdateParameter(void* view, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue normalized, Steinberg_Vst_ParamValue plain);
extern char* GoEditControllerGetEditorHTML(uintptr_t component);
extern void GoEditControllerClearEditorView(uintptr_t component, void* view);
// GoEditorMessage handles a JSON message posted by the editor page
// (window.vst3go in editor.js): parameter gestures and plugin messages.
extern void GoEditorMessage(uintptr_t component, char* json, int size);

#ifdef __cplusplus
}
#endif

#endif

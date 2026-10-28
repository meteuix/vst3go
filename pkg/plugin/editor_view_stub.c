//go:build !darwin && !(windows && amd64)

#include "editor_view.h"

void* VST3GoCreateEditorView(uintptr_t component) {
    (void)component;
    return NULL;
}

void VST3GoEditorViewUpdateParameter(void* view, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue normalized, Steinberg_Vst_ParamValue plain) {
    (void)view;
    (void)id;
    (void)normalized;
    (void)plain;
}

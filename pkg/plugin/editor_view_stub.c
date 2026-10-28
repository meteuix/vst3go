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

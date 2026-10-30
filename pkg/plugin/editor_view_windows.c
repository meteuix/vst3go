//go:build windows && amd64

#include <windows.h>
#include <shobjidl.h>
#include <WebView2.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>
#include <stdio.h>

#include "editor_view.h"

typedef struct VST3GoEditorView VST3GoEditorView;
typedef struct EditorEnvironmentHandler EditorEnvironmentHandler;
typedef struct EditorControllerHandler EditorControllerHandler;
typedef struct EditorMessageHandler EditorMessageHandler;

struct VST3GoEditorView {
    Steinberg_IPlugView view;
    ULONG refCount;
    uintptr_t component;
    HWND parentWindow;
    ICoreWebView2Environment* environment;
    ICoreWebView2Controller* controller;
    ICoreWebView2* webView;
    EditorEnvironmentHandler* environmentHandler;
    EditorControllerHandler* controllerHandler;
    EditorMessageHandler* messageHandler;
    EventRegistrationToken messageToken;
    BOOL hasMessageToken;
    WCHAR* userDataFolder;
    struct Steinberg_IPlugFrame* frame;
    Steinberg_ViewRect size;
    BOOL attached;
};

struct EditorEnvironmentHandler {
    ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler iface;
    ULONG refCount;
    VST3GoEditorView* view;
};

struct EditorControllerHandler {
    ICoreWebView2CreateCoreWebView2ControllerCompletedHandler iface;
    ULONG refCount;
    VST3GoEditorView* view;
};

struct EditorMessageHandler {
    ICoreWebView2WebMessageReceivedEventHandler iface;
    ULONG refCount;
    VST3GoEditorView* view;
};

static Steinberg_tresult editorView_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj);
static Steinberg_uint32 editorView_addRef(void* thisInterface);
static Steinberg_uint32 editorView_release(void* thisInterface);
static Steinberg_tresult editorView_isPlatformTypeSupported(void* thisInterface, Steinberg_FIDString type);
static Steinberg_tresult editorView_attached(void* thisInterface, void* parent, Steinberg_FIDString type);
static Steinberg_tresult editorView_removed(void* thisInterface);
static Steinberg_tresult editorView_onWheel(void* thisInterface, float distance);
static Steinberg_tresult editorView_onKeyDown(void* thisInterface, Steinberg_char16 key, Steinberg_int16 keyCode, Steinberg_int16 modifiers);
static Steinberg_tresult editorView_onKeyUp(void* thisInterface, Steinberg_char16 key, Steinberg_int16 keyCode, Steinberg_int16 modifiers);
static Steinberg_tresult editorView_getSize(void* thisInterface, Steinberg_ViewRect* size);
static Steinberg_tresult editorView_onSize(void* thisInterface, Steinberg_ViewRect* size);
static Steinberg_tresult editorView_onFocus(void* thisInterface, Steinberg_TBool state);
static Steinberg_tresult editorView_setFrame(void* thisInterface, struct Steinberg_IPlugFrame* frame);
static Steinberg_tresult editorView_canResize(void* thisInterface);
static Steinberg_tresult editorView_checkSizeConstraint(void* thisInterface, Steinberg_ViewRect* rect);

static HRESULT STDMETHODCALLTYPE editorEnvironmentHandler_QueryInterface(ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler* thisInterface, REFIID iid, void** object);
static ULONG STDMETHODCALLTYPE editorEnvironmentHandler_AddRef(ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler* thisInterface);
static ULONG STDMETHODCALLTYPE editorEnvironmentHandler_Release(ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler* thisInterface);
static HRESULT STDMETHODCALLTYPE editorEnvironmentHandler_Invoke(ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler* thisInterface, HRESULT result, ICoreWebView2Environment* environment);

static HRESULT STDMETHODCALLTYPE editorControllerHandler_QueryInterface(ICoreWebView2CreateCoreWebView2ControllerCompletedHandler* thisInterface, REFIID iid, void** object);
static ULONG STDMETHODCALLTYPE editorControllerHandler_AddRef(ICoreWebView2CreateCoreWebView2ControllerCompletedHandler* thisInterface);
static ULONG STDMETHODCALLTYPE editorControllerHandler_Release(ICoreWebView2CreateCoreWebView2ControllerCompletedHandler* thisInterface);
static HRESULT STDMETHODCALLTYPE editorControllerHandler_Invoke(ICoreWebView2CreateCoreWebView2ControllerCompletedHandler* thisInterface, HRESULT result, ICoreWebView2Controller* controller);

static HRESULT STDMETHODCALLTYPE editorMessageHandler_QueryInterface(ICoreWebView2WebMessageReceivedEventHandler* thisInterface, REFIID iid, void** object);
static ULONG STDMETHODCALLTYPE editorMessageHandler_AddRef(ICoreWebView2WebMessageReceivedEventHandler* thisInterface);
static ULONG STDMETHODCALLTYPE editorMessageHandler_Release(ICoreWebView2WebMessageReceivedEventHandler* thisInterface);
static HRESULT STDMETHODCALLTYPE editorMessageHandler_Invoke(ICoreWebView2WebMessageReceivedEventHandler* thisInterface, ICoreWebView2* sender, ICoreWebView2WebMessageReceivedEventArgs* args);

static ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandlerVtbl environmentHandlerVtbl = {
    editorEnvironmentHandler_QueryInterface,
    editorEnvironmentHandler_AddRef,
    editorEnvironmentHandler_Release,
    editorEnvironmentHandler_Invoke
};

static ICoreWebView2CreateCoreWebView2ControllerCompletedHandlerVtbl controllerHandlerVtbl = {
    editorControllerHandler_QueryInterface,
    editorControllerHandler_AddRef,
    editorControllerHandler_Release,
    editorControllerHandler_Invoke
};

static ICoreWebView2WebMessageReceivedEventHandlerVtbl messageHandlerVtbl = {
    editorMessageHandler_QueryInterface,
    editorMessageHandler_AddRef,
    editorMessageHandler_Release,
    editorMessageHandler_Invoke
};

static Steinberg_IPlugViewVtbl editorViewVtbl = {
    editorView_queryInterface,
    editorView_addRef,
    editorView_release,
    editorView_isPlatformTypeSupported,
    editorView_attached,
    editorView_removed,
    editorView_onWheel,
    editorView_onKeyDown,
    editorView_onKeyUp,
    editorView_getSize,
    editorView_onSize,
    editorView_onFocus,
    editorView_setFrame,
    editorView_canResize,
    editorView_checkSizeConstraint
};

static VST3GoEditorView* editorViewFromInterface(void* thisInterface) {
    return (VST3GoEditorView*)thisInterface;
}

static WCHAR* editorViewUtf8ToWide(const char* utf8) {
    if (!utf8) {
        return NULL;
    }

    int required = MultiByteToWideChar(CP_UTF8, 0, utf8, -1, NULL, 0);
    if (required <= 0) {
        return NULL;
    }

    WCHAR* wide = (WCHAR*)calloc((size_t)required, sizeof(WCHAR));
    if (!wide) {
        return NULL;
    }

    if (!MultiByteToWideChar(CP_UTF8, 0, utf8, -1, wide, required)) {
        free(wide);
        return NULL;
    }

    return wide;
}

static WCHAR* editorViewCreateUserDataFolder(VST3GoEditorView* view) {
    WCHAR tempPath[MAX_PATH];
    DWORD tempLength = GetTempPathW(MAX_PATH, tempPath);
    if (tempLength == 0 || tempLength >= MAX_PATH) {
        return NULL;
    }

    size_t required = wcslen(tempPath) + 64;
    WCHAR* folder = (WCHAR*)calloc(required, sizeof(WCHAR));
    if (!folder) {
        return NULL;
    }

    if (swprintf(folder, required, L"%lsVST3Go-WebView2-%lu-%p", tempPath, (unsigned long)GetCurrentProcessId(), (void*)view) < 0) {
        free(folder);
        return NULL;
    }

    if (!CreateDirectoryW(folder, NULL)) {
        DWORD error = GetLastError();
        if (error != ERROR_ALREADY_EXISTS) {
            free(folder);
            return NULL;
        }
    }

    return folder;
}

static void editorViewReleaseWebView(VST3GoEditorView* view) {
    if (!view) {
        return;
    }

    if (view->webView && view->hasMessageToken) {
        view->webView->lpVtbl->remove_WebMessageReceived(view->webView, view->messageToken);
        view->hasMessageToken = FALSE;
    }

    if (view->messageHandler) {
        view->messageHandler->iface.lpVtbl->Release(&view->messageHandler->iface);
        view->messageHandler = NULL;
    }

    if (view->webView) {
        view->webView->lpVtbl->Release(view->webView);
        view->webView = NULL;
    }

    if (view->controller) {
        view->controller->lpVtbl->Release(view->controller);
        view->controller = NULL;
    }

    if (view->controllerHandler) {
        view->controllerHandler->iface.lpVtbl->Release(&view->controllerHandler->iface);
        view->controllerHandler = NULL;
    }

    if (view->environment) {
        view->environment->lpVtbl->Release(view->environment);
        view->environment = NULL;
    }

    if (view->environmentHandler) {
        view->environmentHandler->iface.lpVtbl->Release(&view->environmentHandler->iface);
        view->environmentHandler = NULL;
    }

    if (view->userDataFolder) {
        RemoveDirectoryW(view->userDataFolder);
        free(view->userDataFolder);
        view->userDataFolder = NULL;
    }
}

static void editorViewUpdateBounds(VST3GoEditorView* view) {
    if (!view || !view->controller) {
        return;
    }

    RECT bounds = { 0, 0, 0, 0 };
    if (view->parentWindow && GetClientRect(view->parentWindow, &bounds)) {
        // use the actual host window size
    } else {
        bounds.left = 0;
        bounds.top = 0;
        bounds.right = view->size.right - view->size.left;
        bounds.bottom = view->size.bottom - view->size.top;
    }

    view->controller->lpVtbl->put_Bounds(view->controller, bounds);
}

static BOOL editorViewLoadHtml(VST3GoEditorView* view) {
    if (!view || !view->webView) {
        return FALSE;
    }

    char* html = GoEditControllerGetEditorHTML(view->component);
    if (!html) {
        return FALSE;
    }

    WCHAR* wideHtml = editorViewUtf8ToWide(html);
    free(html);
    if (!wideHtml) {
        return FALSE;
    }

    HRESULT result = view->webView->lpVtbl->NavigateToString(view->webView, wideHtml);
    free(wideHtml);
    return SUCCEEDED(result);
}

static void editorViewConfigureWebView(VST3GoEditorView* view) {
    if (!view || !view->webView) {
        return;
    }

    ICoreWebView2Settings* settings = NULL;
    HRESULT result = view->webView->lpVtbl->get_Settings(view->webView, &settings);
    if (FAILED(result) || !settings) {
        return;
    }

    settings->lpVtbl->put_AreDefaultContextMenusEnabled(settings, FALSE);
    settings->lpVtbl->put_AreDevToolsEnabled(settings, FALSE);
    settings->lpVtbl->put_IsStatusBarEnabled(settings, FALSE);
    settings->lpVtbl->Release(settings);
}

static EditorEnvironmentHandler* editorViewCreateEnvironmentHandler(VST3GoEditorView* view) {
    EditorEnvironmentHandler* handler = (EditorEnvironmentHandler*)calloc(1, sizeof(EditorEnvironmentHandler));
    if (!handler) {
        return NULL;
    }

    handler->iface.lpVtbl = &environmentHandlerVtbl;
    handler->refCount = 1;
    handler->view = view;
    return handler;
}

static EditorControllerHandler* editorViewCreateControllerHandler(VST3GoEditorView* view) {
    EditorControllerHandler* handler = (EditorControllerHandler*)calloc(1, sizeof(EditorControllerHandler));
    if (!handler) {
        return NULL;
    }

    handler->iface.lpVtbl = &controllerHandlerVtbl;
    handler->refCount = 1;
    handler->view = view;
    return handler;
}

static EditorMessageHandler* editorViewCreateMessageHandler(VST3GoEditorView* view) {
    EditorMessageHandler* handler = (EditorMessageHandler*)calloc(1, sizeof(EditorMessageHandler));
    if (!handler) {
        return NULL;
    }

    handler->iface.lpVtbl = &messageHandlerVtbl;
    handler->refCount = 1;
    handler->view = view;
    return handler;
}

const char* const VST3GoEditorPlatformType = "HWND";

void* VST3GoCreateEditorView(uintptr_t component) {
    VST3GoEditorView* view = (VST3GoEditorView*)calloc(1, sizeof(VST3GoEditorView));
    if (!view) {
        return NULL;
    }

    view->view.lpVtbl = &editorViewVtbl;
    view->refCount = 1;
    view->component = component;
    view->size.left = 0;
    view->size.top = 0;
    GoEditorSize(component, &view->size.right, &view->size.bottom);
    return view;
}

void VST3GoEditorViewPost(void* thisInterface, const char* json) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view || !view->webView || !json) {
        return;
    }

    int size = MultiByteToWideChar(CP_UTF8, 0, json, -1, NULL, 0);
    if (size <= 0) {
        return;
    }
    WCHAR* payload = (WCHAR*)malloc((size_t)size * sizeof(WCHAR));
    if (!payload) {
        return;
    }
    MultiByteToWideChar(CP_UTF8, 0, json, -1, payload, size);
    view->webView->lpVtbl->PostWebMessageAsJson(view->webView, payload);
    free(payload);
}

static Steinberg_tresult editorView_queryInterface(void* thisInterface, const Steinberg_TUID iid, void** obj) {
    if (!obj) {
        return Steinberg_kInvalidArgument;
    }

    if (memcmp(iid, Steinberg_FUnknown_iid, sizeof(Steinberg_TUID)) == 0 ||
        memcmp(iid, Steinberg_IPlugView_iid, sizeof(Steinberg_TUID)) == 0) {
        *obj = thisInterface;
        editorView_addRef(thisInterface);
        return Steinberg_kResultOk;
    }

    *obj = NULL;
    return Steinberg_kNoInterface;
}

static Steinberg_uint32 editorView_addRef(void* thisInterface) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    return ++view->refCount;
}

static Steinberg_uint32 editorView_release(void* thisInterface) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view) {
        return 0;
    }

    if (view->refCount > 0) {
        view->refCount--;
    }

    if (view->refCount == 0) {
        GoEditControllerClearEditorView(view->component, view);
        editorViewReleaseWebView(view);
        free(view);
        return 0;
    }

    return view->refCount;
}

static Steinberg_tresult editorView_isPlatformTypeSupported(void* thisInterface, Steinberg_FIDString type) {
    (void)thisInterface;
    if (!type) {
        return Steinberg_kResultFalse;
    }

    return (strcmp(type, "HWND") == 0) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}

typedef HRESULT(STDAPICALLTYPE* CreateEnvironmentFn)(PCWSTR, PCWSTR, ICoreWebView2EnvironmentOptions*,
                                                     ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler*);

// editorViewCreateEnvironmentFn finds CreateCoreWebView2EnvironmentWithOptions
// in WebView2Loader.dll. Bundles ship the loader next to the plugin binary,
// a folder hosts do not search when they load the plugin (LoadLibraryW
// looks next to the host), so it is loaded from there by its full path,
// else from the usual search path. Main thread.
static CreateEnvironmentFn editorViewCreateEnvironmentFn(void) {
    static CreateEnvironmentFn create;
    if (create) {
        return create;
    }
    HMODULE self = NULL;
    HMODULE loader = NULL;
    WCHAR path[MAX_PATH];
    if (GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                           (LPCWSTR)(void*)editorViewCreateEnvironmentFn, &self)) {
        DWORD n = GetModuleFileNameW(self, path, MAX_PATH);
        WCHAR* slash = n > 0 && n < MAX_PATH ? wcsrchr(path, L'\\') : NULL;
        static const WCHAR name[] = L"WebView2Loader.dll";
        if (slash && (size_t)(slash + 1 - path) + sizeof(name) / sizeof(WCHAR) <= MAX_PATH) {
            memcpy(slash + 1, name, sizeof(name));
            loader = LoadLibraryW(path);
        }
    }
    if (!loader) {
        loader = LoadLibraryW(L"WebView2Loader.dll");
    }
    if (loader) {
        create = (CreateEnvironmentFn)(void*)GetProcAddress(loader, "CreateCoreWebView2EnvironmentWithOptions");
    }
    return create;
}

static Steinberg_tresult editorView_attached(void* thisInterface, void* parent, Steinberg_FIDString type) {
    (void)type;
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view || !parent) {
        return Steinberg_kResultFalse;
    }

    editorViewReleaseWebView(view);

    view->parentWindow = (HWND)parent;
    view->attached = TRUE;
    view->userDataFolder = editorViewCreateUserDataFolder(view);
    view->environmentHandler = editorViewCreateEnvironmentHandler(view);
    view->controllerHandler = editorViewCreateControllerHandler(view);
    if (!view->environmentHandler || !view->controllerHandler) {
        editorViewReleaseWebView(view);
        return Steinberg_kResultFalse;
    }

    CreateEnvironmentFn create = editorViewCreateEnvironmentFn();
    if (!create) {
        editorViewReleaseWebView(view);
        return Steinberg_kResultFalse;
    }
    HRESULT result = create(NULL, view->userDataFolder, NULL, &view->environmentHandler->iface);
    return SUCCEEDED(result) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_removed(void* thisInterface) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view) {
        return Steinberg_kResultFalse;
    }

    view->attached = FALSE;
    view->parentWindow = NULL;
    editorViewReleaseWebView(view);
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_onWheel(void* thisInterface, float distance) {
    (void)thisInterface;
    (void)distance;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_onKeyDown(void* thisInterface, Steinberg_char16 key, Steinberg_int16 keyCode, Steinberg_int16 modifiers) {
    (void)thisInterface;
    (void)key;
    (void)keyCode;
    (void)modifiers;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_onKeyUp(void* thisInterface, Steinberg_char16 key, Steinberg_int16 keyCode, Steinberg_int16 modifiers) {
    (void)thisInterface;
    (void)key;
    (void)keyCode;
    (void)modifiers;
    return Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_getSize(void* thisInterface, Steinberg_ViewRect* size) {
    if (!size) {
        return Steinberg_kResultFalse;
    }

    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view) {
        return Steinberg_kResultFalse;
    }

    *size = view->size;
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_onSize(void* thisInterface, Steinberg_ViewRect* size) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view || !size) {
        return Steinberg_kResultFalse;
    }

    view->size = *size;
    editorViewUpdateBounds(view);
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_onFocus(void* thisInterface, Steinberg_TBool state) {
    (void)thisInterface;
    (void)state;
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_setFrame(void* thisInterface, struct Steinberg_IPlugFrame* frame) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view) {
        return Steinberg_kResultFalse;
    }

    view->frame = frame;
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_canResize(void* thisInterface) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    return view && GoEditorCanResize(view->component) ? Steinberg_kResultOk : Steinberg_kResultFalse;
}

static Steinberg_tresult editorView_checkSizeConstraint(void* thisInterface, Steinberg_ViewRect* rect) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view || !rect) {
        return Steinberg_kInvalidArgument;
    }
    Steinberg_int32 width = rect->right - rect->left, height = rect->bottom - rect->top;
    GoEditorConstrainSize(view->component, &width, &height);
    rect->right = rect->left + width;
    rect->bottom = rect->top + height;
    return Steinberg_kResultOk;
}

void VST3GoEditorViewRequestResize(void* thisInterface, int32_t width, int32_t height) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view || !view->frame) {
        return;
    }
    Steinberg_ViewRect rect = { 0, 0, width, height };
    view->frame->lpVtbl->resizeView(view->frame, (struct Steinberg_IPlugView*)&view->view, &rect);
}

// ---- open file dialog ----

// The dialog is modal, so it runs from a timer of the UI thread's message
// loop rather than inside WebView2's message event, one at a time.
static struct {
    BOOL pending;
    uintptr_t component;
    uint32_t request;
    HWND owner;
    WCHAR* title;
    WCHAR* patterns;  // "*.wav;*.flac", NULL for any file
} fileDialog;

static WCHAR* wideString(const char* s) {
    int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
    WCHAR* w = n > 0 ? (WCHAR*)calloc((size_t)n, sizeof(WCHAR)) : NULL;
    if (w) MultiByteToWideChar(CP_UTF8, 0, s, -1, w, n);
    return w;
}

static char* utf8String(const WCHAR* w) {
    int n = WideCharToMultiByte(CP_UTF8, 0, w, -1, NULL, 0, NULL, NULL);
    char* s = n > 0 ? (char*)calloc((size_t)n, 1) : NULL;
    if (s) WideCharToMultiByte(CP_UTF8, 0, w, -1, s, n, NULL, NULL);
    return s;
}

static void clearFileDialog(void) {
    free(fileDialog.title);
    free(fileDialog.patterns);
    fileDialog.title = fileDialog.patterns = NULL;
    fileDialog.pending = FALSE;
}

static void CALLBACK showFileDialog(HWND hwnd, UINT msg, UINT_PTR timer, DWORD time) {
    (void)hwnd;
    (void)msg;
    (void)time;
    KillTimer(NULL, timer);
    char* path = NULL;
    const char* error = NULL;
    IFileOpenDialog* dialog = NULL;
    if (FAILED(CoCreateInstance(&CLSID_FileOpenDialog, NULL, CLSCTX_INPROC_SERVER, &IID_IFileOpenDialog, (void**)&dialog))) {
        error = "the file dialog is not available";
    } else {
        FILEOPENDIALOGOPTIONS options = 0;
        dialog->lpVtbl->GetOptions(dialog, &options);
        dialog->lpVtbl->SetOptions(dialog, options | FOS_FORCEFILESYSTEM | FOS_FILEMUSTEXIST);
        if (fileDialog.title) dialog->lpVtbl->SetTitle(dialog, fileDialog.title);
        if (fileDialog.patterns) {
            COMDLG_FILTERSPEC spec = { fileDialog.patterns, fileDialog.patterns };
            dialog->lpVtbl->SetFileTypes(dialog, 1, &spec);
        }
        HWND owner = fileDialog.owner && IsWindow(fileDialog.owner) ? fileDialog.owner : NULL;
        HRESULT hr = dialog->lpVtbl->Show(dialog, owner);
        IShellItem* item = NULL;
        if (SUCCEEDED(hr) && SUCCEEDED(dialog->lpVtbl->GetResult(dialog, &item))) {
            WCHAR* wide = NULL;
            if (SUCCEEDED(item->lpVtbl->GetDisplayName(item, SIGDN_FILESYSPATH, &wide))) {
                path = utf8String(wide);
                CoTaskMemFree(wide);
            }
            item->lpVtbl->Release(item);
            if (!path) error = "the chosen file has no file system path";
        } else if (hr != HRESULT_FROM_WIN32(ERROR_CANCELLED)) {
            error = "the file dialog failed";
        }
        dialog->lpVtbl->Release(dialog);
    }
    uintptr_t component = fileDialog.component;
    uint32_t request = fileDialog.request;
    clearFileDialog();
    GoEditorFileChosen(component, request, path, (char*)error);
    free(path);
}

void VST3GoEditorViewChooseFile(void* thisInterface, uint32_t request, const char* title, const char* extensions) {
    VST3GoEditorView* view = (VST3GoEditorView*)thisInterface;
    if (!view) {
        return;
    }
    if (fileDialog.pending) {
        GoEditorFileChosen(view->component, request, NULL, (char*)"a file dialog is already open");
        return;
    }
    fileDialog.pending = TRUE;
    fileDialog.component = view->component;
    fileDialog.request = request;
    fileDialog.owner = view->parentWindow ? GetAncestor(view->parentWindow, GA_ROOT) : NULL;
    fileDialog.title = title && *title ? wideString(title) : NULL;
    if (extensions && *extensions) {
        // "wav;flac" -> "*.wav;*.flac"
        size_t n = strlen(extensions), count = 1;
        for (size_t i = 0; i < n; i++) count += extensions[i] == ';';
        char* patterns = (char*)calloc(n + 2 * count + 1, 1);
        if (!patterns) {
            clearFileDialog();
            GoEditorFileChosen(view->component, request, NULL, (char*)"out of memory");
            return;
        }
        char* out = patterns;
        for (const char* ext = extensions;;) {
            const char* end = strchr(ext, ';');
            int len = end ? (int)(end - ext) : (int)strlen(ext);
            out += sprintf(out, "%s*.%.*s", out == patterns ? "" : ";", len, ext);
            if (!end) break;
            ext = end + 1;
        }
        fileDialog.patterns = wideString(patterns);
        free(patterns);
    }
    if (!SetTimer(NULL, 0, 0, showFileDialog)) {
        clearFileDialog();
        GoEditorFileChosen(view->component, request, NULL, (char*)"the file dialog could not be scheduled");
    }
}


static HRESULT STDMETHODCALLTYPE editorEnvironmentHandler_QueryInterface(ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler* thisInterface, REFIID iid, void** object) {
    (void)iid;
    if (!object) {
        return E_POINTER;
    }

    *object = thisInterface;
    editorEnvironmentHandler_AddRef(thisInterface);
    return S_OK;
}

static ULONG STDMETHODCALLTYPE editorEnvironmentHandler_AddRef(ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler* thisInterface) {
    EditorEnvironmentHandler* handler = (EditorEnvironmentHandler*)thisInterface;
    return ++handler->refCount;
}

static ULONG STDMETHODCALLTYPE editorEnvironmentHandler_Release(ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler* thisInterface) {
    EditorEnvironmentHandler* handler = (EditorEnvironmentHandler*)thisInterface;
    if (handler->refCount > 0) {
        handler->refCount--;
    }

    if (handler->refCount == 0) {
        free(handler);
        return 0;
    }

    return handler->refCount;
}

static HRESULT STDMETHODCALLTYPE editorEnvironmentHandler_Invoke(ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler* thisInterface, HRESULT result, ICoreWebView2Environment* environment) {
    EditorEnvironmentHandler* handler = (EditorEnvironmentHandler*)thisInterface;
    VST3GoEditorView* view = handler->view;
    if (!view || FAILED(result) || !environment) {
        return result;
    }

    view->environment = environment;
    environment->lpVtbl->AddRef(environment);

    if (!view->controllerHandler) {
        return E_FAIL;
    }

    HRESULT createResult = environment->lpVtbl->CreateCoreWebView2Controller(environment, view->parentWindow, &view->controllerHandler->iface);
    return createResult;
}

static HRESULT STDMETHODCALLTYPE editorControllerHandler_QueryInterface(ICoreWebView2CreateCoreWebView2ControllerCompletedHandler* thisInterface, REFIID iid, void** object) {
    (void)iid;
    if (!object) {
        return E_POINTER;
    }

    *object = thisInterface;
    editorControllerHandler_AddRef(thisInterface);
    return S_OK;
}

static ULONG STDMETHODCALLTYPE editorControllerHandler_AddRef(ICoreWebView2CreateCoreWebView2ControllerCompletedHandler* thisInterface) {
    EditorControllerHandler* handler = (EditorControllerHandler*)thisInterface;
    return ++handler->refCount;
}

static ULONG STDMETHODCALLTYPE editorControllerHandler_Release(ICoreWebView2CreateCoreWebView2ControllerCompletedHandler* thisInterface) {
    EditorControllerHandler* handler = (EditorControllerHandler*)thisInterface;
    if (handler->refCount > 0) {
        handler->refCount--;
    }

    if (handler->refCount == 0) {
        free(handler);
        return 0;
    }

    return handler->refCount;
}

static HRESULT STDMETHODCALLTYPE editorControllerHandler_Invoke(ICoreWebView2CreateCoreWebView2ControllerCompletedHandler* thisInterface, HRESULT result, ICoreWebView2Controller* controller) {
    EditorControllerHandler* handler = (EditorControllerHandler*)thisInterface;
    VST3GoEditorView* view = handler->view;
    if (!view || FAILED(result) || !controller) {
        return result;
    }

    view->controller = controller;
    controller->lpVtbl->AddRef(controller);

    HRESULT webViewResult = controller->lpVtbl->get_CoreWebView2(controller, &view->webView);
    if (FAILED(webViewResult) || !view->webView) {
        return webViewResult;
    }

    editorViewConfigureWebView(view);

    view->messageHandler = editorViewCreateMessageHandler(view);
    if (!view->messageHandler) {
        return E_OUTOFMEMORY;
    }

    HRESULT messageResult = view->webView->lpVtbl->add_WebMessageReceived(view->webView, &view->messageHandler->iface, &view->messageToken);
    if (FAILED(messageResult)) {
        return messageResult;
    }
    view->hasMessageToken = TRUE;

    editorViewUpdateBounds(view);
    editorViewLoadHtml(view);
    controller->lpVtbl->put_IsVisible(controller, TRUE);
    return S_OK;
}

static HRESULT STDMETHODCALLTYPE editorMessageHandler_QueryInterface(ICoreWebView2WebMessageReceivedEventHandler* thisInterface, REFIID iid, void** object) {
    (void)iid;
    if (!object) {
        return E_POINTER;
    }

    *object = thisInterface;
    editorMessageHandler_AddRef(thisInterface);
    return S_OK;
}

static ULONG STDMETHODCALLTYPE editorMessageHandler_AddRef(ICoreWebView2WebMessageReceivedEventHandler* thisInterface) {
    EditorMessageHandler* handler = (EditorMessageHandler*)thisInterface;
    return ++handler->refCount;
}

static ULONG STDMETHODCALLTYPE editorMessageHandler_Release(ICoreWebView2WebMessageReceivedEventHandler* thisInterface) {
    EditorMessageHandler* handler = (EditorMessageHandler*)thisInterface;
    if (handler->refCount > 0) {
        handler->refCount--;
    }

    if (handler->refCount == 0) {
        free(handler);
        return 0;
    }

    return handler->refCount;
}

static HRESULT STDMETHODCALLTYPE editorMessageHandler_Invoke(ICoreWebView2WebMessageReceivedEventHandler* thisInterface, ICoreWebView2* sender, ICoreWebView2WebMessageReceivedEventArgs* args) {
    (void)sender;
    EditorMessageHandler* handler = (EditorMessageHandler*)thisInterface;
    VST3GoEditorView* view = handler->view;
    if (!view || !args) {
        return E_POINTER;
    }

    LPWSTR message = NULL;
    HRESULT result = args->lpVtbl->get_WebMessageAsJson(args, &message);
    if (FAILED(result) || !message) {
        return result;
    }

    int size = WideCharToMultiByte(CP_UTF8, 0, message, -1, NULL, 0, NULL, NULL);
    if (size > 1) {
        char* json = (char*)malloc((size_t)size);
        if (json) {
            WideCharToMultiByte(CP_UTF8, 0, message, -1, json, size, NULL, NULL);
            GoEditorMessage(view->component, json, size - 1);
            free(json);
        }
    }

    CoTaskMemFree(message);
    return S_OK;
}

// ---- floating windows ----

typedef struct {
    HWND hwnd;
    VST3GoFloatingCallbacks callbacks;
} VST3GoFloatingWindow;

// The user closing the window hides it and tells the owner.
static LRESULT CALLBACK floatingProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    VST3GoFloatingWindow* w = (VST3GoFloatingWindow*)GetWindowLongPtrW(hwnd, GWLP_USERDATA);
    if (msg == WM_CLOSE) {
        ShowWindow(hwnd, SW_HIDE);
        if (w) w->callbacks.closed(w->callbacks.ctx);
        return 0;
    }
    if (msg == WM_SIZE && w && wParam != SIZE_MINIMIZED) {
        w->callbacks.resized(w->callbacks.ctx, (int32_t)LOWORD(lParam), (int32_t)HIWORD(lParam));
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

static HMODULE floatingModule(void) {
    HMODULE module = NULL;
    GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT, (LPCWSTR)(void*)floatingProc, &module);
    return module;
}

// floatingClass registers the window class once per module, under a name
// of its own so plugins in one process do not share it.
static const WCHAR* floatingClass(void) {
    static WCHAR name[64];
    static ATOM atom;
    if (!atom) {
        HMODULE module = floatingModule();
        swprintf(name, 64, L"VST3GoFloatingWindow_%p", (void*)module);
        WNDCLASSEXW wc;
        memset(&wc, 0, sizeof(wc));
        wc.cbSize = sizeof(wc);
        wc.lpfnWndProc = floatingProc;
        wc.hInstance = module;
        wc.hCursor = LoadCursor(NULL, IDC_ARROW);
        wc.lpszClassName = name;
        atom = RegisterClassExW(&wc);
    }
    return atom ? name : NULL;
}

static DWORD floatingStyle(HWND hwnd) { return (DWORD)GetWindowLongPtrW(hwnd, GWL_STYLE); }

void* VST3GoFloatingWindowCreate(int32_t width, int32_t height, int resizable, VST3GoFloatingCallbacks callbacks) {
    const WCHAR* cls = floatingClass();
    if (!cls) return NULL;
    VST3GoFloatingWindow* w = (VST3GoFloatingWindow*)calloc(1, sizeof(VST3GoFloatingWindow));
    if (!w) return NULL;
    DWORD style = WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN;
    if (!resizable) style &= ~(DWORD)(WS_THICKFRAME | WS_MAXIMIZEBOX);
    RECT rect = { 0, 0, width, height };
    AdjustWindowRect(&rect, style, FALSE);
    w->hwnd = CreateWindowExW(0, cls, L"", style, CW_USEDEFAULT, CW_USEDEFAULT, rect.right - rect.left, rect.bottom - rect.top,
                              NULL, NULL, floatingModule(), NULL);
    if (!w->hwnd) {
        free(w);
        return NULL;
    }
    // Set after creation: the size messages of CreateWindowEx are not
    // the user's.
    w->callbacks = callbacks;
    SetWindowLongPtrW(w->hwnd, GWLP_USERDATA, (LONG_PTR)w);
    return w;
}

void* VST3GoFloatingWindowParent(void* window) {
    return window ? (void*)((VST3GoFloatingWindow*)window)->hwnd : NULL;
}

void VST3GoFloatingWindowSetTitle(void* window, const char* title) {
    if (!window || !title) return;
    WCHAR wide[256];
    if (MultiByteToWideChar(CP_UTF8, 0, title, -1, wide, 256) > 0) SetWindowTextW(((VST3GoFloatingWindow*)window)->hwnd, wide);
}

void VST3GoFloatingWindowSetTransient(void* window, void* owner) {
    if (!window || !owner) return;
    HWND root = GetAncestor((HWND)owner, GA_ROOT);
    SetWindowLongPtrW(((VST3GoFloatingWindow*)window)->hwnd, GWLP_HWNDPARENT, (LONG_PTR)(root ? root : (HWND)owner));
}

void VST3GoFloatingWindowSetSize(void* window, int32_t width, int32_t height) {
    if (!window) return;
    HWND hwnd = ((VST3GoFloatingWindow*)window)->hwnd;
    RECT rect = { 0, 0, width, height };
    AdjustWindowRect(&rect, floatingStyle(hwnd), FALSE);
    SetWindowPos(hwnd, NULL, 0, 0, rect.right - rect.left, rect.bottom - rect.top, SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE);
}

void VST3GoFloatingWindowShow(void* window, int visible) {
    if (!window) return;
    ShowWindow(((VST3GoFloatingWindow*)window)->hwnd, visible ? SW_SHOW : SW_HIDE);
}

void VST3GoFloatingWindowDestroy(void* window) {
    if (!window) return;
    VST3GoFloatingWindow* w = (VST3GoFloatingWindow*)window;
    SetWindowLongPtrW(w->hwnd, GWLP_USERDATA, 0);
    DestroyWindow(w->hwnd);
    free(w);
}

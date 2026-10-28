//go:build windows && amd64

#include <windows.h>
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
    view->size.right = 900;
    view->size.bottom = 640;
    return view;
}

void VST3GoEditorViewUpdateParameter(void* thisInterface, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue normalized, Steinberg_Vst_ParamValue plain) {
    VST3GoEditorView* view = editorViewFromInterface(thisInterface);
    if (!view || !view->webView) {
        return;
    }

    WCHAR payload[256];
    if (swprintf(payload, sizeof(payload) / sizeof(payload[0]), L"{\"type\":\"param-change\",\"id\":%u,\"normalized\":%.17g,\"plain\":%.17g}", (unsigned int)id, normalized, plain) < 0) {
        return;
    }

    view->webView->lpVtbl->PostWebMessageAsJson(view->webView, payload);
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

    HRESULT result = CreateCoreWebView2EnvironmentWithOptions(NULL, view->userDataFolder, NULL, &view->environmentHandler->iface);
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
    (void)thisInterface;
    return Steinberg_kResultOk;
}

static Steinberg_tresult editorView_checkSizeConstraint(void* thisInterface, Steinberg_ViewRect* rect) {
    (void)thisInterface;
    if (!rect) {
        return Steinberg_kResultFalse;
    }

    return Steinberg_kResultOk;
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

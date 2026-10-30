// Loading a plugin binary and making directories on POSIX and on Windows
// (mingw-w64; scripts/docker_check_windows_wine.sh runs the checks under
// Wine).
#pragma once

#include <stdio.h>

#if defined(_WIN32)
#include <direct.h>
#include <windows.h>

#define RTLD_NOW 0
#define RTLD_LOCAL 0
// HOME_ENV names the variable of the user's home folder, SEP the path
// separator.
#define HOME_ENV "USERPROFILE"
#define SEP "\\"

static inline void* dlopen(const char* path, int flags) {
    (void)flags;
    return (void*)LoadLibraryA(path);
}
static inline void* dlsym(void* lib, const char* name) { return (void*)GetProcAddress((HMODULE)lib, name); }
static inline const char* dlerror(void) {
    static char msg[32];
    snprintf(msg, sizeof(msg), "error %lu", (unsigned long)GetLastError());
    return msg;
}
static inline int make_dir(const char* path) { return _mkdir(path); }
#else
#include <dlfcn.h>
#include <sys/stat.h>

#define HOME_ENV "HOME"
#define SEP "/"

static inline int make_dir(const char* path) { return mkdir(path, 0755); }
#endif

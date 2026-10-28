// Writes the Turtle files of an LV2 bundle (manifest.ttl, plugins.ttl and
// presets.ttl) by loading the built plugin binary (vst3go build -format lv2).
//
// usage: lv2info <binary> <bundle-dir>
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

int main(int argc, char** argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: %s <binary> <bundle-dir>\n", argv[0]);
        return 2;
    }
    void* lib = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    if (!lib) {
        fprintf(stderr, "lv2info: %s\n", dlerror());
        return 1;
    }
    typedef const char* (*WriteBundle)(const char* dir, const char* binary);
    WriteBundle write = (WriteBundle)dlsym(lib, "VST3GoLV2WriteBundle");
    if (!write || !dlsym(lib, "lv2_descriptor")) {
        fprintf(stderr, "lv2info: %s has no LV2 entry\n", argv[1]);
        return 1;
    }
    const char* base = strrchr(argv[1], '/');
    const char* err = write(argv[2], base ? base + 1 : argv[1]);
    if (err) {
        fprintf(stderr, "lv2info: %s\n", err);
        return 1;
    }
    return 0;
}

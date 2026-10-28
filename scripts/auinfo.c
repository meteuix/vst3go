// Prints the AudioComponents entry of an Audio Unit bundle's Info.plist by
// loading the built plugin binary (scripts/build_au.sh).
//
// usage: auinfo <binary>
#include <dlfcn.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>

int main(int argc, char** argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: %s <binary>\n", argv[0]);
        return 2;
    }
    void* lib = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    if (!lib) {
        fprintf(stderr, "auinfo: %s\n", dlerror());
        return 1;
    }
    typedef char* (*Plist)(bool*);
    Plist plist = (Plist)dlsym(lib, "GoAUComponentsPlist");
    if (!plist || !dlsym(lib, "VST3GoAUFactory")) {
        fprintf(stderr, "auinfo: %s has no Audio Unit factory\n", argv[1]);
        return 1;
    }
    bool ok = false;
    char* text = plist(&ok);
    if (!ok) {
        fprintf(stderr, "auinfo: %s\n", text ? text : "no components");
        return 1;
    }
    fputs(text, stdout);
    free(text);
    return 0;
}

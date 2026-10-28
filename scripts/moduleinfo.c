// moduleinfo writes the moduleinfo.json of a VST3 binary (Contents/Resources
// in the bundle) to stdout, in the format of the Steinberg moduleinfotool:
// hosts read it to scan the plugin without loading the binary.
//
// usage: moduleinfo <plugin-binary> <module-name>
// scripts/build_vst3.sh builds and runs it.
#include "vst3/vst3_c_api.h"
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

static void json_string(const char* s) {
    putchar('"');
    for (const unsigned char* p = (const unsigned char*)s; *p; p++) {
        switch (*p) {
        case '"': fputs("\\\"", stdout); break;
        case '\\': fputs("\\\\", stdout); break;
        case '\n': fputs("\\n", stdout); break;
        case '\t': fputs("\\t", stdout); break;
        default:
            if (*p < 0x20) printf("\\u%04x", *p);
            else putchar(*p);
        }
    }
    putchar('"');
}

// cid_string prints a class ID like Steinberg::VST3::UID::toString: plain
// bytes, or the GUID layout on Windows (COM-compatible TUIDs).
static void cid_string(const Steinberg_TUID cid, char out[33]) {
    const unsigned char* b = (const unsigned char*)cid;
#if defined(_WIN32)
    snprintf(out, 33, "%02X%02X%02X%02X%02X%02X%02X%02X%02X%02X%02X%02X%02X%02X%02X%02X",
             b[3], b[2], b[1], b[0], b[5], b[4], b[7], b[6], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15]);
#else
    for (int i = 0; i < 16; i++) snprintf(out + 2 * i, 3, "%02X", b[i]);
#endif
}

int main(int argc, char** argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: moduleinfo <plugin-binary> <module-name>\n");
        return 2;
    }
    void* lib = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    if (!lib) {
        fprintf(stderr, "moduleinfo: dlopen %s: %s\n", argv[1], dlerror());
        return 1;
    }
    typedef struct Steinberg_IPluginFactory* (*GetFactory)(void);
    GetFactory get = (GetFactory)dlsym(lib, "GetPluginFactory");
    struct Steinberg_IPluginFactory* factory = get ? get() : NULL;
    if (!factory) {
        fprintf(stderr, "moduleinfo: %s has no plugin factory\n", argv[1]);
        return 1;
    }
    struct Steinberg_IPluginFactory2* factory2 = NULL;
    if (factory->lpVtbl->queryInterface(factory, Steinberg_IPluginFactory2_iid, (void**)&factory2) != Steinberg_kResultOk || !factory2) {
        fprintf(stderr, "moduleinfo: %s has no IPluginFactory2\n", argv[1]);
        return 1;
    }

    struct Steinberg_PFactoryInfo fi;
    memset(&fi, 0, sizeof(fi));
    factory->lpVtbl->getFactoryInfo(factory, &fi);
    int count = factory->lpVtbl->countClasses(factory);

    struct Steinberg_PClassInfo2 first;
    memset(&first, 0, sizeof(first));
    if (count > 0) factory2->lpVtbl->getClassInfo2(factory2, 0, &first);

    printf("{\n  \"Name\": ");
    json_string(argv[2]);
    printf(",\n  \"Version\": ");
    json_string(first.version);
    printf(",\n  \"Factory Info\": {\n    \"Vendor\": ");
    json_string(fi.vendor);
    printf(",\n    \"URL\": ");
    json_string(fi.url);
    printf(",\n    \"E-Mail\": ");
    json_string(fi.email);
    printf(",\n    \"Flags\": {\n      \"Unicode\": %s,\n      \"Classes Discardable\": %s,\n      \"Component Non Discardable\": %s\n    }\n  },\n",
           (fi.flags & Steinberg_PFactoryInfo_FactoryFlags_kUnicode) ? "true" : "false",
           (fi.flags & Steinberg_PFactoryInfo_FactoryFlags_kClassesDiscardable) ? "true" : "false",
           (fi.flags & Steinberg_PFactoryInfo_FactoryFlags_kComponentNonDiscardable) ? "true" : "false");
    printf("  \"Compatibility\": [],\n  \"Classes\": [");
    for (int i = 0; i < count; i++) {
        struct Steinberg_PClassInfo2 ci;
        memset(&ci, 0, sizeof(ci));
        if (factory2->lpVtbl->getClassInfo2(factory2, i, &ci) != Steinberg_kResultOk) {
            fprintf(stderr, "moduleinfo: getClassInfo2(%d) failed\n", i);
            return 1;
        }
        char cid[33];
        cid_string(ci.cid, cid);
        printf("%s\n    {\n      \"CID\": \"%s\",\n      \"Category\": ", i ? "," : "", cid);
        json_string(ci.category);
        printf(",\n      \"Name\": ");
        json_string(ci.name);
        printf(",\n      \"Vendor\": ");
        json_string(ci.vendor);
        printf(",\n      \"Version\": ");
        json_string(ci.version);
        printf(",\n      \"SDKVersion\": ");
        json_string(ci.sdkVersion);
        printf(",\n      \"Sub Categories\": [");
        // Sub categories are '|'-separated.
        char sub[sizeof(ci.subCategories) + 1];
        snprintf(sub, sizeof(sub), "%s", ci.subCategories);
        int n = 0;
        for (char* tok = strtok(sub, "|"); tok; tok = strtok(NULL, "|")) {
            printf("%s", n++ ? ", " : "");
            json_string(tok);
        }
        printf("],\n      \"Class Flags\": %u,\n      \"Cardinality\": %d,\n      \"Snapshots\": []\n    }",
               (unsigned)ci.classFlags, (int)ci.cardinality);
    }
    printf("\n  ]\n}\n");
    return 0;
}

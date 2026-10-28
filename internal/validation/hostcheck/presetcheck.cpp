// presetcheck checks that .vstpreset files written by pkg/preset load with
// the VST3 SDK's own PresetFile, and that SDK-written presets have the
// layout pkg/preset reads (scripts/validate_vst3.sh drives both sides):
//
//   presetcheck <plugin> state <state-out>      component state with param 0 = 0.25
//   presetcheck <plugin> load <preset> <out>    PresetFile::loadPreset, then savePreset
#include "public.sdk/source/vst/vstpresetfile.h"
#include "pluginterfaces/vst/ivstaudioprocessor.h"
#include "pluginterfaces/vst/ivsteditcontroller.h"
#include <dlfcn.h>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>

using namespace Steinberg;
using namespace Steinberg::Vst;

#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, "FAIL presetcheck: " __VA_ARGS__); fprintf(stderr, "\n"); exit(1); } } while (0)

struct Instance {
    FUID cid;
    IComponent* component = nullptr;
    IEditController* controller = nullptr;
};

static Instance create(const char* path) {
    void* lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    CHECK(lib, "dlopen %s: %s", path, dlerror());
    auto getFactory = (IPluginFactory* (*)())dlsym(lib, "GetPluginFactory");
    CHECK(getFactory, "no GetPluginFactory");
    IPluginFactory* factory = getFactory();
    PClassInfo info;
    CHECK(factory->getClassInfo(0, &info) == kResultOk, "getClassInfo");
    Instance in;
    in.cid = FUID::fromTUID(info.cid);
    CHECK(factory->createInstance(info.cid, IComponent::iid, (void**)&in.component) == kResultOk, "createInstance");
    CHECK(in.component->initialize(nullptr) == kResultOk, "initialize");
    CHECK(in.component->queryInterface(IEditController::iid, (void**)&in.controller) == kResultOk, "single-component controller");
    return in;
}

// MemStream is a minimal growable IBStream for getState.
struct MemStream : IBStream {
    char data[1 << 16]; int64 size = 0, pos = 0;
    tresult PLUGIN_API queryInterface(const TUID, void** obj) override { *obj = nullptr; return kNoInterface; }
    uint32 PLUGIN_API addRef() override { return 1; }
    uint32 PLUGIN_API release() override { return 1; }
    tresult PLUGIN_API read(void* buf, int32 n, int32* got) override {
        int32 c = (int32)std::min<int64>(n, size - pos); memcpy(buf, data + pos, c); pos += c; if (got) *got = c; return kResultOk;
    }
    tresult PLUGIN_API write(void* buf, int32 n, int32* put) override {
        if (pos + n > (int64)sizeof(data)) return kResultFalse;
        memcpy(data + pos, buf, n); pos += n; size = std::max(size, pos); if (put) *put = n; return kResultOk;
    }
    tresult PLUGIN_API seek(int64 p, int32 mode, int64* result) override {
        pos = mode == kIBSeekSet ? p : mode == kIBSeekCur ? pos + p : size + p; if (result) *result = pos; return kResultOk;
    }
    tresult PLUGIN_API tell(int64* p) override { *p = pos; return kResultOk; }
};

int main(int argc, char** argv) {
    CHECK(argc >= 4, "usage: presetcheck <plugin> state <out> | load <preset> <out>");
    Instance in = create(argv[1]);
    if (!strcmp(argv[2], "state")) {
        in.controller->setParamNormalized(0, 0.25);
        MemStream s;
        CHECK(in.component->getState(&s) == kResultOk, "getState");
        FILE* f = fopen(argv[3], "wb");
        CHECK(f && fwrite(s.data, 1, (size_t)s.size, f) == (size_t)s.size, "write %s", argv[3]);
        fclose(f);
        char cid[33];
        in.cid.toString(cid);
        printf("%s\n", cid);
        return 0;
    }
    CHECK(!strcmp(argv[2], "load") && argc == 5, "unknown mode %s", argv[2]);
    IBStream* file = FileStream::open(argv[3], "rb");
    CHECK(file, "open %s", argv[3]);
    CHECK(PresetFile::loadPreset(file, in.cid, in.component, in.controller), "SDK PresetFile::loadPreset rejected %s", argv[3]);
    file->release();
    double v = in.controller->getParamNormalized(0);
    CHECK(std::fabs(v - 0.25) < 1e-9, "param 0 after loadPreset = %f, want 0.25", v);
    in.controller->setParamNormalized(0, 0.75);
    IBStream* out = FileStream::open(argv[4], "wb");
    CHECK(out, "create %s", argv[4]);
    CHECK(PresetFile::savePreset(out, in.cid, in.component, in.controller), "SDK PresetFile::savePreset");
    out->release();
    printf("preset ok\n");
    return 0;
}

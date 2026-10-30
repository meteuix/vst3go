// aracheck is a minimal ARA 2 host that checks the ARA runtime of
// examples/araclip through its VST3, CLAP and Audio Unit binaries: the
// factory (VST3 main factory class, CLAP ARA factory and plug-in
// extension, Audio Unit ARA properties), the document controller with the host's audio access, archiving, content access and
// model update interfaces, note analysis read back through content
// readers, binding a plugin instance as playback renderer, rendering a
// playback region at its song position, storing and restoring the document
// archive, and both teardown orders (plugin first, document controller
// first). scripts/validate_ara.sh builds and runs it.
//
// usage: aracheck vst3|clap <binary>
//        aracheck au <type> <subtype> <manufacturer> (macOS, installed component)
#include "vst3/vst3_c_api.h"
#include "clap/clap.h"
#include "ara/ARACLAP.h"
#include "portable.h"
#if defined(__APPLE__)
#include <AudioToolbox/AudioToolbox.h>
#include "ara/ARAAudioUnit.h"
#endif
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); fprintf(stderr, __VA_ARGS__); fprintf(stderr, "\n"); exit(1); } } while (0)

#define FACTORY_ID "com.vst3go.examples.araclip.ara"
#define ARCHIVE_ID "com.vst3go.examples.araclip.1"
#define RATE 48000
#define FRAMES (2 * RATE)
#define BLOCK 512

// ---- the host's song: one stereo recording, 0.5 s of silence, 1 s of
// A4 (MIDI 69), silence; the right channel is the left one at half level ----
static float audio[2][FRAMES];

static void make_audio(void) {
    for (int i = 0; i < FRAMES; i++) {
        float v = 0;
        if (i >= RATE / 2 && i < RATE / 2 + RATE) v = 0.5f * (float)sin(2 * M_PI * 440 * (i - RATE / 2) / RATE);
        audio[0][i] = v;
        audio[1][i] = 0.5f * v;
    }
}

// Host refs are small integers.
#define SOURCE_HOST_REF ((ARAAudioSourceHostRef)(intptr_t)2)
#define CONTEXT_HOST_REF ((ARAMusicalContextHostRef)(intptr_t)3)
#define READER_HOST_REF ((ARAAudioReaderHostRef)(intptr_t)4)

static struct {
    int readers, reads, badReads;
    int analysisCompleted, sourceChanged, documentChanged;
    int tempoReaders, tempoReadersOpened;
} hostLog;

// ---- audio access ----
static ARAAudioReaderHostRef createAudioReader(ARAAudioAccessControllerHostRef c, ARAAudioSourceHostRef s, ARABool use64) {
    (void)c;
    CHECK(s == SOURCE_HOST_REF, "audio reader for an unknown source");
    CHECK(!use64, "64-bit reader for a source that does not merit it");
    hostLog.readers++;
    return READER_HOST_REF;
}
static ARABool readAudioSamples(ARAAudioAccessControllerHostRef c, ARAAudioReaderHostRef r, ARASamplePosition pos, ARASampleCount n, void* const buffers[]) {
    (void)c;
    CHECK(r == READER_HOST_REF, "read from an unknown reader");
    hostLog.reads++;
    if (pos < 0 || n < 0 || pos + n > FRAMES) {
        hostLog.badReads++;
        return kARAFalse;
    }
    for (int ch = 0; ch < 2; ch++) memcpy(buffers[ch], &audio[ch][pos], (size_t)n * sizeof(float));
    return kARATrue;
}
static void destroyAudioReader(ARAAudioAccessControllerHostRef c, ARAAudioReaderHostRef r) {
    (void)c; (void)r;
    hostLog.readers--;
}
static const ARAAudioAccessControllerInterface audioAccess = {
    sizeof(ARAAudioAccessControllerInterface), createAudioReader, readAudioSamples, destroyAudioReader,
};

// ---- archiving: one in-memory archive ----
static struct { ARAByte data[1 << 16]; size_t size; } archive;
#define ARCHIVE_REF ((ARAArchiveReaderHostRef)(intptr_t)5)
#define ARCHIVE_WRITER ((ARAArchiveWriterHostRef)(intptr_t)6)

static ARASize getArchiveSize(ARAArchivingControllerHostRef c, ARAArchiveReaderHostRef r) {
    (void)c;
    CHECK(r == ARCHIVE_REF, "size of an unknown archive");
    return archive.size;
}
static ARABool readBytesFromArchive(ARAArchivingControllerHostRef c, ARAArchiveReaderHostRef r, ARASize pos, ARASize n, ARAByte buf[]) {
    (void)c;
    CHECK(r == ARCHIVE_REF, "read from an unknown archive");
    if (pos + n > archive.size) return kARAFalse;
    memcpy(buf, archive.data + pos, n);
    return kARATrue;
}
static ARABool writeBytesToArchive(ARAArchivingControllerHostRef c, ARAArchiveWriterHostRef w, ARASize pos, ARASize n, const ARAByte buf[]) {
    (void)c;
    CHECK(w == ARCHIVE_WRITER, "write to an unknown archive");
    if (pos + n > sizeof(archive.data)) return kARAFalse;
    memcpy(archive.data + pos, buf, n);
    if (pos + n > archive.size) archive.size = pos + n;
    return kARATrue;
}
static void archivingProgress(ARAArchivingControllerHostRef c, float v) {
    (void)c;
    CHECK(v >= 0 && v <= 1, "archiving progress %f", v);
}
static ARAPersistentID getDocumentArchiveID(ARAArchivingControllerHostRef c, ARAArchiveReaderHostRef r) {
    (void)c; (void)r;
    return ARCHIVE_ID;
}
static const ARAArchivingControllerInterface archiving = {
    sizeof(ARAArchivingControllerInterface), getArchiveSize, readBytesFromArchive, writeBytesToArchive,
    archivingProgress, archivingProgress, getDocumentArchiveID,
};

// ---- content access: the song is 4/4 at 120 BPM ----
static const ARAContentTempoEntry tempo[2] = { { 0, 0 }, { 2, 4 } };
static const ARAContentBarSignature bars[1] = { { 4, 4, 0 } };
#define TEMPO_READER ((ARAContentReaderHostRef)(intptr_t)7)
#define BARS_READER ((ARAContentReaderHostRef)(intptr_t)8)

static ARABool isContextContentAvailable(ARAContentAccessControllerHostRef c, ARAMusicalContextHostRef mc, ARAContentType type) {
    (void)c;
    CHECK(mc == CONTEXT_HOST_REF, "content of an unknown musical context");
    return type == kARAContentTypeTempoEntries || type == kARAContentTypeBarSignatures;
}
static ARAContentGrade getContextContentGrade(ARAContentAccessControllerHostRef c, ARAMusicalContextHostRef mc, ARAContentType type) {
    (void)c; (void)mc; (void)type;
    return kARAContentGradeApproved;
}
static ARAContentReaderHostRef createContextContentReader(ARAContentAccessControllerHostRef c, ARAMusicalContextHostRef mc, ARAContentType type, const ARAContentTimeRange* range) {
    (void)c; (void)range;
    CHECK(mc == CONTEXT_HOST_REF, "reader for an unknown musical context");
    hostLog.tempoReaders++;
    hostLog.tempoReadersOpened++;
    if (type == kARAContentTypeTempoEntries) return TEMPO_READER;
    CHECK(type == kARAContentTypeBarSignatures, "reader for content type %d", type);
    return BARS_READER;
}
static ARABool isSourceContentAvailable(ARAContentAccessControllerHostRef c, ARAAudioSourceHostRef s, ARAContentType type) {
    (void)c; (void)s; (void)type;
    return kARAFalse;
}
static ARAContentGrade getSourceContentGrade(ARAContentAccessControllerHostRef c, ARAAudioSourceHostRef s, ARAContentType type) {
    (void)c; (void)s; (void)type;
    return kARAContentGradeInitial;
}
static ARAContentReaderHostRef createSourceContentReader(ARAContentAccessControllerHostRef c, ARAAudioSourceHostRef s, ARAContentType type, const ARAContentTimeRange* range) {
    (void)c; (void)s; (void)type; (void)range;
    CHECK(0, "reader for source content the host does not have");
    return NULL;
}
static ARAInt32 getHostReaderEventCount(ARAContentAccessControllerHostRef c, ARAContentReaderHostRef r) {
    (void)c;
    return r == TEMPO_READER ? 2 : 1;
}
static const void* getHostReaderEvent(ARAContentAccessControllerHostRef c, ARAContentReaderHostRef r, ARAInt32 i) {
    (void)c;
    if (r == TEMPO_READER) return &tempo[i];
    return &bars[i];
}
static void destroyHostReader(ARAContentAccessControllerHostRef c, ARAContentReaderHostRef r) {
    (void)c; (void)r;
    hostLog.tempoReaders--;
}
static const ARAContentAccessControllerInterface contentAccess = {
    sizeof(ARAContentAccessControllerInterface), isContextContentAvailable, getContextContentGrade, createContextContentReader,
    isSourceContentAvailable, getSourceContentGrade, createSourceContentReader,
    getHostReaderEventCount, getHostReaderEvent, destroyHostReader,
};

// ---- model updates ----
static void analysisProgress(ARAModelUpdateControllerHostRef c, ARAAudioSourceHostRef s, ARAAnalysisProgressState state, float v) {
    (void)c;
    CHECK(s == SOURCE_HOST_REF, "progress of an unknown source");
    CHECK(v >= 0 && v <= 1, "analysis progress %f", v);
    if (state == kARAAnalysisProgressCompleted) hostLog.analysisCompleted++;
}
static void sourceContentChanged(ARAModelUpdateControllerHostRef c, ARAAudioSourceHostRef s, const ARAContentTimeRange* range, ARAContentUpdateFlags flags) {
    (void)c; (void)range; (void)flags;
    CHECK(s == SOURCE_HOST_REF, "content change of an unknown source");
    hostLog.sourceChanged++;
}
static void modificationContentChanged(ARAModelUpdateControllerHostRef c, ARAAudioModificationHostRef m, const ARAContentTimeRange* range, ARAContentUpdateFlags flags) {
    (void)c; (void)m; (void)range; (void)flags;
}
static void regionContentChanged(ARAModelUpdateControllerHostRef c, ARAPlaybackRegionHostRef r, const ARAContentTimeRange* range, ARAContentUpdateFlags flags) {
    (void)c; (void)r; (void)range; (void)flags;
}
static void documentDataChanged(ARAModelUpdateControllerHostRef c) {
    (void)c;
    hostLog.documentChanged++;
}
static const ARAModelUpdateControllerInterface modelUpdate = {
    sizeof(ARAModelUpdateControllerInterface), analysisProgress, sourceContentChanged, modificationContentChanged,
    regionContentChanged, documentDataChanged,
};

static const ARADocumentControllerHostInstance hostInstance = {
    sizeof(ARADocumentControllerHostInstance),
    NULL, &audioAccess, NULL, &archiving, NULL, &contentAccess, NULL, &modelUpdate, NULL, NULL,
};

// ---- document ----
typedef struct {
    const ARADocumentControllerInstance* instance;
    const ARADocumentControllerInterface* dc;
    ARADocumentControllerRef ref;
    ARAMusicalContextRef context;
    ARARegionSequenceRef sequence;
    ARAAudioSourceRef source;
    ARAAudioModificationRef modification;
    ARAPlaybackRegionRef region;
} Song;

static double now(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (double)ts.tv_sec + ts.tv_nsec / 1e9;
}

static void check_factory(const ARAFactory* f) {
    CHECK(f && f->structSize >= kARAFactoryMinSize, "ARA factory");
    CHECK(f->lowestSupportedApiGeneration <= kARAAPIGeneration_2_0_Final && f->highestSupportedApiGeneration >= kARAAPIGeneration_2_0_Final,
          "API generations %d..%d", f->lowestSupportedApiGeneration, f->highestSupportedApiGeneration);
    CHECK(!strcmp(f->factoryID, FACTORY_ID), "factory ID %s", f->factoryID);
    CHECK(!strcmp(f->documentArchiveID, ARCHIVE_ID), "archive ID %s", f->documentArchiveID);
    CHECK(f->plugInName && !strcmp(f->plugInName, "ARA Clip"), "plugin name");
    CHECK(f->analyzeableContentTypesCount == 1 && f->analyzeableContentTypes[0] == kARAContentTypeNotes, "analyzeable content types");
    CHECK(f->createDocumentControllerWithDocument, "createDocumentControllerWithDocument");
    ARAInterfaceConfiguration config = { sizeof(config), f->highestSupportedApiGeneration, NULL };
    f->initializeARAWithConfiguration(&config);
}

// open_song creates the document controller and the song's model, the
// way a host does when it opens a song.
static Song open_song(const ARAFactory* f) {
    Song s;
    memset(&s, 0, sizeof(s));
    ARADocumentProperties props = { sizeof(props), "Song" };
    s.instance = f->createDocumentControllerWithDocument(&hostInstance, &props);
    CHECK(s.instance && s.instance->documentControllerInterface, "createDocumentControllerWithDocument");
    s.dc = s.instance->documentControllerInterface;
    s.ref = s.instance->documentControllerRef;
    CHECK(s.dc->getFactory(s.ref) == f, "getFactory");

    s.dc->beginEditing(s.ref);
    ARAMusicalContextProperties mc = { sizeof(mc), "Song", 0, NULL };
    s.context = s.dc->createMusicalContext(s.ref, CONTEXT_HOST_REF, &mc);
    ARARegionSequenceProperties seq = { sizeof(seq), "Track", 0, s.context, NULL };
    s.sequence = s.dc->createRegionSequence(s.ref, (ARARegionSequenceHostRef)(intptr_t)1, &seq);
    // Field by field: on x86 the struct is packed (ARAInterface.h), and a
    // constant initializer would put pointers at unaligned addresses.
    ARAAudioSourceProperties src;
    memset(&src, 0, sizeof(src));
    src.structSize = sizeof(src);
    src.name = "Take";
    src.persistentID = "take";
    src.sampleCount = FRAMES;
    src.sampleRate = RATE;
    src.channelCount = 2;
    s.source = s.dc->createAudioSource(s.ref, SOURCE_HOST_REF, &src);
    ARAAudioModificationProperties mod = { sizeof(mod), "Clip", "clip" };
    s.modification = s.dc->createAudioModification(s.ref, s.source, (ARAAudioModificationHostRef)(intptr_t)9, &mod);
    // The region plays the clip's 0.5..0.75 s at 1 s in the song.
    ARAPlaybackRegionProperties reg = { sizeof(reg), kARAPlaybackTransformationNoChanges, 0.5, 0.25, 1, 0.25, s.context, s.sequence, "Clip", NULL };
    s.region = s.dc->createPlaybackRegion(s.ref, s.modification, (ARAPlaybackRegionHostRef)(intptr_t)10, &reg);
    CHECK(s.context && s.sequence && s.source && s.modification && s.region, "model objects");
    s.dc->endEditing(s.ref);
    s.dc->enableAudioSourceSamplesAccess(s.ref, s.source, kARATrue);
    return s;
}

static void check_notes(Song* s) {
    ARAContentType notes = kARAContentTypeNotes;
    s->dc->requestAudioSourceContentAnalysis(s->ref, s->source, 1, &notes);
    // Generous: under emulation (Wine on Rosetta) Go code runs tens of
    // times slower than native.
    double deadline = now() + 60;
    while (s->dc->isAudioSourceContentAnalysisIncomplete(s->ref, s->source, notes)) {
        CHECK(now() < deadline, "analysis did not finish");
        s->dc->notifyModelUpdates(s->ref);
        struct timespec ts = { 0, 10000000 };
        nanosleep(&ts, NULL);
    }
    s->dc->notifyModelUpdates(s->ref);
    CHECK(hostLog.analysisCompleted >= 1 && hostLog.sourceChanged >= 1, "analysis notifications: %d completed, %d changes",
          hostLog.analysisCompleted, hostLog.sourceChanged);
    CHECK(hostLog.badReads == 0 && hostLog.reads > 0, "audio reads: %d, %d out of range", hostLog.reads, hostLog.badReads);
    CHECK(s->dc->isAudioSourceContentAvailable(s->ref, s->source, notes), "notes available");
    CHECK(s->dc->getAudioSourceContentGrade(s->ref, s->source, notes) == kARAContentGradeDetected, "notes grade");
    CHECK(!s->dc->isAudioSourceContentAvailable(s->ref, s->source, kARAContentTypeTempoEntries), "the plugin has no tempo of its own");

    ARAContentReaderRef r = s->dc->createAudioSourceContentReader(s->ref, s->source, notes, NULL);
    CHECK(r && s->dc->getContentReaderEventCount(s->ref, r) == 1, "one note");
    const ARAContentNote* n = s->dc->getContentReaderDataForEvent(s->ref, r, 0);
    CHECK(n->pitchNumber == 69 && fabsf(n->frequency - 440) < 2 && fabs(n->startPosition - 0.5) < 0.05 && fabs(n->noteDuration - 1) < 0.05,
          "note pitch %d, %f Hz, from %f for %f s", n->pitchNumber, n->frequency, n->startPosition, n->noteDuration);
    s->dc->destroyContentReader(s->ref, r);

    // The region's notes are in song time; the tone starts before the
    // region, so its note starts with the region.
    CHECK(s->dc->isPlaybackRegionContentAvailable(s->ref, s->region, notes), "region notes available");
    r = s->dc->createPlaybackRegionContentReader(s->ref, s->region, notes, NULL);
    CHECK(r && s->dc->getContentReaderEventCount(s->ref, r) == 1, "one region note");
    n = s->dc->getContentReaderDataForEvent(s->ref, r, 0);
    CHECK(fabs(n->startPosition - 1) < 0.05, "region note from %f s", n->startPosition);
    s->dc->destroyContentReader(s->ref, r);
    CHECK(s->dc->isAudioModificationPreservingAudioSourceSignal(s->ref, s->modification), "an untouched clip preserves the signal");
    CHECK(hostLog.tempoReadersOpened >= 2, "the tempo and bars of the musical context were not read");
}

// check_archive stores the song, reopens it in a second document
// controller and restores it there.
static void check_archive(const ARAFactory* f, Song* s) {
    archive.size = 0;
    CHECK(s->dc->storeObjectsToArchive(s->ref, ARCHIVE_WRITER, NULL), "storeObjectsToArchive");
    CHECK(archive.size > 0, "empty archive");
    Song r = open_song(f);
    r.dc->beginEditing(r.ref);
    CHECK(r.dc->restoreObjectsFromArchive(r.ref, ARCHIVE_REF, NULL), "restoreObjectsFromArchive");
    r.dc->endEditing(r.ref);
    CHECK(r.dc->isAudioModificationPreservingAudioSourceSignal(r.ref, r.modification), "restored clip");
    r.dc->destroyDocumentController(r.ref);
}

static void close_song(Song* s) {
    s->dc->beginEditing(s->ref);
    s->dc->destroyPlaybackRegion(s->ref, s->region);
    s->dc->destroyAudioModification(s->ref, s->modification);
    s->dc->enableAudioSourceSamplesAccess(s->ref, s->source, kARAFalse);
    s->dc->destroyAudioSource(s->ref, s->source);
    s->dc->destroyRegionSequence(s->ref, s->sequence);
    s->dc->destroyMusicalContext(s->ref, s->context);
    s->dc->endEditing(s->ref);
    s->dc->destroyDocumentController(s->ref);
    CHECK(hostLog.readers == 0 && hostLog.tempoReaders == 0, "host readers left open: %d audio, %d content", hostLog.readers, hostLog.tempoReaders);
}

// check_binding checks the plug-in extension of a bound instance.
static void check_binding(const ARAPlugInExtensionInstance* ext, Song* s) {
    CHECK(ext && ext->structSize >= kARAPlugInExtensionInstanceMinSize, "plug-in extension");
    CHECK(ext->playbackRendererInterface && ext->playbackRendererRef, "playback renderer role");
    CHECK(!ext->editorRendererInterface && !ext->editorViewInterface, "roles that were not assigned");
    ext->playbackRendererInterface->addPlaybackRegion(ext->playbackRendererRef, s->region);
}

// render checks one rendered block: at is the song position of its first
// frame; the region's audio starts at song frame RATE and comes from
// recording frame RATE/2.
typedef void (*Render)(void* plugin, int64_t at, int playing, float* out[2]);

static void check_render(void* plugin, Render render) {
    float l[BLOCK], r[BLOCK];
    float* out[2] = { l, r };
    int64_t at = RATE - 100;
    render(plugin, at, 1, out);
    for (int i = 0; i < BLOCK; i++) {
        float want = i < 100 ? 0 : audio[0][RATE / 2 + i - 100];
        CHECK(l[i] == want && r[i] == 0.5f * want, "frame %d: %f %f, want %f", i, l[i], r[i], want);
    }
    // The region ends 12000 frames later.
    at = RATE + 12000 - 10;
    render(plugin, at, 1, out);
    CHECK(l[9] == audio[0][RATE / 2 + 11999] && l[10] == 0, "region end: %f %f", l[9], l[10]);
    render(plugin, RATE, 0, out);
    for (int i = 0; i < BLOCK; i++) CHECK(l[i] == 0 && r[i] == 0, "rendered while stopped");
}

// ---- VST3 ----
static const Steinberg_TUID IMainFactory_iid = SMTG_INLINE_UID(0xDB2A1669, 0xFAFD42A5, 0xA82F864F, 0x7B6872EA);
static const Steinberg_TUID IPlugInEntryPoint_iid = SMTG_INLINE_UID(0x12814E54, 0xA1CE4076, 0x82B96813, 0x16950BD6);
static const Steinberg_TUID IPlugInEntryPoint2_iid = SMTG_INLINE_UID(0xCD9A5913, 0xC9EB46D7, 0x96CA53AD, 0xD1DB89F5);

typedef struct MainFactory {
    struct {
        Steinberg_tresult (SMTG_STDMETHODCALLTYPE* queryInterface)(void*, const Steinberg_TUID, void**);
        Steinberg_uint32 (SMTG_STDMETHODCALLTYPE* addRef)(void*);
        Steinberg_uint32 (SMTG_STDMETHODCALLTYPE* release)(void*);
        const ARAFactory* (SMTG_STDMETHODCALLTYPE* getFactory)(void*);
    }* lpVtbl;
} MainFactory;
typedef struct EntryPoint {
    struct {
        Steinberg_tresult (SMTG_STDMETHODCALLTYPE* queryInterface)(void*, const Steinberg_TUID, void**);
        Steinberg_uint32 (SMTG_STDMETHODCALLTYPE* addRef)(void*);
        Steinberg_uint32 (SMTG_STDMETHODCALLTYPE* release)(void*);
        const ARAFactory* (SMTG_STDMETHODCALLTYPE* getFactory)(void*);
        const ARAPlugInExtensionInstance* (SMTG_STDMETHODCALLTYPE* bindToDocumentController)(void*, ARADocumentControllerRef);
    }* lpVtbl;
} EntryPoint;
typedef struct EntryPoint2 {
    struct {
        Steinberg_tresult (SMTG_STDMETHODCALLTYPE* queryInterface)(void*, const Steinberg_TUID, void**);
        Steinberg_uint32 (SMTG_STDMETHODCALLTYPE* addRef)(void*);
        Steinberg_uint32 (SMTG_STDMETHODCALLTYPE* release)(void*);
        const ARAPlugInExtensionInstance* (SMTG_STDMETHODCALLTYPE* bindToDocumentControllerWithRoles)(void*, ARADocumentControllerRef, ARAPlugInInstanceRoleFlags, ARAPlugInInstanceRoleFlags);
    }* lpVtbl;
} EntryPoint2;

typedef struct {
    struct Steinberg_Vst_IComponent* comp;
    struct Steinberg_Vst_IAudioProcessor* proc;
} VST3Plugin;

static void vst3_render(void* plugin, int64_t at, int playing, float* out[2]) {
    VST3Plugin* p = plugin;
    static float silence[2][BLOCK];
    float* ins[2] = { silence[0], silence[1] };
    struct Steinberg_Vst_AudioBusBuffers in = { 2, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers32 = ins } };
    struct Steinberg_Vst_AudioBusBuffers outs = { 2, 0, { .Steinberg_Vst_AudioBusBuffers_channelBuffers32 = out } };
    struct Steinberg_Vst_ProcessContext ctx;
    memset(&ctx, 0, sizeof(ctx));
    ctx.state = playing ? Steinberg_Vst_ProcessContext_StatesAndFlags_kPlaying : 0;
    ctx.sampleRate = RATE;
    ctx.projectTimeSamples = at;
    struct Steinberg_Vst_ProcessData data;
    memset(&data, 0, sizeof(data));
    data.processMode = Steinberg_Vst_ProcessModes_kRealtime;
    data.symbolicSampleSize = Steinberg_Vst_SymbolicSampleSizes_kSample32;
    data.numSamples = BLOCK; data.numInputs = 1; data.numOutputs = 1; data.inputs = &in; data.outputs = &outs;
    data.processContext = &ctx;
    CHECK(p->proc->lpVtbl->process(p->proc, &data) == Steinberg_kResultOk, "process");
}

static void check_vst3(const char* path) {
    void* lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    CHECK(lib, "dlopen %s: %s", path, dlerror());
    typedef struct Steinberg_IPluginFactory* (*GetFactory)(void);
    GetFactory get = (GetFactory)dlsym(lib, "GetPluginFactory");
    CHECK(get, "no GetPluginFactory");
    struct Steinberg_IPluginFactory* f = get();

    // The audio module class and the ARA main factory class.
    Steinberg_TUID pluginCid, araCid;
    int plugins = 0, factories = 0;
    for (int i = 0; i < f->lpVtbl->countClasses(f); i++) {
        struct Steinberg_PClassInfo ci;
        CHECK(f->lpVtbl->getClassInfo(f, i, &ci) == Steinberg_kResultOk, "getClassInfo %d", i);
        if (!strcmp(ci.category, "Audio Module Class")) { memcpy(pluginCid, ci.cid, sizeof(pluginCid)); plugins++; }
        if (!strcmp(ci.category, "ARA Main Factory Class")) { memcpy(araCid, ci.cid, sizeof(araCid)); factories++; }
    }
    CHECK(plugins == 1 && factories == 1, "%d plugin and %d ARA main factory classes", plugins, factories);
    MainFactory* main = NULL;
    CHECK(f->lpVtbl->createInstance(f, araCid, IMainFactory_iid, (void**)&main) == Steinberg_kResultOk && main, "create the ARA main factory");
    const ARAFactory* factory = main->lpVtbl->getFactory(main);
    check_factory(factory);
    Song s = open_song(factory);
    check_notes(&s);
    check_archive(factory, &s);

    VST3Plugin p;
    CHECK(f->lpVtbl->createInstance(f, pluginCid, Steinberg_Vst_IComponent_iid, (void**)&p.comp) == Steinberg_kResultOk, "createInstance");
    CHECK(p.comp->lpVtbl->initialize(p.comp, NULL) == Steinberg_kResultOk, "initialize");
    CHECK(p.comp->lpVtbl->queryInterface(p.comp, Steinberg_Vst_IAudioProcessor_iid, (void**)&p.proc) == Steinberg_kResultOk, "QI processor");
    EntryPoint* entry = NULL;
    EntryPoint2* entry2 = NULL;
    CHECK(p.comp->lpVtbl->queryInterface(p.comp, IPlugInEntryPoint_iid, (void**)&entry) == Steinberg_kResultOk, "QI IPlugInEntryPoint");
    CHECK(p.comp->lpVtbl->queryInterface(p.comp, IPlugInEntryPoint2_iid, (void**)&entry2) == Steinberg_kResultOk, "QI IPlugInEntryPoint2");
    CHECK(entry->lpVtbl->getFactory(entry) == factory, "the instance's factory");

    // Bind before activation, as the ARA VST3 binding requires.
    const ARAPlugInExtensionInstance* ext = entry2->lpVtbl->bindToDocumentControllerWithRoles(entry2, s.ref, kARAPlaybackRendererRole | kARAEditorRendererRole | kARAEditorViewRole, kARAPlaybackRendererRole);
    check_binding(ext, &s);
    struct Steinberg_Vst_ProcessSetup setup = { Steinberg_Vst_ProcessModes_kRealtime, Steinberg_Vst_SymbolicSampleSizes_kSample32, BLOCK, RATE };
    CHECK(p.proc->lpVtbl->setupProcessing(p.proc, &setup) == Steinberg_kResultOk, "setupProcessing");
    CHECK(p.comp->lpVtbl->setActive(p.comp, 1) == Steinberg_kResultOk, "setActive");
    CHECK(p.proc->lpVtbl->setProcessing(p.proc, 1) == Steinberg_kResultOk, "setProcessing");
    check_render(&p, vst3_render);

    // Teardown, plugin first.
    p.proc->lpVtbl->setProcessing(p.proc, 0);
    p.comp->lpVtbl->setActive(p.comp, 0);
    ext->playbackRendererInterface->removePlaybackRegion(ext->playbackRendererRef, s.region);
    entry->lpVtbl->release(entry);
    entry2->lpVtbl->release(entry2);
    p.proc->lpVtbl->release(p.proc);
    p.comp->lpVtbl->terminate(p.comp);
    p.comp->lpVtbl->release(p.comp);
    close_song(&s);
    main->lpVtbl->release(main);
    factory->uninitializeARA();
    f->lpVtbl->release(f);
}

// ---- CLAP ----
static const void* host_get_extension(const clap_host_t* h, const char* id) { (void)h; (void)id; return NULL; }
static void host_request(const clap_host_t* h) { (void)h; }
static const clap_host_t host = {
    CLAP_VERSION_INIT, NULL, "aracheck", "vst3go", "", "1.0",
    host_get_extension, host_request, host_request, host_request,
};
static uint32_t no_events_size(const clap_input_events_t* l) { (void)l; return 0; }
static const clap_event_header_t* no_events_get(const clap_input_events_t* l, uint32_t i) { (void)l; (void)i; return NULL; }
static bool drop_event(const clap_output_events_t* l, const clap_event_header_t* e) { (void)l; (void)e; return true; }
static const clap_input_events_t noEvents = { NULL, no_events_size, no_events_get };
static const clap_output_events_t dropEvents = { NULL, drop_event };

static void clap_render(void* plugin, int64_t at, int playing, float* out[2]) {
    const clap_plugin_t* p = plugin;
    static float silence[2][BLOCK];
    float* ins[2] = { silence[0], silence[1] };
    clap_audio_buffer_t in = { ins, NULL, 2, 0, 0 };
    clap_audio_buffer_t outs = { out, NULL, 2, 0, 0 };
    clap_event_transport_t tr;
    memset(&tr, 0, sizeof(tr));
    tr.header.size = sizeof(tr);
    tr.header.type = CLAP_EVENT_TRANSPORT;
    tr.flags = CLAP_TRANSPORT_HAS_SECONDS_TIMELINE | (playing ? CLAP_TRANSPORT_IS_PLAYING : 0);
    tr.song_pos_seconds = (clap_sectime)llround((double)at / RATE * CLAP_SECTIME_FACTOR);
    clap_process_t proc;
    memset(&proc, 0, sizeof(proc));
    proc.steady_time = -1; proc.frames_count = BLOCK; proc.transport = &tr;
    proc.audio_inputs = &in; proc.audio_inputs_count = 1;
    proc.audio_outputs = &outs; proc.audio_outputs_count = 1;
    proc.in_events = &noEvents; proc.out_events = &dropEvents;
    CHECK(p->process(p, &proc) == CLAP_PROCESS_CONTINUE, "process");
}

static void check_clap(const char* path) {
    void* lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    CHECK(lib, "dlopen %s: %s", path, dlerror());
    const clap_plugin_entry_t* entry = (const clap_plugin_entry_t*)dlsym(lib, "clap_entry");
    CHECK(entry && entry->init(path), "clap_entry");
    const clap_plugin_factory_t* plugins = entry->get_factory(CLAP_PLUGIN_FACTORY_ID);
    const clap_ara_factory_t* araFactory = entry->get_factory(CLAP_EXT_ARA_FACTORY);
    CHECK(plugins && araFactory && araFactory->get_factory_count(araFactory) == 1, "one ARA factory");
    CHECK(entry->get_factory(CLAP_EXT_ARA_FACTORY_COMPAT) == araFactory, "ARA factory under its draft ID");
    const ARAFactory* factory = araFactory->get_ara_factory(araFactory, 0);
    check_factory(factory);
    const char* id = araFactory->get_plugin_id(araFactory, 0);
    const clap_plugin_descriptor_t* desc = plugins->get_plugin_descriptor(plugins, 0);
    CHECK(id && !strcmp(id, desc->id), "plugin ID %s of the ARA factory", id ? id : "(null)");
    int supported = 0;
    for (const char* const* f = desc->features; *f; f++) supported |= !strcmp(*f, CLAP_PLUGIN_FEATURE_ARA_SUPPORTED);
    CHECK(supported, "feature ara:supported");

    Song s = open_song(factory);
    check_notes(&s);
    check_archive(factory, &s);

    const clap_plugin_t* p = plugins->create_plugin(plugins, &host, id);
    CHECK(p && p->init(p), "create and init %s", id);
    const clap_ara_plugin_extension_t* ext = p->get_extension(p, CLAP_EXT_ARA_PLUGINEXTENSION);
    CHECK(ext && p->get_extension(p, CLAP_EXT_ARA_PLUGINEXTENSION_COMPAT) == ext, "ARA plug-in extension");
    CHECK(ext->get_factory(p) == factory, "the instance's factory");
    const ARAPlugInExtensionInstance* bound = ext->bind_to_document_controller(p, s.ref, kARAPlaybackRendererRole | kARAEditorRendererRole | kARAEditorViewRole, kARAPlaybackRendererRole);
    check_binding(bound, &s);
    CHECK(p->activate(p, RATE, 1, BLOCK) && p->start_processing(p), "activate");
    check_render((void*)p, clap_render);

    // Teardown, document controller first: the host removes and destroys
    // the region and the document, then the plugin.
    p->stop_processing(p);
    bound->playbackRendererInterface->removePlaybackRegion(bound->playbackRendererRef, s.region);
    close_song(&s);
    p->deactivate(p);
    p->destroy(p);
    factory->uninitializeARA();
    entry->deinit();
}

// ---- Audio Unit (macOS): the component must be installed ----
#if defined(__APPLE__)
typedef struct {
    AudioUnit unit;
    Float64 sample;
    Boolean playing;
} AUPlugin;

static OSStatus au_silence(void* ref, AudioUnitRenderActionFlags* flags, const AudioTimeStamp* ts, UInt32 bus, UInt32 frames, AudioBufferList* io) {
    (void)ref; (void)flags; (void)ts; (void)bus; (void)frames;
    for (UInt32 i = 0; i < io->mNumberBuffers; i++) memset(io->mBuffers[i].mData, 0, io->mBuffers[i].mDataByteSize);
    return noErr;
}

static OSStatus au_transport(void* ref, Boolean* playing, Boolean* recording, Boolean* changed, Float64* sample, Boolean* cycling, Float64* cycleStart, Float64* cycleEnd) {
    AUPlugin* p = ref;
    *playing = p->playing; *recording = false; *changed = false; *sample = p->sample;
    *cycling = false; *cycleStart = 0; *cycleEnd = 0;
    return noErr;
}

static void au_render(void* plugin, int64_t at, int playing, float* out[2]) {
    AUPlugin* p = plugin;
    p->sample = (Float64)at;
    p->playing = playing != 0;
    struct { AudioBufferList list; AudioBuffer second; } buffers;
    buffers.list.mNumberBuffers = 2;
    for (int ch = 0; ch < 2; ch++) {
        buffers.list.mBuffers[ch].mNumberChannels = 1;
        buffers.list.mBuffers[ch].mDataByteSize = BLOCK * sizeof(float);
        buffers.list.mBuffers[ch].mData = out[ch];
    }
    AudioTimeStamp ts;
    memset(&ts, 0, sizeof(ts));
    ts.mFlags = kAudioTimeStampSampleTimeValid;
    ts.mSampleTime = (Float64)at;
    AudioUnitRenderActionFlags flags = 0;
    OSStatus err = AudioUnitRender(p->unit, &flags, &ts, 0, BLOCK, &buffers.list);
    CHECK(err == noErr, "AudioUnitRender: %d", (int)err);
}

static OSType four_cc(const char* s) {
    CHECK(strlen(s) == 4, "four-character code %s", s);
    return (OSType)s[0] << 24 | (OSType)s[1] << 16 | (OSType)s[2] << 8 | (OSType)s[3];
}

static void check_au(const char* type, const char* subtype, const char* manufacturer) {
    AudioComponentDescription desc = { four_cc(type), four_cc(subtype), four_cc(manufacturer), 0, 0 };
    AudioComponent component = AudioComponentFindNext(NULL, &desc);
    CHECK(component, "no component %s %s %s", type, subtype, manufacturer);
    AUPlugin p;
    memset(&p, 0, sizeof(p));
    CHECK(AudioComponentInstanceNew(component, &p.unit) == noErr, "AudioComponentInstanceNew");

    ARAAudioUnitFactory af = { kARAAudioUnitMagic, NULL };
    UInt32 size = sizeof(af);
    CHECK(AudioUnitGetProperty(p.unit, kAudioUnitProperty_ARAFactory, kAudioUnitScope_Global, 0, &af, &size) == noErr && size == sizeof(af), "kAudioUnitProperty_ARAFactory");
    CHECK(af.inOutMagicNumber == kARAAudioUnitMagic, "magic number");
    const ARAFactory* factory = af.outFactory;
    check_factory(factory);
    Song s = open_song(factory);
    check_notes(&s);
    check_archive(factory, &s);

    ARAAudioUnitPlugInExtensionBinding b = { kARAAudioUnitMagic, s.ref, NULL, kARAPlaybackRendererRole | kARAEditorRendererRole | kARAEditorViewRole, kARAPlaybackRendererRole };
    size = sizeof(b);
    CHECK(AudioUnitGetProperty(p.unit, kAudioUnitProperty_ARAPlugInExtensionBindingWithRoles, kAudioUnitScope_Global, 0, &b, &size) == noErr, "kAudioUnitProperty_ARAPlugInExtensionBindingWithRoles");
    check_binding(b.outPlugInExtension, &s);

    AudioStreamBasicDescription format = {
        RATE, kAudioFormatLinearPCM, kAudioFormatFlagsNativeFloatPacked | kAudioFormatFlagIsNonInterleaved, sizeof(float), 1, sizeof(float), 2, 32, 0,
    };
    for (AudioUnitScope scope = kAudioUnitScope_Input; scope <= kAudioUnitScope_Output; scope++)
        CHECK(AudioUnitSetProperty(p.unit, kAudioUnitProperty_StreamFormat, scope, 0, &format, sizeof(format)) == noErr, "stream format");
    UInt32 maxFrames = BLOCK;
    CHECK(AudioUnitSetProperty(p.unit, kAudioUnitProperty_MaximumFramesPerSlice, kAudioUnitScope_Global, 0, &maxFrames, sizeof(maxFrames)) == noErr, "maximum frames");
    AURenderCallbackStruct input = { au_silence, NULL };
    CHECK(AudioUnitSetProperty(p.unit, kAudioUnitProperty_SetRenderCallback, kAudioUnitScope_Input, 0, &input, sizeof(input)) == noErr, "input callback");
    HostCallbackInfo callbacks;
    memset(&callbacks, 0, sizeof(callbacks));
    callbacks.hostUserData = &p;
    callbacks.transportStateProc2 = au_transport;
    CHECK(AudioUnitSetProperty(p.unit, kAudioUnitProperty_HostCallbacks, kAudioUnitScope_Global, 0, &callbacks, sizeof(callbacks)) == noErr, "host callbacks");
    CHECK(AudioUnitInitialize(p.unit) == noErr, "AudioUnitInitialize");
    check_render(&p, au_render);

    // Teardown, plugin first.
    AudioUnitUninitialize(p.unit);
    b.outPlugInExtension->playbackRendererInterface->removePlaybackRegion(b.outPlugInExtension->playbackRendererRef, s.region);
    AudioComponentInstanceDispose(p.unit);
    close_song(&s);
    factory->uninitializeARA();
}
#endif

int main(int argc, char** argv) {
    int au = argc == 5 && !strcmp(argv[1], "au");
#if !defined(__APPLE__)
    au = 0;
#endif
    if (!au && (argc != 3 || (strcmp(argv[1], "vst3") && strcmp(argv[1], "clap")))) {
        fprintf(stderr, "usage: aracheck vst3|clap <binary>\n       aracheck au <type> <subtype> <manufacturer> (macOS)\n");
        return 2;
    }
    make_audio();
    if (!strcmp(argv[1], "vst3")) check_vst3(argv[2]);
    else if (!strcmp(argv[1], "clap")) check_clap(argv[2]);
#if defined(__APPLE__)
    else check_au(argv[2], argv[3], argv[4]);
#endif
    printf("aracheck %s ok: factory, analysis (A4 found), archive, binding, region rendering, teardown\n", argv[1]);
    return 0;
}

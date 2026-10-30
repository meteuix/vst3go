#include "ara.h"
#include <stdlib.h>
#include <string.h>

// Copies a versioned ARA struct (structSize first) zero-extended to the
// size this runtime knows: fields an older host does not have read as 0.
static void copySized(void* dst, size_t size, const void* src) {
    memset(dst, 0, size);
    if (!src) {
        return;
    }
    size_t n = *(const ARASize*)src;
    memcpy(dst, src, n < size ? n : size);
}

// Naturally aligned copies for Go (see ara.h).

static VST3GoARAContextProps contextProps(const ARAMusicalContextProperties* properties) {
    ARAMusicalContextProperties p;
    copySized(&p, sizeof(p), properties);
    return (VST3GoARAContextProps){ p.name, p.orderIndex, p.color };
}

static VST3GoARASequenceProps sequenceProps(const ARARegionSequenceProperties* properties) {
    ARARegionSequenceProperties p;
    copySized(&p, sizeof(p), properties);
    return (VST3GoARASequenceProps){ p.name, p.orderIndex, (uintptr_t)p.musicalContextRef, p.color };
}

static VST3GoARARegionProps regionProps(const ARAPlaybackRegionProperties* properties) {
    ARAPlaybackRegionProperties p;
    copySized(&p, sizeof(p), properties);
    return (VST3GoARARegionProps){
        p.transformationFlags, p.startInModificationTime, p.durationInModificationTime, p.startInPlaybackTime,
        p.durationInPlaybackTime, (uintptr_t)p.regionSequenceRef, p.name, p.color,
    };
}

#define CTL(ref) ((VST3GoARAController*)(ref))
#define GO(ref) (CTL(ref)->go)
#define REF(x) ((uintptr_t)(x))

// Document controller interface

static void dc_destroy(ARADocumentControllerRef ref) {
    GoARADestroyController(GO(ref));
    free(CTL(ref));
}

static const ARAFactory* dc_getFactory(ARADocumentControllerRef ref) { return CTL(ref)->factory; }
static void dc_beginEditing(ARADocumentControllerRef ref) { GoARABeginEditing(GO(ref)); }
static void dc_endEditing(ARADocumentControllerRef ref) { GoARAEndEditing(GO(ref)); }
static void dc_notifyModelUpdates(ARADocumentControllerRef ref) { GoARANotifyModelUpdates(GO(ref)); }

// The ARA 1 archiving calls: restoring is an editing session that ends
// with restoring everything, storing stores everything.
static ARABool dc_beginRestoring(ARADocumentControllerRef ref, ARAArchiveReaderHostRef reader) {
    (void)reader;
    GoARABeginEditing(GO(ref));
    return kARATrue;
}

static ARABool dc_endRestoring(ARADocumentControllerRef ref, ARAArchiveReaderHostRef reader) {
    ARABool ok = GoARARestore(GO(ref), reader, NULL);
    GoARAEndEditing(GO(ref));
    return ok;
}

static ARABool dc_storeDocument(ARADocumentControllerRef ref, ARAArchiveWriterHostRef writer) {
    return GoARAStore(GO(ref), writer, NULL);
}

static ARABool dc_restoreObjects(ARADocumentControllerRef ref, ARAArchiveReaderHostRef reader, const ARARestoreObjectsFilter* filter) {
    if (!filter) {
        return GoARARestore(GO(ref), reader, NULL);
    }
    ARARestoreObjectsFilter f;
    copySized(&f, sizeof(f), filter);
    VST3GoARARestoreFilter g = {
        f.documentData, f.audioSourceIDsCount, f.audioSourceArchiveIDs, f.audioSourceCurrentIDs,
        f.audioModificationIDsCount, f.audioModificationArchiveIDs, f.audioModificationCurrentIDs,
    };
    return GoARARestore(GO(ref), reader, &g);
}

static ARABool dc_storeObjects(ARADocumentControllerRef ref, ARAArchiveWriterHostRef writer, const ARAStoreObjectsFilter* filter) {
    if (!filter) {
        return GoARAStore(GO(ref), writer, NULL);
    }
    ARAStoreObjectsFilter f;
    copySized(&f, sizeof(f), filter);
    VST3GoARAStoreFilter g = {
        f.documentData, f.audioSourceRefsCount, (const uintptr_t*)f.audioSourceRefs,
        f.audioModificationRefsCount, (const uintptr_t*)f.audioModificationRefs,
    };
    return GoARAStore(GO(ref), writer, &g);
}

static void dc_updateDocument(ARADocumentControllerRef ref, const ARADocumentProperties* properties) {
    ARADocumentProperties p;
    copySized(&p, sizeof(p), properties);
    GoARAUpdateDocument(GO(ref), (char*)p.name);
}

static ARAMusicalContextRef dc_createContext(ARADocumentControllerRef ref, ARAMusicalContextHostRef hostRef, const ARAMusicalContextProperties* properties) {
    VST3GoARAContextProps p = contextProps(properties);
    return (ARAMusicalContextRef)GoARACreateContext(GO(ref), REF(hostRef), &p);
}

static void dc_updateContext(ARADocumentControllerRef ref, ARAMusicalContextRef context, const ARAMusicalContextProperties* properties) {
    VST3GoARAContextProps p = contextProps(properties);
    GoARAUpdateContext(GO(ref), REF(context), &p);
}

static void dc_updateContextContent(ARADocumentControllerRef ref, ARAMusicalContextRef context, const ARAContentTimeRange* range, ARAContentUpdateFlags flags) {
    (void)range;
    (void)flags;
    GoARAUpdateContextContent(GO(ref), REF(context));
}

static void dc_destroyContext(ARADocumentControllerRef ref, ARAMusicalContextRef context) {
    GoARADestroyObject(GO(ref), VST3GO_ARA_CONTEXT, REF(context));
}

static ARARegionSequenceRef dc_createSequence(ARADocumentControllerRef ref, ARARegionSequenceHostRef hostRef, const ARARegionSequenceProperties* properties) {
    VST3GoARASequenceProps p = sequenceProps(properties);
    return (ARARegionSequenceRef)GoARACreateSequence(GO(ref), REF(hostRef), &p);
}

static void dc_updateSequence(ARADocumentControllerRef ref, ARARegionSequenceRef sequence, const ARARegionSequenceProperties* properties) {
    VST3GoARASequenceProps p = sequenceProps(properties);
    GoARAUpdateSequence(GO(ref), REF(sequence), &p);
}

static void dc_destroySequence(ARADocumentControllerRef ref, ARARegionSequenceRef sequence) {
    GoARADestroyObject(GO(ref), VST3GO_ARA_SEQUENCE, REF(sequence));
}

static ARAAudioSourceRef dc_createSource(ARADocumentControllerRef ref, ARAAudioSourceHostRef hostRef, const ARAAudioSourceProperties* properties) {
    ARAAudioSourceProperties p;
    copySized(&p, sizeof(p), properties);
    return (ARAAudioSourceRef)GoARACreateSource(GO(ref), REF(hostRef), &p);
}

static void dc_updateSource(ARADocumentControllerRef ref, ARAAudioSourceRef source, const ARAAudioSourceProperties* properties) {
    ARAAudioSourceProperties p;
    copySized(&p, sizeof(p), properties);
    GoARAUpdateSource(GO(ref), REF(source), &p);
}

static void dc_updateSourceContent(ARADocumentControllerRef ref, ARAAudioSourceRef source, const ARAContentTimeRange* range, ARAContentUpdateFlags flags) {
    (void)range;
    GoARAUpdateSourceContent(GO(ref), REF(source), flags);
}

static void dc_enableSourceAccess(ARADocumentControllerRef ref, ARAAudioSourceRef source, ARABool enable) {
    GoARAEnableSourceAccess(GO(ref), REF(source), enable);
}

static void dc_deactivateSource(ARADocumentControllerRef ref, ARAAudioSourceRef source, ARABool deactivate) {
    GoARADeactivate(GO(ref), VST3GO_ARA_SOURCE, REF(source), deactivate);
}

static void dc_destroySource(ARADocumentControllerRef ref, ARAAudioSourceRef source) {
    GoARADestroyObject(GO(ref), VST3GO_ARA_SOURCE, REF(source));
}

static ARAAudioModificationRef dc_createModification(ARADocumentControllerRef ref, ARAAudioSourceRef source, ARAAudioModificationHostRef hostRef, const ARAAudioModificationProperties* properties) {
    ARAAudioModificationProperties p;
    copySized(&p, sizeof(p), properties);
    return (ARAAudioModificationRef)GoARACreateModification(GO(ref), REF(source), REF(hostRef), &p);
}

static ARAAudioModificationRef dc_cloneModification(ARADocumentControllerRef ref, ARAAudioModificationRef modification, ARAAudioModificationHostRef hostRef, const ARAAudioModificationProperties* properties) {
    ARAAudioModificationProperties p;
    copySized(&p, sizeof(p), properties);
    return (ARAAudioModificationRef)GoARACloneModification(GO(ref), REF(modification), REF(hostRef), &p);
}

static void dc_updateModification(ARADocumentControllerRef ref, ARAAudioModificationRef modification, const ARAAudioModificationProperties* properties) {
    ARAAudioModificationProperties p;
    copySized(&p, sizeof(p), properties);
    GoARAUpdateModification(GO(ref), REF(modification), &p);
}

static void dc_deactivateModification(ARADocumentControllerRef ref, ARAAudioModificationRef modification, ARABool deactivate) {
    GoARADeactivate(GO(ref), VST3GO_ARA_MODIFICATION, REF(modification), deactivate);
}

static void dc_destroyModification(ARADocumentControllerRef ref, ARAAudioModificationRef modification) {
    GoARADestroyObject(GO(ref), VST3GO_ARA_MODIFICATION, REF(modification));
}

static ARAPlaybackRegionRef dc_createRegion(ARADocumentControllerRef ref, ARAAudioModificationRef modification, ARAPlaybackRegionHostRef hostRef, const ARAPlaybackRegionProperties* properties) {
    VST3GoARARegionProps p = regionProps(properties);
    return (ARAPlaybackRegionRef)GoARACreateRegion(GO(ref), REF(modification), REF(hostRef), &p);
}

static void dc_updateRegion(ARADocumentControllerRef ref, ARAPlaybackRegionRef region, const ARAPlaybackRegionProperties* properties) {
    VST3GoARARegionProps p = regionProps(properties);
    GoARAUpdateRegion(GO(ref), REF(region), &p);
}

static void dc_destroyRegion(ARADocumentControllerRef ref, ARAPlaybackRegionRef region) {
    GoARADestroyObject(GO(ref), VST3GO_ARA_REGION, REF(region));
}

static ARABool dc_sourceContentAvailable(ARADocumentControllerRef ref, ARAAudioSourceRef source, ARAContentType type) {
    return GoARAContentAvailable(GO(ref), VST3GO_ARA_SOURCE, REF(source), type);
}

static ARABool dc_analysisIncomplete(ARADocumentControllerRef ref, ARAAudioSourceRef source, ARAContentType type) {
    return GoARAAnalysisIncomplete(GO(ref), REF(source), type);
}

static void dc_requestAnalysis(ARADocumentControllerRef ref, ARAAudioSourceRef source, ARASize count, const ARAContentType types[]) {
    (void)count;
    (void)types;
    GoARARequestAnalysis(GO(ref), REF(source));
}

static ARAContentGrade dc_sourceContentGrade(ARADocumentControllerRef ref, ARAAudioSourceRef source, ARAContentType type) {
    return GoARAContentGrade(GO(ref), VST3GO_ARA_SOURCE, REF(source), type);
}

static ARAContentReaderRef dc_sourceContentReader(ARADocumentControllerRef ref, ARAAudioSourceRef source, ARAContentType type, const ARAContentTimeRange* range) {
    ARAContentTimeRange r = range ? *range : (ARAContentTimeRange){0, 0};
    return (ARAContentReaderRef)GoARACreateContentReader(GO(ref), VST3GO_ARA_SOURCE, REF(source), type, range ? &r : NULL);
}

static ARABool dc_modificationContentAvailable(ARADocumentControllerRef ref, ARAAudioModificationRef modification, ARAContentType type) {
    return GoARAContentAvailable(GO(ref), VST3GO_ARA_MODIFICATION, REF(modification), type);
}

static ARAContentGrade dc_modificationContentGrade(ARADocumentControllerRef ref, ARAAudioModificationRef modification, ARAContentType type) {
    return GoARAContentGrade(GO(ref), VST3GO_ARA_MODIFICATION, REF(modification), type);
}

static ARAContentReaderRef dc_modificationContentReader(ARADocumentControllerRef ref, ARAAudioModificationRef modification, ARAContentType type, const ARAContentTimeRange* range) {
    ARAContentTimeRange r = range ? *range : (ARAContentTimeRange){0, 0};
    return (ARAContentReaderRef)GoARACreateContentReader(GO(ref), VST3GO_ARA_MODIFICATION, REF(modification), type, range ? &r : NULL);
}

static ARABool dc_regionContentAvailable(ARADocumentControllerRef ref, ARAPlaybackRegionRef region, ARAContentType type) {
    return GoARAContentAvailable(GO(ref), VST3GO_ARA_REGION, REF(region), type);
}

static ARAContentGrade dc_regionContentGrade(ARADocumentControllerRef ref, ARAPlaybackRegionRef region, ARAContentType type) {
    return GoARAContentGrade(GO(ref), VST3GO_ARA_REGION, REF(region), type);
}

static ARAContentReaderRef dc_regionContentReader(ARADocumentControllerRef ref, ARAPlaybackRegionRef region, ARAContentType type, const ARAContentTimeRange* range) {
    ARAContentTimeRange r = range ? *range : (ARAContentTimeRange){0, 0};
    return (ARAContentReaderRef)GoARACreateContentReader(GO(ref), VST3GO_ARA_REGION, REF(region), type, range ? &r : NULL);
}

static ARAInt32 dc_readerCount(ARADocumentControllerRef ref, ARAContentReaderRef reader) {
    (void)ref;
    return ((VST3GoARAContentReader*)reader)->count;
}

static const void* dc_readerEvent(ARADocumentControllerRef ref, ARAContentReaderRef reader, ARAInt32 index) {
    (void)ref;
    VST3GoARAContentReader* r = (VST3GoARAContentReader*)reader;
    if (index < 0 || index >= r->count) {
        return NULL;
    }
    return r->events + (size_t)index * (size_t)r->stride;
}

static void dc_readerDestroy(ARADocumentControllerRef ref, ARAContentReaderRef reader) {
    (void)ref;
    free(reader);
}

static void dc_headAndTail(ARADocumentControllerRef ref, ARAPlaybackRegionRef region, ARATimeDuration* head, ARATimeDuration* tail) {
    double h = 0, t = 0;
    GoARAHeadAndTail(GO(ref), REF(region), &h, &t);
    if (head) *head = h;
    if (tail) *tail = t;
}

// Per factory: the processing algorithms (ara.Config.Algorithms), which
// live as long as the factory, and whether chunks open automatically.
static ARAProcessingAlgorithmProperties* algorithms[VST3GO_ARA_MAX_FACTORIES];
static ARAInt32 algorithmCounts[VST3GO_ARA_MAX_FACTORIES];
static ARABool openChunks[VST3GO_ARA_MAX_FACTORIES];

static ARAInt32 dc_algorithmCount(ARADocumentControllerRef ref) { return algorithmCounts[CTL(ref)->slot]; }

static const ARAProcessingAlgorithmProperties* dc_algorithmProperties(ARADocumentControllerRef ref, ARAInt32 index) {
    int32_t slot = CTL(ref)->slot;
    return index >= 0 && index < algorithmCounts[slot] ? &algorithms[slot][index] : NULL;
}

static ARAInt32 dc_algorithmForSource(ARADocumentControllerRef ref, ARAAudioSourceRef source) {
    return GoARAAlgorithm(GO(ref), REF(source));
}

static void dc_requestAlgorithm(ARADocumentControllerRef ref, ARAAudioSourceRef source, ARAInt32 index) {
    GoARARequestAlgorithm(GO(ref), REF(source), index);
}

// The runtime has no license gate of its own; a plugin with licensing
// checks it when the user opens the editor or renders.
static ARABool dc_isLicensed(ARADocumentControllerRef ref, ARABool runDialog, ARASize count, const ARAContentType types[], ARAPlaybackTransformationFlags flags) {
    (void)ref;
    (void)runDialog;
    (void)count;
    (void)types;
    (void)flags;
    return kARATrue;
}

// An audio file chunk is the archive of the one source, in the format of
// the document archives; hosts import it with restoreObjectsFromArchive.
static ARABool dc_storeChunk(ARADocumentControllerRef ref, ARAArchiveWriterHostRef writer, ARAAudioSourceRef source, ARAPersistentID* archiveID, ARABool* openAutomatically) {
    if (archiveID) *archiveID = CTL(ref)->factory->documentArchiveID;
    if (openAutomatically) *openAutomatically = openChunks[CTL(ref)->slot];
    return GoARAStoreChunk(GO(ref), writer, REF(source));
}

static ARABool dc_preservesSignal(ARADocumentControllerRef ref, ARAAudioModificationRef modification) {
    return GoARAPreservesSignal(GO(ref), REF(modification));
}

static const ARADocumentControllerInterface controllerInterface = {
    sizeof(ARADocumentControllerInterface),
    dc_destroy,
    dc_getFactory,
    dc_beginEditing,
    dc_endEditing,
    dc_notifyModelUpdates,
    dc_beginRestoring,
    dc_endRestoring,
    dc_storeDocument,
    dc_updateDocument,
    dc_createContext,
    dc_updateContext,
    dc_updateContextContent,
    dc_destroyContext,
    dc_createSource,
    dc_updateSource,
    dc_updateSourceContent,
    dc_enableSourceAccess,
    dc_deactivateSource,
    dc_destroySource,
    dc_createModification,
    dc_cloneModification,
    dc_updateModification,
    dc_deactivateModification,
    dc_destroyModification,
    dc_createRegion,
    dc_updateRegion,
    dc_destroyRegion,
    dc_sourceContentAvailable,
    dc_analysisIncomplete,
    dc_requestAnalysis,
    dc_sourceContentGrade,
    dc_sourceContentReader,
    dc_modificationContentAvailable,
    dc_modificationContentGrade,
    dc_modificationContentReader,
    dc_regionContentAvailable,
    dc_regionContentGrade,
    dc_regionContentReader,
    dc_readerCount,
    dc_readerEvent,
    dc_readerDestroy,
    dc_createSequence,
    dc_updateSequence,
    dc_destroySequence,
    dc_headAndTail,
    dc_restoreObjects,
    dc_storeObjects,
    dc_algorithmCount,
    dc_algorithmProperties,
    dc_algorithmForSource,
    dc_requestAlgorithm,
    dc_isLicensed,
    dc_storeChunk,
    dc_preservesSignal,
};

// Factories

static ARAFactory* factories[VST3GO_ARA_MAX_FACTORIES];

static const ARADocumentControllerInstance* createController(int32_t slot, const ARADocumentControllerHostInstance* host, const ARADocumentProperties* properties) {
    if (!host) {
        return NULL;
    }
    VST3GoARAController* c = (VST3GoARAController*)calloc(1, sizeof(VST3GoARAController));
    if (!c) {
        return NULL;
    }
    copySized(&c->host, sizeof(c->host), host);
    copySized(&c->audio, sizeof(c->audio), c->host.audioAccessControllerInterface);
    copySized(&c->archive, sizeof(c->archive), c->host.archivingControllerInterface);
    copySized(&c->content, sizeof(c->content), c->host.contentAccessControllerInterface);
    copySized(&c->model, sizeof(c->model), c->host.modelUpdateControllerInterface);
    if (!c->audio.createAudioReaderForSource || !c->archive.readBytesFromArchive || !c->archive.writeBytesToArchive) {
        free(c);
        return NULL;
    }
    c->instance.structSize = sizeof(c->instance);
    c->instance.documentControllerRef = (ARADocumentControllerRef)c;
    c->instance.documentControllerInterface = &controllerInterface;
    c->factory = factories[slot];
    c->slot = slot;
    ARADocumentProperties p;
    copySized(&p, sizeof(p), properties);
    c->go = GoARACreateController(slot, c, (char*)p.name);
    if (!c->go) {
        free(c);
        return NULL;
    }
    return &c->instance;
}

#define VST3GO_ARA_CREATE(n) \
    static const ARADocumentControllerInstance* create##n(const ARADocumentControllerHostInstance* host, const ARADocumentProperties* properties) { \
        return createController(n, host, properties); \
    }
VST3GO_ARA_CREATE(0)
VST3GO_ARA_CREATE(1)
VST3GO_ARA_CREATE(2)
VST3GO_ARA_CREATE(3)
VST3GO_ARA_CREATE(4)
VST3GO_ARA_CREATE(5)
VST3GO_ARA_CREATE(6)
VST3GO_ARA_CREATE(7)
VST3GO_ARA_CREATE(8)
VST3GO_ARA_CREATE(9)
VST3GO_ARA_CREATE(10)
VST3GO_ARA_CREATE(11)
VST3GO_ARA_CREATE(12)
VST3GO_ARA_CREATE(13)
VST3GO_ARA_CREATE(14)
VST3GO_ARA_CREATE(15)

typedef const ARADocumentControllerInstance* (*createFunc)(const ARADocumentControllerHostInstance*, const ARADocumentProperties*);
static const createFunc creators[VST3GO_ARA_MAX_FACTORIES] = {
    create0, create1, create2, create3, create4, create5, create6, create7,
    create8, create9, create10, create11, create12, create13, create14, create15,
};

// The runtime keeps no global ARA state: it checks the size of every host
// struct instead of the API generation.
static void initializeARA(const ARAInterfaceConfiguration* config) { (void)config; }
static void uninitializeARA(void) {}

ARAFactory* vst3go_ara_factory_new(int32_t slot, char* factoryID, char* name, char* vendor, char* url,
                                   char* version, char* archiveID, int32_t* contentTypes, int32_t contentTypeCount,
                                   int32_t transformations, char** algorithmIDs, char** algorithmNames,
                                   int32_t algorithmCount, ARABool audioFileChunks, ARABool openChunksAutomatically) {
    if (slot < 0 || slot >= VST3GO_ARA_MAX_FACTORIES || factories[slot] || algorithmCount < 0) {
        return NULL;
    }
    ARAFactory* f = (ARAFactory*)calloc(1, sizeof(ARAFactory));
    if (!f) {
        return NULL;
    }
    if (algorithmCount > 0) {
        algorithms[slot] = (ARAProcessingAlgorithmProperties*)calloc((size_t)algorithmCount, sizeof(ARAProcessingAlgorithmProperties));
        if (!algorithms[slot]) {
            free(f);
            return NULL;
        }
        for (int32_t i = 0; i < algorithmCount; i++) {
            algorithms[slot][i].structSize = sizeof(ARAProcessingAlgorithmProperties);
            algorithms[slot][i].persistentID = algorithmIDs[i];
            algorithms[slot][i].name = algorithmNames[i];
        }
        algorithmCounts[slot] = algorithmCount;
    }
    openChunks[slot] = openChunksAutomatically;
    f->structSize = sizeof(ARAFactory);
    f->lowestSupportedApiGeneration = kARAAPIGeneration_2_0_Final;
    f->highestSupportedApiGeneration = kARAAPIGeneration_2_3_Final;
    f->factoryID = factoryID;
    f->initializeARAWithConfiguration = initializeARA;
    f->uninitializeARA = uninitializeARA;
    f->plugInName = name;
    f->manufacturerName = vendor;
    f->informationURL = url;
    f->version = version;
    f->createDocumentControllerWithDocument = creators[slot];
    f->documentArchiveID = archiveID;
    f->analyzeableContentTypesCount = (ARASize)contentTypeCount;
    f->analyzeableContentTypes = (const ARAContentType*)contentTypes;
    f->supportedPlaybackTransformationFlags = transformations;
    f->supportsStoringAudioFileChunks = audioFileChunks;
    factories[slot] = f;
    return f;
}

const ARAFactory* vst3go_ara_factory(int32_t slot) {
    return slot >= 0 && slot < VST3GO_ARA_MAX_FACTORIES ? factories[slot] : NULL;
}

// Plug-in extension

typedef struct {
    ARAPlugInExtensionInstance instance;
    uintptr_t binding;
} Extension;

#define BINDING(ref) (((Extension*)(ref))->binding)

static void pr_addRegion(ARAPlaybackRendererRef ref, ARAPlaybackRegionRef region) {
    GoARARendererEdit(BINDING(ref), VST3GO_ARA_ADD_PLAYBACK_REGION, REF(region));
}
static void pr_removeRegion(ARAPlaybackRendererRef ref, ARAPlaybackRegionRef region) {
    GoARARendererEdit(BINDING(ref), VST3GO_ARA_REMOVE_PLAYBACK_REGION, REF(region));
}
static void er_addRegion(ARAEditorRendererRef ref, ARAPlaybackRegionRef region) {
    GoARARendererEdit(BINDING(ref), VST3GO_ARA_ADD_EDITOR_REGION, REF(region));
}
static void er_removeRegion(ARAEditorRendererRef ref, ARAPlaybackRegionRef region) {
    GoARARendererEdit(BINDING(ref), VST3GO_ARA_REMOVE_EDITOR_REGION, REF(region));
}
static void er_addSequence(ARAEditorRendererRef ref, ARARegionSequenceRef sequence) {
    GoARARendererEdit(BINDING(ref), VST3GO_ARA_ADD_EDITOR_SEQUENCE, REF(sequence));
}
static void er_removeSequence(ARAEditorRendererRef ref, ARARegionSequenceRef sequence) {
    GoARARendererEdit(BINDING(ref), VST3GO_ARA_REMOVE_EDITOR_SEQUENCE, REF(sequence));
}
static void ev_selection(ARAEditorViewRef ref, const ARAViewSelection* selection) {
    ARAViewSelection s;
    copySized(&s, sizeof(s), selection);
    GoARASelection(BINDING(ref), &s);
}
static void ev_hide(ARAEditorViewRef ref, ARASize count, const ARARegionSequenceRef sequences[]) {
    GoARAHideSequences(BINDING(ref), count, (uintptr_t*)sequences);
}

static const ARAPlaybackRendererInterface playbackRendererInterface = {
    sizeof(ARAPlaybackRendererInterface), pr_addRegion, pr_removeRegion
};
static const ARAEditorRendererInterface editorRendererInterface = {
    sizeof(ARAEditorRendererInterface), er_addRegion, er_removeRegion, er_addSequence, er_removeSequence
};
static const ARAEditorViewInterface editorViewInterface = {
    sizeof(ARAEditorViewInterface), ev_selection, ev_hide
};

const ARAPlugInExtensionInstance* vst3go_ara_extension_new(uintptr_t binding, int32_t roles) {
    Extension* e = (Extension*)calloc(1, sizeof(Extension));
    if (!e) {
        return NULL;
    }
    e->binding = binding;
    e->instance.structSize = sizeof(e->instance);
    if (roles & kARAPlaybackRendererRole) {
        e->instance.playbackRendererRef = (ARAPlaybackRendererRef)e;
        e->instance.playbackRendererInterface = &playbackRendererInterface;
    }
    if (roles & kARAEditorRendererRole) {
        e->instance.editorRendererRef = (ARAEditorRendererRef)e;
        e->instance.editorRendererInterface = &editorRendererInterface;
    }
    if (roles & kARAEditorViewRole) {
        e->instance.editorViewRef = (ARAEditorViewRef)e;
        e->instance.editorViewInterface = &editorViewInterface;
    }
    return &e->instance;
}

void vst3go_ara_extension_free(const ARAPlugInExtensionInstance* extension) {
    free((void*)extension);
}

VST3GoARAContentReader* vst3go_ara_content_reader_new(int32_t count, int32_t stride) {
    // One block: the host frees the reader with destroyContentReader.
    VST3GoARAContentReader* r = (VST3GoARAContentReader*)calloc(1, sizeof(VST3GoARAContentReader) + (size_t)count * (size_t)stride);
    if (!r) {
        return NULL;
    }
    r->count = count;
    r->stride = stride;
    r->events = (char*)(r + 1);
    return r;
}

void vst3go_ara_set_note(VST3GoARAContentReader* reader, int32_t index, const VST3GoARANote* note) {
    ARAContentNote* e = (ARAContentNote*)(reader->events + (size_t)index * (size_t)reader->stride);
    e->frequency = note->frequency;
    e->pitchNumber = note->pitch;
    e->volume = note->volume;
    e->startPosition = note->start;
    e->attackDuration = note->attack;
    e->noteDuration = note->duration;
    e->signalDuration = note->signal;
}

// Host calls

void* vst3go_ara_audio_reader_new(VST3GoARAController* c, uintptr_t sourceHostRef) {
    return c->audio.createAudioReaderForSource(c->host.audioAccessControllerHostRef, (ARAAudioSourceHostRef)sourceHostRef, kARAFalse);
}

ARABool vst3go_ara_audio_read(VST3GoARAController* c, void* reader, int64_t position, int64_t frames, int32_t channels, float* data) {
    void** buffers = (void**)malloc(sizeof(void*) * (size_t)channels);
    if (!buffers) {
        return kARAFalse;
    }
    for (int32_t ch = 0; ch < channels; ch++) {
        buffers[ch] = data + (size_t)ch * (size_t)frames;
    }
    ARABool ok = c->audio.readAudioSamples(c->host.audioAccessControllerHostRef, (ARAAudioReaderHostRef)reader, position, frames, buffers);
    free(buffers);
    return ok;
}

void vst3go_ara_audio_reader_free(VST3GoARAController* c, void* reader) {
    c->audio.destroyAudioReader(c->host.audioAccessControllerHostRef, (ARAAudioReaderHostRef)reader);
}

void* vst3go_ara_context_reader_new(VST3GoARAController* c, uintptr_t contextHostRef, int32_t contentType) {
    ARAContentAccessControllerHostRef host = c->host.contentAccessControllerHostRef;
    if (!c->content.isMusicalContextContentAvailable || !c->content.createMusicalContextContentReader ||
        !c->content.isMusicalContextContentAvailable(host, (ARAMusicalContextHostRef)contextHostRef, contentType)) {
        return NULL;
    }
    return c->content.createMusicalContextContentReader(host, (ARAMusicalContextHostRef)contextHostRef, contentType, NULL);
}

int32_t vst3go_ara_host_reader_count(VST3GoARAController* c, void* reader) {
    return c->content.getContentReaderEventCount(c->host.contentAccessControllerHostRef, (ARAContentReaderHostRef)reader);
}

const void* vst3go_ara_host_reader_event(VST3GoARAController* c, void* reader, int32_t index) {
    return c->content.getContentReaderDataForEvent(c->host.contentAccessControllerHostRef, (ARAContentReaderHostRef)reader, index);
}

void vst3go_ara_host_reader_free(VST3GoARAController* c, void* reader) {
    c->content.destroyContentReader(c->host.contentAccessControllerHostRef, (ARAContentReaderHostRef)reader);
}

void vst3go_ara_notify_progress(VST3GoARAController* c, uintptr_t sourceHostRef, int32_t state, float value) {
    if (c->model.notifyAudioSourceAnalysisProgress) {
        c->model.notifyAudioSourceAnalysisProgress(c->host.modelUpdateControllerHostRef, (ARAAudioSourceHostRef)sourceHostRef, state, value);
    }
}

void vst3go_ara_notify_content(VST3GoARAController* c, int32_t kind, uintptr_t hostRef, int32_t flags) {
    ARAModelUpdateControllerHostRef host = c->host.modelUpdateControllerHostRef;
    switch (kind) {
    case VST3GO_ARA_SOURCE:
        if (c->model.notifyAudioSourceContentChanged) {
            c->model.notifyAudioSourceContentChanged(host, (ARAAudioSourceHostRef)hostRef, NULL, flags);
        }
        break;
    case VST3GO_ARA_MODIFICATION:
        if (c->model.notifyAudioModificationContentChanged) {
            c->model.notifyAudioModificationContentChanged(host, (ARAAudioModificationHostRef)hostRef, NULL, flags);
        }
        break;
    case VST3GO_ARA_REGION:
        if (c->model.notifyPlaybackRegionContentChanged) {
            c->model.notifyPlaybackRegionContentChanged(host, (ARAPlaybackRegionHostRef)hostRef, NULL, flags);
        }
        break;
    }
}

void vst3go_ara_notify_document(VST3GoARAController* c) {
    if (c->model.notifyDocumentDataChanged) {
        c->model.notifyDocumentDataChanged(c->host.modelUpdateControllerHostRef);
    }
}

size_t vst3go_ara_archive_size(VST3GoARAController* c, void* reader) {
    return c->archive.getArchiveSize(c->host.archivingControllerHostRef, (ARAArchiveReaderHostRef)reader);
}

ARABool vst3go_ara_archive_read(VST3GoARAController* c, void* reader, size_t position, size_t length, void* buffer) {
    return c->archive.readBytesFromArchive(c->host.archivingControllerHostRef, (ARAArchiveReaderHostRef)reader, position, length, (ARAByte*)buffer);
}

ARABool vst3go_ara_archive_write(VST3GoARAController* c, void* writer, size_t position, size_t length, void* buffer) {
    return c->archive.writeBytesToArchive(c->host.archivingControllerHostRef, (ARAArchiveWriterHostRef)writer, position, length, (const ARAByte*)buffer);
}

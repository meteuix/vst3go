#ifndef VST3GO_ARA_H
#define VST3GO_ARA_H

// ARA (Audio Random Access) runtime: the ARA factory of each ARA plugin,
// the document controller the host drives, and the plug-in extension a
// bound instance exposes. The model lives in Go (pkg/ara); these thunks
// translate the C interfaces and keep host pointers on the C side.

#include <stddef.h>
#include <stdint.h>
#include "../../include/ara/ARAInterface.h"

// Highest number of ARA plugins in one binary: createDocumentController
// gets no factory argument, so every factory has its own entry function.
#define VST3GO_ARA_MAX_FACTORIES 16

// Model object kinds, for the generic calls into Go.
enum {
    VST3GO_ARA_CONTEXT = 0,
    VST3GO_ARA_SEQUENCE = 1,
    VST3GO_ARA_SOURCE = 2,
    VST3GO_ARA_MODIFICATION = 3,
    VST3GO_ARA_REGION = 4,
};

// Renderer edits, for GoARARendererEdit.
enum {
    VST3GO_ARA_ADD_PLAYBACK_REGION = 0,
    VST3GO_ARA_REMOVE_PLAYBACK_REGION = 1,
    VST3GO_ARA_ADD_EDITOR_REGION = 2,
    VST3GO_ARA_REMOVE_EDITOR_REGION = 3,
    VST3GO_ARA_ADD_EDITOR_SEQUENCE = 4,
    VST3GO_ARA_REMOVE_EDITOR_SEQUENCE = 5,
};

// One document controller. The host interfaces are copied zero-extended
// to the size this runtime knows, so a function the host does not provide
// is NULL.
typedef struct VST3GoARAController {
    ARADocumentControllerInstance instance;
    ARADocumentControllerHostInstance host;
    ARAAudioAccessControllerInterface audio;
    ARAArchivingControllerInterface archive;
    ARAContentAccessControllerInterface content;
    ARAModelUpdateControllerInterface model;
    const ARAFactory* factory;
    int32_t slot; // of the factory
    uintptr_t go; // cgo handle of the Go controller
} VST3GoARAController;

// Content the plugin hands to the host: count events of stride bytes.
typedef struct VST3GoARAContentReader {
    int32_t count;
    int32_t stride;
    char* events;
} VST3GoARAContentReader;

// ARAInterface.h packs its structs to 1 byte on x86, which leaves fields
// after an ARAInt32 misaligned; cgo cannot represent those. The calls into
// Go take these naturally aligned copies of such structs instead.
typedef struct VST3GoARAContextProps {
    const char* name;
    int32_t order;
    const ARAColor* color;
} VST3GoARAContextProps;

typedef struct VST3GoARASequenceProps {
    const char* name;
    int32_t order;
    uintptr_t context;
    const ARAColor* color;
} VST3GoARASequenceProps;

typedef struct VST3GoARARegionProps {
    int32_t flags;
    double startInModification;
    double durationInModification;
    double startInPlayback;
    double durationInPlayback;
    uintptr_t sequence;
    const char* name;
    const ARAColor* color;
} VST3GoARARegionProps;

typedef struct VST3GoARAStoreFilter {
    int32_t document;
    size_t sourceCount;
    const uintptr_t* sources;
    size_t modificationCount;
    const uintptr_t* modifications;
} VST3GoARAStoreFilter;

typedef struct VST3GoARARestoreFilter {
    int32_t document;
    size_t sourceCount;
    const char* const* sourceArchiveIDs;
    const char* const* sourceCurrentIDs;
    size_t modificationCount;
    const char* const* modificationArchiveIDs;
    const char* const* modificationCurrentIDs;
} VST3GoARARestoreFilter;

typedef struct VST3GoARANote {
    float frequency;
    int32_t pitch;
    float volume;
    double start;
    double attack;
    double duration;
    double signal;
} VST3GoARANote;

// Factories
ARAFactory* vst3go_ara_factory_new(int32_t slot, char* factoryID, char* name, char* vendor, char* url,
                                   char* version, char* archiveID, int32_t* contentTypes, int32_t contentTypeCount,
                                   int32_t transformations, char** algorithmIDs, char** algorithmNames,
                                   int32_t algorithmCount, ARABool audioFileChunks, ARABool openChunksAutomatically);
const ARAFactory* vst3go_ara_factory(int32_t slot);

// Plug-in extension of a bound instance.
const ARAPlugInExtensionInstance* vst3go_ara_extension_new(uintptr_t binding, int32_t roles);
void vst3go_ara_extension_free(const ARAPlugInExtensionInstance* extension);

// Plug-in content readers.
VST3GoARAContentReader* vst3go_ara_content_reader_new(int32_t count, int32_t stride);
void vst3go_ara_set_note(VST3GoARAContentReader* reader, int32_t index, const VST3GoARANote* note);

// Host calls.
void* vst3go_ara_audio_reader_new(VST3GoARAController* c, uintptr_t sourceHostRef);
ARABool vst3go_ara_audio_read(VST3GoARAController* c, void* reader, int64_t position, int64_t frames,
                              int32_t channels, float* data);
void vst3go_ara_audio_reader_free(VST3GoARAController* c, void* reader);
void* vst3go_ara_context_reader_new(VST3GoARAController* c, uintptr_t contextHostRef, int32_t contentType);
int32_t vst3go_ara_host_reader_count(VST3GoARAController* c, void* reader);
const void* vst3go_ara_host_reader_event(VST3GoARAController* c, void* reader, int32_t index);
void vst3go_ara_host_reader_free(VST3GoARAController* c, void* reader);
void vst3go_ara_notify_progress(VST3GoARAController* c, uintptr_t sourceHostRef, int32_t state, float value);
void vst3go_ara_notify_content(VST3GoARAController* c, int32_t kind, uintptr_t hostRef, int32_t flags);
void vst3go_ara_notify_document(VST3GoARAController* c);
size_t vst3go_ara_archive_size(VST3GoARAController* c, void* reader);
ARABool vst3go_ara_archive_read(VST3GoARAController* c, void* reader, size_t position, size_t length, void* buffer);
ARABool vst3go_ara_archive_write(VST3GoARAController* c, void* writer, size_t position, size_t length, void* buffer);

// Go side (ara.go).
extern uintptr_t GoARACreateController(int32_t slot, VST3GoARAController* c, char* name);
extern void GoARADestroyController(uintptr_t controller);
extern void GoARABeginEditing(uintptr_t controller);
extern void GoARAEndEditing(uintptr_t controller);
extern void GoARANotifyModelUpdates(uintptr_t controller);
extern ARABool GoARARestore(uintptr_t controller, void* reader, VST3GoARARestoreFilter* filter);
extern ARABool GoARAStore(uintptr_t controller, void* writer, VST3GoARAStoreFilter* filter);
extern ARABool GoARAStoreChunk(uintptr_t controller, void* writer, uintptr_t sourceRef);
extern int32_t GoARAAlgorithm(uintptr_t controller, uintptr_t sourceRef);
extern void GoARARequestAlgorithm(uintptr_t controller, uintptr_t sourceRef, int32_t index);
extern void GoARAUpdateDocument(uintptr_t controller, char* name);
extern uintptr_t GoARACreateContext(uintptr_t controller, uintptr_t hostRef, VST3GoARAContextProps* props);
extern void GoARAUpdateContext(uintptr_t controller, uintptr_t ref, VST3GoARAContextProps* props);
extern void GoARAUpdateContextContent(uintptr_t controller, uintptr_t ref);
extern uintptr_t GoARACreateSequence(uintptr_t controller, uintptr_t hostRef, VST3GoARASequenceProps* props);
extern void GoARAUpdateSequence(uintptr_t controller, uintptr_t ref, VST3GoARASequenceProps* props);
extern uintptr_t GoARACreateSource(uintptr_t controller, uintptr_t hostRef, ARAAudioSourceProperties* props);
extern void GoARAUpdateSource(uintptr_t controller, uintptr_t ref, ARAAudioSourceProperties* props);
extern void GoARAUpdateSourceContent(uintptr_t controller, uintptr_t ref, int32_t flags);
extern void GoARAEnableSourceAccess(uintptr_t controller, uintptr_t ref, ARABool enable);
extern uintptr_t GoARACreateModification(uintptr_t controller, uintptr_t sourceRef, uintptr_t hostRef, ARAAudioModificationProperties* props);
extern uintptr_t GoARACloneModification(uintptr_t controller, uintptr_t ref, uintptr_t hostRef, ARAAudioModificationProperties* props);
extern void GoARAUpdateModification(uintptr_t controller, uintptr_t ref, ARAAudioModificationProperties* props);
extern ARABool GoARAPreservesSignal(uintptr_t controller, uintptr_t ref);
extern uintptr_t GoARACreateRegion(uintptr_t controller, uintptr_t modificationRef, uintptr_t hostRef, VST3GoARARegionProps* props);
extern void GoARAUpdateRegion(uintptr_t controller, uintptr_t ref, VST3GoARARegionProps* props);
extern void GoARAHeadAndTail(uintptr_t controller, uintptr_t ref, double* head, double* tail);
extern void GoARADeactivate(uintptr_t controller, int32_t kind, uintptr_t ref, ARABool deactivate);
extern void GoARADestroyObject(uintptr_t controller, int32_t kind, uintptr_t ref);
extern ARABool GoARAAnalysisIncomplete(uintptr_t controller, uintptr_t ref, int32_t contentType);
extern void GoARARequestAnalysis(uintptr_t controller, uintptr_t ref);
extern ARABool GoARAContentAvailable(uintptr_t controller, int32_t kind, uintptr_t ref, int32_t contentType);
extern int32_t GoARAContentGrade(uintptr_t controller, int32_t kind, uintptr_t ref, int32_t contentType);
extern VST3GoARAContentReader* GoARACreateContentReader(uintptr_t controller, int32_t kind, uintptr_t ref, int32_t contentType, ARAContentTimeRange* range);
extern void GoARARendererEdit(uintptr_t binding, int32_t op, uintptr_t ref);
extern void GoARASelection(uintptr_t binding, ARAViewSelection* selection);
extern void GoARAHideSequences(uintptr_t binding, size_t count, uintptr_t* refs);

#endif // VST3GO_ARA_H

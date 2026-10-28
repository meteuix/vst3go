// CLAP preset discovery factory and preset-load extension: one provider per
// plugin indexes the .vstpreset files of its user preset folder
// (clap_presets.go), and instances load them by path.
#include "clap_bridge.h"
#include <stdlib.h>
#include <string.h>

extern uint32_t GoClapPluginCount(void);
extern clap_preset_discovery_provider_descriptor_t* GoClapPresetProviderDescriptor(uint32_t index);
extern bool GoClapPresetProviderInit(uint32_t index, clap_preset_discovery_indexer_t* indexer);
extern bool GoClapPresetMetadata(uint32_t index, uint32_t kind, char* location, clap_preset_discovery_metadata_receiver_t* receiver);
extern bool GoClapPresetLoad(uintptr_t id, uint32_t kind, char* location, char* loadKey);

typedef struct {
    clap_preset_discovery_provider_t provider;
    const clap_preset_discovery_indexer_t* indexer;
    uint32_t index;
} VST3GoPresetProvider;

static VST3GoPresetProvider* providerOf(const clap_preset_discovery_provider_t* provider) {
    return (VST3GoPresetProvider*)provider->provider_data;
}

static bool provider_init(const clap_preset_discovery_provider_t* provider) {
    VST3GoPresetProvider* p = providerOf(provider);
    return GoClapPresetProviderInit(p->index, (clap_preset_discovery_indexer_t*)p->indexer);
}

static void provider_destroy(const clap_preset_discovery_provider_t* provider) {
    free(providerOf(provider));
}

static bool provider_get_metadata(const clap_preset_discovery_provider_t* provider, uint32_t kind, const char* location, const clap_preset_discovery_metadata_receiver_t* receiver) {
    return GoClapPresetMetadata(providerOf(provider)->index, kind, (char*)location, (clap_preset_discovery_metadata_receiver_t*)receiver);
}

static const void* provider_get_extension(const clap_preset_discovery_provider_t* provider, const char* id) {
    (void)provider; (void)id;
    return NULL;
}

static uint32_t discovery_count(const clap_preset_discovery_factory_t* factory) {
    (void)factory;
    return GoClapPluginCount();
}

static const clap_preset_discovery_provider_descriptor_t* discovery_get_descriptor(const clap_preset_discovery_factory_t* factory, uint32_t index) {
    (void)factory;
    return index < GoClapPluginCount() ? GoClapPresetProviderDescriptor(index) : NULL;
}

static const clap_preset_discovery_provider_t* discovery_create(const clap_preset_discovery_factory_t* factory, const clap_preset_discovery_indexer_t* indexer, const char* providerID) {
    (void)factory;
    if (!indexer || !providerID) {
        return NULL;
    }
    for (uint32_t i = 0; i < GoClapPluginCount(); i++) {
        const clap_preset_discovery_provider_descriptor_t* desc = GoClapPresetProviderDescriptor(i);
        if (!desc || strcmp(desc->id, providerID) != 0) {
            continue;
        }
        VST3GoPresetProvider* p = (VST3GoPresetProvider*)calloc(1, sizeof(VST3GoPresetProvider));
        if (!p) {
            return NULL;
        }
        p->provider.desc = desc;
        p->provider.provider_data = p;
        p->provider.init = provider_init;
        p->provider.destroy = provider_destroy;
        p->provider.get_metadata = provider_get_metadata;
        p->provider.get_extension = provider_get_extension;
        p->indexer = indexer;
        p->index = i;
        return &p->provider;
    }
    return NULL;
}

static const clap_preset_discovery_factory_t discoveryFactory = {
    discovery_count, discovery_get_descriptor, discovery_create
};

const void* vst3go_clap_preset_discovery_factory(void) { return &discoveryFactory; }

// Indexer and metadata receiver calls for Go, which cannot call C function
// pointers.

bool vst3go_clap_declare_filetype(clap_preset_discovery_indexer_t* indexer, const char* name, const char* extension) {
    clap_preset_discovery_filetype_t filetype = { name, NULL, extension };
    return indexer->declare_filetype(indexer, &filetype);
}

bool vst3go_clap_declare_location(clap_preset_discovery_indexer_t* indexer, uint32_t flags, const char* name, const char* location) {
    clap_preset_discovery_location_t loc = { flags, name, CLAP_PRESET_DISCOVERY_LOCATION_FILE, location };
    return indexer->declare_location(indexer, &loc);
}

void vst3go_clap_metadata_error(clap_preset_discovery_metadata_receiver_t* r, const char* message) {
    r->on_error(r, 0, message);
}

bool vst3go_clap_metadata_begin(clap_preset_discovery_metadata_receiver_t* r, const char* name) {
    return r->begin_preset(r, name, NULL);
}

void vst3go_clap_metadata_plugin_id(clap_preset_discovery_metadata_receiver_t* r, const char* abi, const char* id) {
    clap_universal_plugin_id_t pluginID = { abi, id };
    r->add_plugin_id(r, &pluginID);
}

void vst3go_clap_metadata_flags(clap_preset_discovery_metadata_receiver_t* r, uint32_t flags) {
    r->set_flags(r, flags);
}

void vst3go_clap_metadata_timestamps(clap_preset_discovery_metadata_receiver_t* r, uint64_t modified) {
    r->set_timestamps(r, CLAP_TIMESTAMP_UNKNOWN, modified);
}

void vst3go_clap_metadata_feature(clap_preset_discovery_metadata_receiver_t* r, const char* feature) {
    r->add_feature(r, feature);
}

void vst3go_clap_metadata_extra(clap_preset_discovery_metadata_receiver_t* r, const char* key, const char* value) {
    r->add_extra_info(r, key, value);
}

// preset-load

static bool preset_load_from_location(const clap_plugin_t* plugin, uint32_t kind, const char* location, const char* loadKey) {
    VST3GoClapPlugin* p = (VST3GoClapPlugin*)plugin->plugin_data;
    return GoClapPresetLoad(p->id, kind, (char*)location, (char*)loadKey);
}

static const clap_plugin_preset_load_t extPresetLoad = { preset_load_from_location };

const void* vst3go_clap_preset_load_extension(void) { return &extPresetLoad; }

void vst3go_clap_preset_loaded(VST3GoClapPlugin* p, uint32_t kind, const char* location, const char* loadKey) {
    if (p->hostPresetLoad) {
        p->hostPresetLoad->loaded(p->host, kind, location, loadKey);
    }
}

void vst3go_clap_preset_error(VST3GoClapPlugin* p, uint32_t kind, const char* location, const char* loadKey, const char* message) {
    if (p->hostPresetLoad) {
        p->hostPresetLoad->on_error(p->host, kind, location, loadKey, 0, message);
    }
}

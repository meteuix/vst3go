//go:build standalone && darwin

#include <CoreMIDI/CoreMIDI.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <pthread.h>
#include <dispatch/dispatch.h>

#include "_cgo_export.h"
#include "midiport.h"

// MIDIPacketList input and output still work on current macOS; the UMP
// (MIDIEventList) API would add nothing for 1.0 short messages.
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

static MIDIClientRef client;

typedef struct {
    MIDIPortRef port;
    MIDIEndpointRef endpoint;  // virtual input or output destination
} Port;

static CFStringRef cfstring(const char* s) {
    return CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
}

static int status_code(OSStatus s) { return -(int)(s < 0 ? -s : s ? s : 1); }

// The client lives on a thread running its own run loop: Core MIDI
// updates the client's view of other applications' ports (sources and
// destinations appearing) through run loop notifications.
static struct {
    const char* name;
    OSStatus status;
    dispatch_semaphore_t ready;
} start;

static void* client_thread(void* arg) {
    (void)arg;
    CFStringRef n = cfstring(start.name);
    OSStatus s = MIDIClientCreate(n, NULL, NULL, &client);
    // The MIDI server may still be starting (kMIDIServerStartErr).
    for (int i = 0; i < 10 && s == kMIDIServerStartErr; i++) {
        usleep(100000);
        s = MIDIClientCreate(n, NULL, NULL, &client);
    }
    CFRelease(n);
    start.status = s;
    dispatch_semaphore_signal(start.ready);
    if (s == noErr) CFRunLoopRun();
    return NULL;
}

int vst3go_midi_init(const char* name) {
    start.name = name;
    start.ready = dispatch_semaphore_create(0);
    pthread_t thread;
    if (pthread_create(&thread, NULL, client_thread, NULL) != 0) return -1;
    pthread_detach(thread);
    dispatch_semaphore_wait(start.ready, DISPATCH_TIME_FOREVER);
    return start.status == noErr ? 0 : status_code(start.status);
}

const char* vst3go_midi_error(int code) {
    static char buf[64];
    if (code == VST3GO_MIDI_UNSUPPORTED) return "not supported";
    snprintf(buf, sizeof(buf), "Core MIDI error %d", -code);
    return buf;
}

int vst3go_midi_count(bool input) {
    return (int)(input ? MIDIGetNumberOfSources() : MIDIGetNumberOfDestinations());
}

int vst3go_midi_name(bool input, int index, char* name, int size) {
    MIDIEndpointRef e = input ? MIDIGetSource(index) : MIDIGetDestination(index);
    CFStringRef s = NULL;
    if (!e || MIDIObjectGetStringProperty(e, kMIDIPropertyDisplayName, &s) != noErr || !s) return -1;
    bool ok = CFStringGetCString(s, name, size, kCFStringEncodingUTF8);
    CFRelease(s);
    return ok ? 0 : -1;
}

static void read_proc(const MIDIPacketList* list, void* id, void* source) {
    (void)source;
    const MIDIPacket* p = &list->packet[0];
    for (UInt32 i = 0; i < list->numPackets; i++) {
        goMIDIPortReceive((uintptr_t)id, (uint8_t*)p->data, (int)p->length);
        p = MIDIPacketNext(p);
    }
}

int vst3go_midi_open_input(int index, uintptr_t id, void** handle) {
    MIDIEndpointRef source = MIDIGetSource(index);
    if (!source) return status_code(kMIDIObjectNotFound);
    Port* port = calloc(1, sizeof(Port));
    if (!port) return -1;
    CFStringRef name = cfstring("input");
    OSStatus s = MIDIInputPortCreate(client, name, read_proc, (void*)id, &port->port);
    CFRelease(name);
    if (s == noErr) s = MIDIPortConnectSource(port->port, source, NULL);
    if (s != noErr) {
        if (port->port) MIDIPortDispose(port->port);
        free(port);
        return status_code(s);
    }
    *handle = port;
    return 0;
}

int vst3go_midi_open_virtual_input(const char* name, uintptr_t id, void** handle) {
    Port* port = calloc(1, sizeof(Port));
    if (!port) return -1;
    CFStringRef n = cfstring(name);
    OSStatus s = MIDIDestinationCreate(client, n, read_proc, (void*)id, &port->endpoint);
    CFRelease(n);
    if (s != noErr) {
        free(port);
        return status_code(s);
    }
    *handle = port;
    return 0;
}

void vst3go_midi_close_input(void* handle) {
    Port* port = handle;
    if (port->port) MIDIPortDispose(port->port);
    if (port->endpoint) MIDIEndpointDispose(port->endpoint);
    free(port);
}

int vst3go_midi_open_output(int index, void** handle) {
    MIDIEndpointRef dest = MIDIGetDestination(index);
    if (!dest) return status_code(kMIDIObjectNotFound);
    Port* port = calloc(1, sizeof(Port));
    if (!port) return -1;
    CFStringRef name = cfstring("output");
    OSStatus s = MIDIOutputPortCreate(client, name, &port->port);
    CFRelease(name);
    if (s != noErr) {
        free(port);
        return status_code(s);
    }
    port->endpoint = dest;
    *handle = port;
    return 0;
}

int vst3go_midi_send(void* handle, const uint8_t* data, int size) {
    Port* port = handle;
    Byte buffer[64];
    MIDIPacketList* list = (MIDIPacketList*)buffer;
    MIDIPacket* p = MIDIPacketListInit(list);
    p = MIDIPacketListAdd(list, sizeof(buffer), p, 0, (ByteCount)size, data);
    if (!p) return -1;
    OSStatus s = MIDISend(port->port, port->endpoint, list);
    return s == noErr ? 0 : status_code(s);
}

void vst3go_midi_close_output(void* handle) {
    Port* port = handle;
    MIDIPortDispose(port->port);
    free(port);
}

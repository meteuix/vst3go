//go:build standalone && linux

#include <alsa/asoundlib.h>
#include <poll.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "_cgo_export.h"
#include "midiport.h"

// One sequencer client for listing; every open port gets its own client
// handle, so an input's thread owns its event queue.
static snd_seq_t* seq;
static char clientName[64];

// ALSA prints its errors to stderr by default; they reach Go as codes.
static void quiet(const char* file, int line, const char* function, int err, const char* fmt, ...) {
    (void)file; (void)line; (void)function; (void)err; (void)fmt;
}

int vst3go_midi_init(const char* name) {
    snd_lib_error_set_handler(quiet);
    snprintf(clientName, sizeof(clientName), "%s", name);
    int err = snd_seq_open(&seq, "default", SND_SEQ_OPEN_DUPLEX, 0);
    if (err < 0) return err;
    snd_seq_set_client_name(seq, clientName);
    return 0;
}

const char* vst3go_midi_error(int code) {
    if (code == VST3GO_MIDI_UNSUPPORTED) return "not supported";
    return snd_strerror(code);
}

// port_at finds the index-th port other clients offer as a source (input)
// or destination (output).
static bool port_at(bool input, int index, snd_seq_addr_t* addr, char* name, int size) {
    unsigned int caps = input ? SND_SEQ_PORT_CAP_READ | SND_SEQ_PORT_CAP_SUBS_READ
                              : SND_SEQ_PORT_CAP_WRITE | SND_SEQ_PORT_CAP_SUBS_WRITE;
    snd_seq_client_info_t* client;
    snd_seq_port_info_t* port;
    snd_seq_client_info_alloca(&client);
    snd_seq_port_info_alloca(&port);
    snd_seq_client_info_set_client(client, -1);
    int self = snd_seq_client_id(seq);
    while (snd_seq_query_next_client(seq, client) >= 0) {
        int id = snd_seq_client_info_get_client(client);
        if (id == self || id == SND_SEQ_CLIENT_SYSTEM) continue;
        snd_seq_port_info_set_client(port, id);
        snd_seq_port_info_set_port(port, -1);
        while (snd_seq_query_next_port(seq, port) >= 0) {
            if ((snd_seq_port_info_get_capability(port) & caps) != caps) continue;
            if (index-- > 0) continue;
            addr->client = (unsigned char)id;
            addr->port = (unsigned char)snd_seq_port_info_get_port(port);
            if (name) {
                snprintf(name, (size_t)size, "%s:%s", snd_seq_client_info_get_name(client), snd_seq_port_info_get_name(port));
            }
            return true;
        }
    }
    return false;
}

int vst3go_midi_count(bool input) {
    int n = 0;
    snd_seq_addr_t addr;
    while (port_at(input, n, &addr, NULL, 0)) n++;
    return n;
}

int vst3go_midi_name(bool input, int index, char* name, int size) {
    snd_seq_addr_t addr;
    return port_at(input, index, &addr, name, size) ? 0 : -ENOENT;
}

typedef struct {
    snd_seq_t* seq;
    int port;
    snd_midi_event_t* coder;
    pthread_t thread;
    int stop[2];  // pipe that wakes the input thread to quit
    uintptr_t id;
    snd_seq_addr_t peer;
} Port;

static void close_port(Port* p) {
    if (p->coder) snd_midi_event_free(p->coder);
    if (p->seq) snd_seq_close(p->seq);
    if (p->stop[0] > 0) close(p->stop[0]);
    if (p->stop[1] > 0) close(p->stop[1]);
    free(p);
}

static int new_port(Port** out, const char* name, int mode, unsigned int caps) {
    Port* p = calloc(1, sizeof(Port));
    if (!p) return -ENOMEM;
    int err = snd_seq_open(&p->seq, "default", mode, 0);
    if (err >= 0) {
        snd_seq_set_client_name(p->seq, name);
        err = snd_seq_create_simple_port(p->seq, name, caps, SND_SEQ_PORT_TYPE_MIDI_GENERIC | SND_SEQ_PORT_TYPE_APPLICATION);
        p->port = err;
    }
    if (err >= 0) err = snd_midi_event_new(256, &p->coder);
    if (err < 0) {
        close_port(p);
        return err;
    }
    *out = p;
    return 0;
}

static void* input_thread(void* arg) {
    Port* p = arg;
    int count = snd_seq_poll_descriptors_count(p->seq, POLLIN);
    struct pollfd* fds = calloc((size_t)count + 1, sizeof(struct pollfd));
    if (!fds) return NULL;
    snd_seq_poll_descriptors(p->seq, fds, (unsigned int)count, POLLIN);
    fds[count] = (struct pollfd){ p->stop[0], POLLIN, 0 };
    snd_midi_event_no_status(p->coder, 1);
    unsigned char buf[256];
    for (;;) {
        if (poll(fds, (nfds_t)count + 1, -1) < 0) continue;
        if (fds[count].revents) break;
        snd_seq_event_t* ev;
        while (snd_seq_event_input(p->seq, &ev) >= 0 && ev) {
            long n = snd_midi_event_decode(p->coder, buf, sizeof(buf), ev);
            if (n > 0) goMIDIPortReceive(p->id, buf, (int)n);
            if (snd_seq_event_input_pending(p->seq, 0) <= 0) break;
        }
    }
    free(fds);
    return NULL;
}

static int start_input(Port* p, uintptr_t id, void** handle) {
    p->id = id;
    snd_seq_nonblock(p->seq, 1);
    if (pipe(p->stop) < 0 || pthread_create(&p->thread, NULL, input_thread, p) != 0) {
        close_port(p);
        return -errno;
    }
    *handle = p;
    return 0;
}

int vst3go_midi_open_input(int index, uintptr_t id, void** handle) {
    snd_seq_addr_t source;
    if (!port_at(true, index, &source, NULL, 0)) return -ENOENT;
    Port* p;
    int err = new_port(&p, clientName, SND_SEQ_OPEN_INPUT, SND_SEQ_PORT_CAP_WRITE | SND_SEQ_PORT_CAP_SUBS_WRITE);
    if (err < 0) return err;
    if ((err = snd_seq_connect_from(p->seq, p->port, source.client, source.port)) < 0) {
        close_port(p);
        return err;
    }
    return start_input(p, id, handle);
}

int vst3go_midi_open_virtual_input(const char* name, uintptr_t id, void** handle) {
    Port* p;
    int err = new_port(&p, name, SND_SEQ_OPEN_INPUT, SND_SEQ_PORT_CAP_WRITE | SND_SEQ_PORT_CAP_SUBS_WRITE);
    if (err < 0) return err;
    return start_input(p, id, handle);
}

void vst3go_midi_close_input(void* handle) {
    Port* p = handle;
    if (write(p->stop[1], "q", 1) == 1) pthread_join(p->thread, NULL);
    close_port(p);
}

int vst3go_midi_open_output(int index, void** handle) {
    snd_seq_addr_t dest;
    if (!port_at(false, index, &dest, NULL, 0)) return -ENOENT;
    Port* p;
    int err = new_port(&p, clientName, SND_SEQ_OPEN_OUTPUT, SND_SEQ_PORT_CAP_READ | SND_SEQ_PORT_CAP_SUBS_READ);
    if (err < 0) return err;
    if ((err = snd_seq_connect_to(p->seq, p->port, dest.client, dest.port)) < 0) {
        close_port(p);
        return err;
    }
    *handle = p;
    return 0;
}

int vst3go_midi_send(void* handle, const uint8_t* data, int size) {
    Port* p = handle;
    snd_seq_event_t ev;
    snd_seq_ev_clear(&ev);
    snd_midi_event_reset_encode(p->coder);
    long n = snd_midi_event_encode(p->coder, data, size, &ev);
    if (n < 0) return (int)n;
    if (ev.type == SND_SEQ_EVENT_NONE) return -EINVAL;
    snd_seq_ev_set_source(&ev, p->port);
    snd_seq_ev_set_subs(&ev);
    snd_seq_ev_set_direct(&ev);
    int err = snd_seq_event_output_direct(p->seq, &ev);
    return err < 0 ? err : 0;
}

void vst3go_midi_close_output(void* handle) { close_port(handle); }

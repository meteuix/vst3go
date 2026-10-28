//go:build standalone

// MIDI ports of the system MIDI API: Core MIDI (midiport_darwin.c), the
// ALSA sequencer (midiport_linux.c) and WinMM (midiport_windows.c). Errors
// are negative codes for vst3go_midi_error; received bytes go to the Go
// export goMIDIPortReceive.
#pragma once

#include <stdbool.h>
#include <stdint.h>

enum { VST3GO_MIDI_UNSUPPORTED = -100000 };

int vst3go_midi_init(const char* client_name);
const char* vst3go_midi_error(int code);

// Ports other applications offer: inputs are sources the application reads,
// outputs destinations it writes.
int vst3go_midi_count(bool input);
int vst3go_midi_name(bool input, int index, char* name, int size);

// Inputs deliver to goMIDIPortReceive(id, bytes, size) on a MIDI thread.
int vst3go_midi_open_input(int index, uintptr_t id, void** handle);
int vst3go_midi_open_virtual_input(const char* name, uintptr_t id, void** handle);
void vst3go_midi_close_input(void* handle);

int vst3go_midi_open_output(int index, void** handle);
int vst3go_midi_send(void* handle, const uint8_t* data, int size);
void vst3go_midi_close_output(void* handle);

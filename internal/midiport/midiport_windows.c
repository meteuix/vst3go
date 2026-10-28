//go:build standalone && windows

#include <windows.h>
#include <mmsystem.h>
#include <stdio.h>

#include "_cgo_export.h"
#include "midiport.h"

int vst3go_midi_init(const char* name) {
    (void)name;
    return 0;
}

const char* vst3go_midi_error(int code) {
    static char buf[128];
    if (code == VST3GO_MIDI_UNSUPPORTED) return "not supported";
    if (midiInGetErrorTextA((MMRESULT)-code, buf, sizeof(buf)) != MMSYSERR_NOERROR) {
        snprintf(buf, sizeof(buf), "WinMM error %d", -code);
    }
    return buf;
}

int vst3go_midi_count(bool input) { return (int)(input ? midiInGetNumDevs() : midiOutGetNumDevs()); }

int vst3go_midi_name(bool input, int index, char* name, int size) {
    WCHAR wide[MAXPNAMELEN];
    MMRESULT r;
    if (input) {
        MIDIINCAPSW caps;
        r = midiInGetDevCapsW((UINT_PTR)index, &caps, sizeof(caps));
        if (r == MMSYSERR_NOERROR) memcpy(wide, caps.szPname, sizeof(wide));
    } else {
        MIDIOUTCAPSW caps;
        r = midiOutGetDevCapsW((UINT_PTR)index, &caps, sizeof(caps));
        if (r == MMSYSERR_NOERROR) memcpy(wide, caps.szPname, sizeof(wide));
    }
    if (r != MMSYSERR_NOERROR) return -(int)r;
    return WideCharToMultiByte(CP_UTF8, 0, wide, -1, name, size, NULL, NULL) ? 0 : -1;
}

static int message_length(uint8_t status) {
    switch (status & 0xF0) {
    case 0xC0:
    case 0xD0:
        return 2;
    case 0xF0:
        return status == 0xF2 ? 3 : (status == 0xF1 || status == 0xF3) ? 2 : 1;
    }
    return 3;
}

static void CALLBACK input_proc(HMIDIIN h, UINT msg, DWORD_PTR id, DWORD_PTR p1, DWORD_PTR p2) {
    (void)h;
    (void)p2;
    if (msg != MIM_DATA) return;
    uint8_t bytes[3] = { (uint8_t)(p1 & 0xFF), (uint8_t)((p1 >> 8) & 0xFF), (uint8_t)((p1 >> 16) & 0xFF) };
    goMIDIPortReceive((uintptr_t)id, bytes, message_length(bytes[0]));
}

int vst3go_midi_open_input(int index, uintptr_t id, void** handle) {
    HMIDIIN h;
    MMRESULT r = midiInOpen(&h, (UINT)index, (DWORD_PTR)input_proc, (DWORD_PTR)id, CALLBACK_FUNCTION);
    if (r == MMSYSERR_NOERROR) r = midiInStart(h);
    if (r != MMSYSERR_NOERROR) return -(int)r;
    *handle = h;
    return 0;
}

// WinMM has no virtual ports.
int vst3go_midi_open_virtual_input(const char* name, uintptr_t id, void** handle) {
    (void)name;
    (void)id;
    (void)handle;
    return VST3GO_MIDI_UNSUPPORTED;
}

void vst3go_midi_close_input(void* handle) {
    HMIDIIN h = handle;
    midiInStop(h);
    midiInReset(h);
    midiInClose(h);
}

int vst3go_midi_open_output(int index, void** handle) {
    HMIDIOUT h;
    MMRESULT r = midiOutOpen(&h, (UINT)index, 0, 0, CALLBACK_NULL);
    if (r != MMSYSERR_NOERROR) return -(int)r;
    *handle = h;
    return 0;
}

int vst3go_midi_send(void* handle, const uint8_t* data, int size) {
    DWORD packed = 0;
    for (int i = 0; i < size && i < 3; i++) packed |= (DWORD)data[i] << (8 * i);
    MMRESULT r = midiOutShortMsg((HMIDIOUT)handle, packed);
    return r == MMSYSERR_NOERROR ? 0 : -(int)r;
}

void vst3go_midi_close_output(void* handle) {
    midiOutReset((HMIDIOUT)handle);
    midiOutClose((HMIDIOUT)handle);
}

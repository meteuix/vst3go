// aucheck drives an installed vst3go Audio Unit, or a registered AUv3
// extension, the way an AU host does, beyond what auval covers: parameter
// text, ClassInfo round trips, MIDI in and out, factory presets, latency,
// side chains, the deadline guard, and the Cocoa editor (Audio Units) with
// edits flowing from the page to AU parameter listeners and back.
//
// usage: aucheck <type> <subtype> <manufacturer> effect|instrument|midifx|sidechain|stall
#import <AudioToolbox/AudioToolbox.h>
#import <AudioToolbox/AUCocoaUIView.h>
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#define CHECK(cond, ...)                                                      \
    do {                                                                      \
        if (!(cond)) {                                                        \
            fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__);              \
            fprintf(stderr, __VA_ARGS__);                                     \
            fprintf(stderr, "\n");                                            \
            exit(1);                                                          \
        }                                                                     \
    } while (0)
#define OK(status, what) CHECK((status) == noErr, "%s: %d", what, (int)(status))

enum { kFrames = 512, kProgramParam = 0x7EFFFFFF };
static const Float64 kRate = 48000;

static AudioUnit unit;
static bool v3;  // an AUv3 extension, reached through the v2 API bridge
static float inputLevel = 0.5f;
static int midiOutNotes, midiOutKey = -1;
static int gestures, valueChanges;
static AudioUnitParameterID lastChanged;

static OSType fourCC(const char* s) {
    CHECK(strlen(s) == 4, "four character code: %s", s);
    return (OSType)s[0] << 24 | (OSType)s[1] << 16 | (OSType)s[2] << 8 | (OSType)s[3];
}

static void run(double seconds) {
    NSDate* until = [NSDate dateWithTimeIntervalSinceNow:seconds];
    while ([until timeIntervalSinceNow] > 0) {
        [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
    }
}

static OSStatus inputCallback(void* ref, AudioUnitRenderActionFlags* flags, const AudioTimeStamp* ts, UInt32 bus, UInt32 frames, AudioBufferList* io) {
    (void)ref; (void)flags; (void)ts; (void)bus;
    for (UInt32 b = 0; b < io->mNumberBuffers; b++) {
        float* data = (float*)io->mBuffers[b].mData;
        for (UInt32 i = 0; i < frames; i++) data[i] = inputLevel;
    }
    return noErr;
}

static OSStatus midiOutput(void* ref, const AudioTimeStamp* ts, UInt32 output, const MIDIPacketList* list) {
    (void)ref; (void)ts; (void)output;
    const MIDIPacket* packet = &list->packet[0];
    for (UInt32 i = 0; i < list->numPackets; i++) {
        if (packet->length >= 3 && (packet->data[0] & 0xF0) == 0x90) {
            midiOutNotes++;
            midiOutKey = packet->data[1];
        }
        packet = MIDIPacketNext(packet);
    }
    return noErr;
}

// MIDI 2.0 note-ons from the event list callback.
static int ump2Notes, ump2Key = -1;
static uint32_t ump2Velocity;

static void sendUMP(UInt32 offset, MIDIProtocolID protocol, const uint32_t* words, UInt32 count) {
    Byte buffer[256];
    MIDIEventList* list = (MIDIEventList*)buffer;
    MIDIEventPacket* packet = MIDIEventListInit(list, protocol);
    packet = MIDIEventListAdd(list, sizeof(buffer), packet, 0, count, words);
    CHECK(packet, "event list");
    OK(MusicDeviceMIDIEventList(unit, offset, list), "MIDI event list");
}

static Float64 sampleTime;

static void setChannels(AudioUnitScope scope, UInt32 count) {
    AudioStreamBasicDescription format;
    UInt32 size = sizeof(format);
    OK(AudioUnitGetProperty(unit, kAudioUnitProperty_StreamFormat, scope, 0, &format, &size), "stream format");
    format.mChannelsPerFrame = count;
    OK(AudioUnitSetProperty(unit, kAudioUnitProperty_StreamFormat, scope, 0, &format, sizeof(format)), "set stream format");
}

static double nowMs(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec * 1e3 + t.tv_nsec / 1e6;
}

// render runs one block and returns the peak of output bus 0.
static float render(UInt32 outChannels) {
    AudioBufferList* list = (AudioBufferList*)calloc(1, sizeof(AudioBufferList) + sizeof(AudioBuffer) * 8);
    list->mNumberBuffers = outChannels;
    for (UInt32 b = 0; b < outChannels; b++) list->mBuffers[b] = (AudioBuffer){ 1, kFrames * sizeof(float), NULL };
    AudioTimeStamp ts = { 0 };
    ts.mSampleTime = sampleTime;
    ts.mFlags = kAudioTimeStampSampleTimeValid;
    AudioUnitRenderActionFlags flags = 0;
    OK(AudioUnitRender(unit, &flags, &ts, 0, kFrames, list), "render");
    sampleTime += kFrames;
    float peak = 0;
    for (UInt32 b = 0; b < outChannels; b++) {
        const float* data = (const float*)list->mBuffers[b].mData;
        CHECK(data, "rendered buffer %u", (unsigned)b);
        for (UInt32 i = 0; i < kFrames; i++) peak = fmaxf(peak, fabsf(data[i]));
    }
    free(list);
    return peak;
}

static UInt32 elementCount(AudioUnitScope scope) {
    UInt32 count = 0, size = sizeof(count);
    OK(AudioUnitGetProperty(unit, kAudioUnitProperty_ElementCount, scope, 0, &count, &size), "element count");
    return count;
}

static UInt32 channels(AudioUnitScope scope, UInt32 element) {
    AudioStreamBasicDescription format;
    UInt32 size = sizeof(format);
    OK(AudioUnitGetProperty(unit, kAudioUnitProperty_StreamFormat, scope, element, &format, &size), "stream format");
    return format.mChannelsPerFrame;
}

static AudioUnitParameterInfo paramInfo(AudioUnitParameterID id) {
    AudioUnitParameterInfo info;
    UInt32 size = sizeof(info);
    OK(AudioUnitGetProperty(unit, kAudioUnitProperty_ParameterInfo, kAudioUnitScope_Global, id, &info, &size), "parameter info");
    return info;
}

static float getParam(AudioUnitParameterID id) {
    AudioUnitParameterValue value = 0;
    OK(AudioUnitGetParameter(unit, id, kAudioUnitScope_Global, 0, &value), "get parameter");
    return value;
}

static void setParam(AudioUnitParameterID id, float value) {
    OK(AudioUnitSetParameter(unit, id, kAudioUnitScope_Global, 0, value, 0), "set parameter");
}

// params returns the parameter list and the first continuous parameter.
static NSArray<NSNumber*>* params(AudioUnitParameterID* continuous) {
    UInt32 size = 0;
    Boolean writable;
    OK(AudioUnitGetPropertyInfo(unit, kAudioUnitProperty_ParameterList, kAudioUnitScope_Global, 0, &size, &writable), "parameter list size");
    UInt32 count = size / sizeof(AudioUnitParameterID);
    AudioUnitParameterID* ids = (AudioUnitParameterID*)calloc(count + 1, sizeof(AudioUnitParameterID));
    OK(AudioUnitGetProperty(unit, kAudioUnitProperty_ParameterList, kAudioUnitScope_Global, 0, ids, &size), "parameter list");
    NSMutableArray* list = [NSMutableArray array];
    *continuous = UINT32_MAX;
    for (UInt32 i = 0; i < count; i++) {
        AudioUnitParameterInfo info = paramInfo(ids[i]);
        CHECK(info.cfNameString && CFStringGetLength(info.cfNameString) > 0, "parameter %u has a name", (unsigned)ids[i]);
        CHECK((info.flags & kAudioUnitParameterFlag_IsReadable) && (info.flags & kAudioUnitParameterFlag_IsWritable), "parameter %u readable and writable", (unsigned)ids[i]);
        if (info.flags & kAudioUnitParameterFlag_CFNameRelease) CFRelease(info.cfNameString);
        if (*continuous == UINT32_MAX && info.unit != kAudioUnitParameterUnit_Indexed && info.unit != kAudioUnitParameterUnit_Boolean) *continuous = ids[i];
        [list addObject:@(ids[i])];
    }
    free(ids);
    return list;
}

static AudioUnitParameterID findParam(NSArray<NSNumber*>* list, NSString* name) {
    for (NSNumber* n in list) {
        AudioUnitParameterInfo info = paramInfo(n.unsignedIntValue);
        bool match = [(__bridge NSString*)info.cfNameString isEqualToString:name];
        if (info.flags & kAudioUnitParameterFlag_CFNameRelease) CFRelease(info.cfNameString);
        if (match) return n.unsignedIntValue;
    }
    CHECK(0, "no parameter %s", name.UTF8String);
    return 0;
}

static void checkText(AudioUnitParameterID id) {
    AudioUnitParameterInfo info = paramInfo(id);
    if (info.flags & kAudioUnitParameterFlag_CFNameRelease) CFRelease(info.cfNameString);
    AudioUnitParameterValue value = info.minValue + (info.maxValue - info.minValue) * 0.5f;
    AudioUnitParameterStringFromValue toText = { id, &value, NULL };
    UInt32 size = sizeof(toText);
    OK(AudioUnitGetProperty(unit, kAudioUnitProperty_ParameterStringFromValue, kAudioUnitScope_Global, 0, &toText, &size), "string from value");
    CHECK(toText.outString && CFStringGetLength(toText.outString) > 0, "parameter text");
    AudioUnitParameterValueFromString fromText = { id, toText.outString, 0 };
    size = sizeof(fromText);
    OK(AudioUnitGetProperty(unit, kAudioUnitProperty_ParameterValueFromString, kAudioUnitScope_Global, 0, &fromText, &size), "value from string");
    float tolerance = (info.maxValue - info.minValue) * 0.02f;
    CHECK(fabsf(fromText.outValue - value) <= tolerance, "text round trip %g -> %s -> %g", value, [(__bridge NSString*)toText.outString UTF8String], fromText.outValue);
    CFRelease(toText.outString);
}

// checkClassInfo saves the state with one value, changes it and restores.
static void checkClassInfo(AudioUnitParameterID id) {
    AudioUnitParameterInfo info = paramInfo(id);
    if (info.flags & kAudioUnitParameterFlag_CFNameRelease) CFRelease(info.cfNameString);
    float saved = info.minValue + (info.maxValue - info.minValue) * 0.3f;
    setParam(id, saved);
    CFPropertyListRef state = NULL;
    UInt32 size = sizeof(state);
    OK(AudioUnitGetProperty(unit, kAudioUnitProperty_ClassInfo, kAudioUnitScope_Global, 0, &state, &size), "get ClassInfo");
    CHECK(state && CFGetTypeID(state) == CFDictionaryGetTypeID() && CFDictionaryGetValue((CFDictionaryRef)state, CFSTR(kAUPresetDataKey)), "ClassInfo holds plugin data");
    setParam(id, info.maxValue);
    OK(AudioUnitSetProperty(unit, kAudioUnitProperty_ClassInfo, kAudioUnitScope_Global, 0, &state, sizeof(state)), "set ClassInfo");
    float tolerance = (info.maxValue - info.minValue) * 1e-4f;
    CHECK(fabsf(getParam(id) - saved) <= tolerance, "ClassInfo restored parameter %u: %g, want %g", (unsigned)id, getParam(id), saved);
    setParam(id, info.defaultValue);

    CFMutableDictionaryRef other = CFDictionaryCreateMutableCopy(NULL, 0, (CFDictionaryRef)state);
    SInt32 subtype = 0x7a7a7a7a;
    CFNumberRef number = CFNumberCreate(NULL, kCFNumberSInt32Type, &subtype);
    CFDictionarySetValue(other, CFSTR(kAUPresetSubtypeKey), number);
    // The bridge to an AUv3 extension sets its fullState, which has no
    // error to return; the state must still not be applied.
    setParam(id, info.maxValue);
    OSStatus status = AudioUnitSetProperty(unit, kAudioUnitProperty_ClassInfo, kAudioUnitScope_Global, 0, &other, sizeof(other));
    CHECK(v3 || status != noErr, "ClassInfo of another component rejected");
    CHECK(getParam(id) == info.maxValue, "ClassInfo of another component applied: %g", getParam(id));
    setParam(id, info.defaultValue);
    CFRelease(number);
    CFRelease(other);
    CFRelease(state);
}

static void setup(void) {
    for (UInt32 i = 0; i < elementCount(kAudioUnitScope_Input); i++) {
        Float64 rate = kRate;
        OK(AudioUnitSetProperty(unit, kAudioUnitProperty_SampleRate, kAudioUnitScope_Input, i, &rate, sizeof(rate)), "input sample rate");
        AURenderCallbackStruct callback = { inputCallback, NULL };
        OK(AudioUnitSetProperty(unit, kAudioUnitProperty_SetRenderCallback, kAudioUnitScope_Input, i, &callback, sizeof(callback)), "render callback");
    }
    for (UInt32 i = 0; i < elementCount(kAudioUnitScope_Output); i++) {
        Float64 rate = kRate;
        OK(AudioUnitSetProperty(unit, kAudioUnitProperty_SampleRate, kAudioUnitScope_Output, i, &rate, sizeof(rate)), "output sample rate");
    }
    UInt32 frames = kFrames;
    OK(AudioUnitSetProperty(unit, kAudioUnitProperty_MaximumFramesPerSlice, kAudioUnitScope_Global, 0, &frames, sizeof(frames)), "max frames");
}

// ---- editor ----

static void eventListener(void* ref, void* object, const AudioUnitEvent* event, UInt64 time, AudioUnitParameterValue value) {
    (void)ref; (void)object; (void)time; (void)value;
    switch (event->mEventType) {
    case kAudioUnitEvent_BeginParameterChangeGesture:
    case kAudioUnitEvent_EndParameterChangeGesture:
        gestures++;
        break;
    case kAudioUnitEvent_ParameterValueChange:
        valueChanges++;
        lastChanged = event->mArgument.mParameter.mParameterID;
        break;
    default:
        break;
    }
}

static WKWebView* findWebView(NSView* view) {
    if ([view isKindOfClass:[WKWebView class]]) return (WKWebView*)view;
    for (NSView* child in view.subviews) {
        WKWebView* found = findWebView(child);
        if (found) return found;
    }
    return nil;
}

static NSString* eval(WKWebView* web, NSString* script) {
    __block NSString* result = nil;
    __block BOOL done = NO;
    [web evaluateJavaScript:script completionHandler:^(id value, NSError* error) {
        result = error ? [NSString stringWithFormat:@"error: %@", error.localizedDescription] : [NSString stringWithFormat:@"%@", value];
        done = YES;
    }];
    for (int i = 0; i < 300 && !done; i++) run(0.01);
    return result;
}

static bool waitFor(WKWebView* web, NSString* script, UInt32 outChannels) {
    for (int i = 0; i < 100; i++) {
        if ([eval(web, script) isEqualToString:@"true"]) return true;
        render(outChannels);
        run(0.02);
    }
    return false;
}

static void checkEditor(bool custom, UInt32 outChannels) {
    AudioUnitCocoaViewInfo info;
    UInt32 size = sizeof(info);
    OK(AudioUnitGetProperty(unit, kAudioUnitProperty_CocoaUI, kAudioUnitScope_Global, 0, &info, &size), "CocoaUI");
    NSBundle* bundle = [NSBundle bundleWithURL:(__bridge NSURL*)info.mCocoaAUViewBundleLocation];
    NSString* className = (__bridge NSString*)info.mCocoaAUViewClass[0];
    Class factoryClass = [bundle classNamed:className];
    CHECK(factoryClass && [factoryClass conformsToProtocol:@protocol(AUCocoaUIBase)], "view factory %s in %s", className.UTF8String, bundle.bundlePath.UTF8String);
    id<AUCocoaUIBase> factory = [[factoryClass alloc] init];
    NSView* view = [factory uiViewForAudioUnit:unit withSize:NSMakeSize(400, 300)];
    CFRelease(info.mCocoaAUViewBundleLocation);
    CFRelease(info.mCocoaAUViewClass[0]);
    CHECK(view && view.frame.size.width > 0 && view.frame.size.height > 0, "editor view");

    NSWindow* window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 900, 640) styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:NO];
    window.releasedWhenClosed = NO;
    [window.contentView addSubview:view];
    [window orderFrontRegardless];

    WKWebView* web = nil;
    for (int i = 0; i < 100 && !web; i++) {
        run(0.05);
        WKWebView* found = findWebView(view);
        if (found && [[eval(found, @"String(typeof vst3go === 'object' && vst3go.params.length > 0)") description] isEqualToString:@"true"]) web = found;
    }
    CHECK(web, "editor page with the vst3go SDK");

    if (custom) {
        run(0.3);
        CHECK(view.frame.size.width == 720 && view.frame.size.height == 420, "page resized the editor view: %gx%g", view.frame.size.width, view.frame.size.height);
        [view setFrameSize:NSMakeSize(5000, 100)];
        CHECK(view.frame.size.width == 1200 && view.frame.size.height == 320, "host resize clamped to the editor limits: %gx%g", view.frame.size.width, view.frame.size.height);
    }

    AUEventListenerRef listener = NULL;
    OK(AUEventListenerCreate(eventListener, NULL, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode, 0, 0, &listener), "event listener");
    NSString* result = eval(web, @"(() => {"
        "const editable = (p) => !p.readOnly && !p.hidden;"
        "const p = vst3go.params.find((p) => editable(p) && p.stepCount === 0) || vst3go.params.find(editable);"
        "window.__changes = 0;"
        "vst3go.onParamChange((q) => { if (q === p) window.__changes++; });"
        "return String(p.id); })()");
    AudioUnitParameterID id = (AudioUnitParameterID)[result longLongValue];
    AudioUnitParameter param = { unit, id, kAudioUnitScope_Global, 0 };
    AudioUnitEvent event = { kAudioUnitEvent_ParameterValueChange, { .mParameter = param } };
    OK(AUEventListenerAddEventType(listener, NULL, &event), "listen to values");
    event.mEventType = kAudioUnitEvent_BeginParameterChangeGesture;
    OK(AUEventListenerAddEventType(listener, NULL, &event), "listen to begin gestures");
    event.mEventType = kAudioUnitEvent_EndParameterChangeGesture;
    OK(AUEventListenerAddEventType(listener, NULL, &event), "listen to end gestures");

    AudioUnitParameterInfo pinfo = paramInfo(id);
    if (pinfo.flags & kAudioUnitParameterFlag_CFNameRelease) CFRelease(pinfo.cfNameString);
    eval(web, [NSString stringWithFormat:@"(() => { const p = vst3go.param(%u); p.beginEdit(); p.setNormalized(0.5); p.setNormalized(0.75); p.endEdit(); return 'ok'; })()", (unsigned)id]);
    for (int i = 0; i < 50 && (gestures < 2 || valueChanges < 1); i++) {
        render(outChannels);
        run(0.02);
    }
    CHECK(gestures == 2 && valueChanges >= 1 && lastChanged == id, "page edit reached AU listeners: %d gestures, %d values", gestures, valueChanges);
    // The plugin maps normalized 0.75 to its plain range; for linear
    // parameters that is min + 0.75 * range.
    float edited = getParam(id);
    CHECK(edited > pinfo.minValue && edited < pinfo.maxValue, "page edit reached GetParameter: %g", edited);

    setParam(id, pinfo.minValue);
    NSString* check = [NSString stringWithFormat:@"String(vst3go.param(%u).normalized === 0 && window.__changes >= 3)", (unsigned)id];
    CHECK(waitFor(web, check, outChannels), "host change reached the page: %s",
          eval(web, [NSString stringWithFormat:@"JSON.stringify([vst3go.param(%u).normalized, window.__changes])", (unsigned)id]).UTF8String);

    AUListenerDispose(listener);
    [view removeFromSuperview];
    view = nil;
    factory = nil;
    run(0.2);
    CHECK(findWebView(window.contentView) == nil, "editor closed with its view");
    [window close];
}

int main(int argc, char** argv) {
    @autoreleasepool {
        CHECK(argc == 5, "usage: aucheck <type> <subtype> <manufacturer> effect|instrument|midifx|sidechain|stall");
        const char* kind = argv[4];
        bool effect = !strcmp(kind, "effect"), instrument = !strcmp(kind, "instrument"), midifx = !strcmp(kind, "midifx"), sidechain = !strcmp(kind, "sidechain"), stall = !strcmp(kind, "stall");
        CHECK(effect || instrument || midifx || sidechain || stall, "unknown kind %s", kind);
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];

        AudioComponentDescription desc = { fourCC(argv[1]), fourCC(argv[2]), fourCC(argv[3]), 0, 0 };
        AudioComponent component = AudioComponentFindNext(NULL, &desc);
        CHECK(component, "component %s %s %s installed", argv[1], argv[2], argv[3]);
        AudioComponentDescription found;
        OK(AudioComponentGetDescription(component, &found), "component description");
        v3 = (found.componentFlags & kAudioComponentFlag_IsV3AudioUnit) != 0;
        // Asynchronously, as AUv3 extensions require; Audio Units load in
        // process, extensions in their own.
        __block OSStatus status = 1;
        AudioComponentInstantiate(component, 0, ^(AudioComponentInstance instance, OSStatus err) {
            unit = instance;
            status = err;
        });
        for (int i = 0; i < 1000 && status == 1; i++) run(0.01);
        OK(status, "instantiate");

        AudioUnitParameterID continuous;
        NSArray<NSNumber*>* list = params(&continuous);
        CHECK(list.count > 0, "parameters");
        if (continuous != UINT32_MAX) {
            checkText(continuous);
            checkClassInfo(continuous);
        }

        setup();
        if (midifx) {
            SInt32 protocol = 0;
            UInt32 size = sizeof(protocol);
            OK(AudioUnitGetProperty(unit, kAudioUnitProperty_AudioUnitMIDIProtocol, kAudioUnitScope_Global, 0, &protocol, &size), "AU MIDI protocol");
            CHECK(protocol == kMIDIProtocol_2_0, "AU MIDI protocol %d", (int)protocol);
        }
        OK(AudioUnitInitialize(unit), "initialize");
        UInt32 outChannels = elementCount(kAudioUnitScope_Output) ? channels(kAudioUnitScope_Output, 0) : 0;

        if (effect) {
            AudioUnitParameterInfo info = paramInfo(continuous);
            setParam(continuous, info.minValue);
            float low = 0, high = 0;
            for (int i = 0; i < 20; i++) low = render(outChannels);
            setParam(continuous, info.maxValue);
            for (int i = 0; i < 20; i++) high = render(outChannels);
            CHECK(high > 0 && high > low * 1.5f, "parameter %u changes the output: %g -> %g", (unsigned)continuous, low, high);
            printf("effect: output %g at minimum, %g at maximum of parameter %u\n", low, high, (unsigned)continuous);

            // Channel configurations: the gain plugin runs in mono or
            // stereo, the same on both sides.
            // Sizes come from AudioUnitGetPropertyInfo: the AUv3 bridge does
            // not shrink a larger buffer size to the value's.
            AUChannelInfo infos[80];
            UInt32 size = 0;
            OK(AudioUnitGetPropertyInfo(unit, kAudioUnitProperty_SupportedNumChannels, kAudioUnitScope_Global, 0, &size, NULL), "supported channels size");
            CHECK(size <= sizeof(infos), "supported channels size %u", (unsigned)size);
            OK(AudioUnitGetProperty(unit, kAudioUnitProperty_SupportedNumChannels, kAudioUnitScope_Global, 0, infos, &size), "supported channels");
            UInt32 count = size / sizeof(AUChannelInfo);
            CHECK(count == 2 && infos[0].inChannels == 2 && infos[0].outChannels == 2 && infos[1].inChannels == 1 && infos[1].outChannels == 1,
                  "stereo and mono configurations: %u, first %d/%d", (unsigned)count, infos[0].inChannels, infos[0].outChannels);
            AudioChannelLayoutTag tags[8];
            OK(AudioUnitGetPropertyInfo(unit, kAudioUnitProperty_SupportedChannelLayoutTags, kAudioUnitScope_Output, 0, &size, NULL), "layout tags size");
            CHECK(size <= sizeof(tags), "layout tags size %u", (unsigned)size);
            OK(AudioUnitGetProperty(unit, kAudioUnitProperty_SupportedChannelLayoutTags, kAudioUnitScope_Output, 0, tags, &size), "layout tags");
            CHECK(size == 2 * sizeof(AudioChannelLayoutTag) && tags[0] == kAudioChannelLayoutTag_Mono && tags[1] == kAudioChannelLayoutTag_Stereo, "mono and stereo layouts");
            OK(AudioUnitUninitialize(unit), "uninitialize");
            setChannels(kAudioUnitScope_Output, 1);
            CHECK(AudioUnitInitialize(unit) == kAudioUnitErr_FormatNotSupported, "stereo in, mono out refused");
            setChannels(kAudioUnitScope_Input, 1);
            // Through the AUv3 bridge the layout is the bus format's, which a
            // stream format set alone leaves without one.
            if (!v3) {
                AudioChannelLayout layout;
                size = sizeof(layout);
                OK(AudioUnitGetProperty(unit, kAudioUnitProperty_AudioChannelLayout, kAudioUnitScope_Input, 0, &layout, &size), "channel layout");
                CHECK(layout.mChannelLayoutTag == kAudioChannelLayoutTag_Mono, "mono layout %#x", (unsigned)layout.mChannelLayoutTag);
            }
            OK(AudioUnitInitialize(unit), "initialize mono");
            float mono = 0;
            for (int i = 0; i < 4; i++) mono = render(1);
            CHECK(fabsf(mono - high) < 1e-5f, "mono output %g, stereo %g", mono, high);
            OK(AudioUnitUninitialize(unit), "uninitialize");
            setChannels(kAudioUnitScope_Input, 2);
            setChannels(kAudioUnitScope_Output, 2);
            OK(AudioUnitInitialize(unit), "initialize stereo");
            printf("effect: mono configuration ok\n");
        }
        if (instrument) {
            CHECK(render(outChannels) == 0, "silent without notes");
            OK(MusicDeviceMIDIEvent(unit, 0x90, 60, 100, 64), "note on");
            float peak = 0;
            for (int i = 0; i < 10; i++) peak = fmaxf(peak, render(outChannels));
            CHECK(peak > 0.01f, "note on sounds: peak %g", peak);
            printf("instrument: note on peak %g\n", peak);

            CFArrayRef presets = NULL;
            UInt32 size = sizeof(presets);
            OK(AudioUnitGetProperty(unit, kAudioUnitProperty_FactoryPresets, kAudioUnitScope_Global, 0, &presets, &size), "factory presets");
            CHECK(CFArrayGetCount(presets) == 4, "four factory presets: %ld", (long)CFArrayGetCount(presets));
            const AUPreset* pluck = (const AUPreset*)CFArrayGetValueAtIndex(presets, 1);
            CHECK(pluck->presetNumber == 1 && [(__bridge NSString*)pluck->presetName isEqualToString:@"Pluck"], "preset 1 is Pluck");
            OK(AudioUnitSetProperty(unit, kAudioUnitProperty_PresentPreset, kAudioUnitScope_Global, 0, pluck, sizeof(AUPreset)), "select Pluck");
            CFRelease(presets);
            AudioUnitParameterID release = findParam(list, @"Release");
            // Release is logarithmic (1 to 2000 ms): the AU carries its normalized value.
            float pluckRelease = logf(80.0f) / logf(2000.0f);
            for (int i = 0; i < 20 && fabsf(getParam(release) - pluckRelease) > 1e-4f; i++) {
                render(outChannels);
                run(0.02);
            }
            CHECK(getParam(kProgramParam) == 1 && fabsf(getParam(release) - pluckRelease) < 1e-4f, "Pluck loaded: program %g, release %g", getParam(kProgramParam), getParam(release));
            AUPreset current;
            size = sizeof(current);
            OK(AudioUnitGetProperty(unit, kAudioUnitProperty_PresentPreset, kAudioUnitScope_Global, 0, &current, &size), "present preset");
            CHECK(current.presetNumber == 1, "present preset %d", (int)current.presetNumber);
            CFRelease(current.presetName);
        }
        if (midifx) {
            // An AUv3 MIDI processor has a silent stereo output, which
            // out-of-process rendering needs.
            CHECK(outChannels == (v3 ? 2 : 0) && elementCount(kAudioUnitScope_Input) == 0, "MIDI processor without audio: %u output channels", (unsigned)outChannels);
            AUMIDIOutputCallbackStruct callback = { midiOutput, NULL };
            OK(AudioUnitSetProperty(unit, kAudioUnitProperty_MIDIOutputCallback, kAudioUnitScope_Global, 0, &callback, sizeof(callback)), "MIDI output callback");
            AudioUnitParameterID transpose = findParam(list, @"Transpose");
            setParam(transpose, 36);  // step 36 of -24..24: +12 semitones
            OK(MusicDeviceMIDIEvent(unit, 0x90, 60, 100, 10), "note on");
            CHECK(render(outChannels) == 0, "MIDI processor output silent");
            CHECK(midiOutNotes == 1 && midiOutKey == 72, "transposed note out: %d notes, key %d", midiOutNotes, midiOutKey);
            printf("midifx: note 60 came out as %d\n", midiOutKey);

            // MIDI 2.0: a host that asks for it before initializing gets
            // MIDI 2.0 messages through the event list callback, with
            // 16-bit velocities intact.
            OK(AudioUnitUninitialize(unit), "uninitialize");
            SInt32 protocol = kMIDIProtocol_2_0;
            OK(AudioUnitSetProperty(unit, kAudioUnitProperty_HostMIDIProtocol, kAudioUnitScope_Global, 0, &protocol, sizeof(protocol)), "host MIDI protocol");
            AUMIDIEventListBlock block = ^OSStatus(AUEventSampleTime time, uint8_t cable, const MIDIEventList* events) {
                (void)time; (void)cable;
                CHECK(events->protocol == kMIDIProtocol_2_0, "output protocol %d", (int)events->protocol);
                const MIDIEventPacket* packet = &events->packet[0];
                for (UInt32 i = 0; i < events->numPackets; i++) {
                    if (packet->wordCount >= 2 && (packet->words[0] & 0xFFF00000u) == 0x40900000u) {
                        ump2Notes++;
                        ump2Key = (packet->words[0] >> 8) & 0x7F;
                        ump2Velocity = packet->words[1] >> 16;
                    }
                    packet = MIDIEventPacketNext(packet);
                }
                return noErr;
            };
            OK(AudioUnitSetProperty(unit, kAudioUnitProperty_MIDIOutputEventListCallback, kAudioUnitScope_Global, 0, &block, sizeof(block)), "MIDI event list callback");
            OK(AudioUnitInitialize(unit), "initialize");
            const uint32_t noteOn2[2] = { 0x40903E00, 0x12340000 };  // key 62, velocity 0x1234
            sendUMP(5, kMIDIProtocol_2_0, noteOn2, 2);
            render(outChannels);
            CHECK(ump2Notes == 1 && ump2Key == 74 && ump2Velocity == 0x1234, "MIDI 2.0 note out: %d notes, key %d, velocity %#x", ump2Notes, ump2Key, ump2Velocity);
            printf("midifx: MIDI 2.0 velocity %#x kept\n", ump2Velocity);
            const uint32_t noteOn1 = 0x20904064;  // MIDI 1.0 in a packet: key 64, velocity 100
            sendUMP(5, kMIDIProtocol_1_0, &noteOn1, 1);
            render(outChannels);
            CHECK(ump2Notes == 2 && ump2Key == 76, "MIDI 1.0 packet note out: %d notes, key %d", ump2Notes, ump2Key);
        }
        if (sidechain) {
            CHECK(elementCount(kAudioUnitScope_Input) == 2, "main and side chain inputs");
            Float64 latency = 0;
            UInt32 size = sizeof(latency);
            for (int i = 0; i < 20 && latency == 0; i++) {
                render(outChannels);
                run(0.02);
                size = sizeof(latency);
                OK(AudioUnitGetProperty(unit, kAudioUnitProperty_Latency, kAudioUnitScope_Global, 0, &latency, &size), "latency");
            }
            CHECK(fabs(latency - 0.002) < 1e-4, "lookahead latency %g s", latency);
            inputLevel = 0.5f;
            float peak = 0;
            for (int i = 0; i < 20; i++) peak = render(outChannels);
            CHECK(peak > 0 && peak < 0.5f, "loud side chain ducks the output: peak %g", peak);
            printf("sidechain: latency %g s, ducked peak %g\n", latency, peak);
        }

        if (stall) {
            // The deadline guard: the output is the input times one plus
            // the note events received; Stall (ms) sleeps on the guard's
            // thread, whose deadline is half a block (5.3 ms).
            AudioUnitParameterID stallParam = findParam(list, @"Stall");
            inputLevel = 0.25f;
            float peak = render(outChannels);
            CHECK(peak == 0.25f, "in-time block passes the input: peak %g", peak);
            setParam(stallParam, 60);
            double t0 = nowMs();
            peak = render(outChannels);
            double late = nowMs() - t0;
            CHECK(late < 6.5 && peak == 0, "stalled block: %.1f ms, peak %g", late, peak);
            // Skipped while the stalled block still runs; the notes and
            // Stall 0 are kept for later.
            OK(MusicDeviceMIDIEvent(unit, 0x90, 60, 100, 10), "note on");
            OK(MusicDeviceMIDIEvent(unit, 0x80, 60, 0, 300), "note off");
            setParam(stallParam, 0);
            t0 = nowMs();
            peak = render(outChannels);
            CHECK(nowMs() - t0 < 5 && peak == 0, "skipped block: %.1f ms, peak %g", nowMs() - t0, peak);
            run(0.08);
            peak = render(outChannels);
            CHECK(peak == 0.75f, "after the stall the carried notes count: peak %g, want 0.75", peak);
            printf("stall: late block returned after %.1f ms\n", late);
        }

        UInt32 size = 0;
        Boolean writable;
        if (AudioUnitGetPropertyInfo(unit, kAudioUnitProperty_CocoaUI, kAudioUnitScope_Global, 0, &size, &writable) == noErr) {
            checkEditor(instrument, outChannels);
            printf("editor ok\n");
        }

        OK(AudioUnitUninitialize(unit), "uninitialize");
        OK(AudioComponentInstanceDispose(unit), "dispose");
        printf("aucheck %s ok\n", kind);
    }
    return 0;
}

// auv3check drives a registered vst3go AUv3 extension through the AUv3 API
// the way an AUv3 host does: out-of-process instantiation, the parameter
// tree, fullState round trips, rendering through the render block, and
// the extension's view controller, whose preferred size is the editor's.
// It builds for macOS and for the iOS simulator, where it runs with
// xcrun simctl spawn; there the view controller's view is loaded without
// a window.
//
// usage: auv3check <type> <subtype> <manufacturer> [<width>x<height>]
// The size, when given, is the editor's.
#include <TargetConditionals.h>
#import <AudioToolbox/AudioToolbox.h>
#import <CoreAudioKit/CoreAudioKit.h>
#if TARGET_OS_OSX
#import <Cocoa/Cocoa.h>
#else
#import <UIKit/UIKit.h>
#endif
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CHECK(cond, ...)                                                      \
    do {                                                                      \
        if (!(cond)) {                                                        \
            fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__);              \
            fprintf(stderr, __VA_ARGS__);                                     \
            fprintf(stderr, "\n");                                            \
            exit(1);                                                          \
        }                                                                     \
    } while (0)

enum { kFrames = 512 };

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

int main(int argc, char** argv) {
    @autoreleasepool {
        CHECK(argc == 4 || argc == 5, "usage: auv3check <type> <subtype> <manufacturer> [<width>x<height>]");
        double wantWidth = 0, wantHeight = 0;
        CHECK(argc == 4 || sscanf(argv[4], "%lfx%lf", &wantWidth, &wantHeight) == 2, "editor size %s", argv[4]);
#if TARGET_OS_OSX
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
#endif
        AudioComponentDescription desc = { fourCC(argv[1]), fourCC(argv[2]), fourCC(argv[3]), 0, 0 }, found;
        AudioComponent component = AudioComponentFindNext(NULL, &desc);
        CHECK(component && AudioComponentGetDescription(component, &found) == noErr, "component %s %s %s registered", argv[1], argv[2], argv[3]);
        CHECK(found.componentFlags & kAudioComponentFlag_IsV3AudioUnit, "a version 3 audio unit");

        __block AUAudioUnit* unit = nil;
        __block NSError* error = nil;
        __block BOOL done = NO;
        [AUAudioUnit instantiateWithComponentDescription:desc options:kAudioComponentInstantiation_LoadOutOfProcess completionHandler:^(AUAudioUnit* created, NSError* err) {
            unit = created;
            error = err;
            done = YES;
        }];
        for (int i = 0; i < 1000 && !done; i++) run(0.01);
        CHECK(unit, "instantiate: %s", error.description.UTF8String);

        // Parameters and state.
        AUParameter* param = nil;
        for (AUParameter* p in unit.parameterTree.allParameters) {
            if ((p.flags & kAudioUnitParameterFlag_IsWritable) && p.maxValue > p.minValue) {
                param = p;
                break;
            }
        }
        CHECK(param, "a writable parameter among %lu", (unsigned long)unit.parameterTree.allParameters.count);
        AUValue saved = param.minValue + (param.maxValue - param.minValue) * 0.25f;
        param.value = saved;
        run(0.1);
        NSDictionary* state = unit.fullState;
        CHECK(state[@kAUPresetDataKey], "fullState holds plugin data");
        param.value = param.maxValue;
        run(0.1);
        unit.fullState = state;
        run(0.1);
        AUValue restored = param.value;
        CHECK(fabsf(restored - saved) <= (param.maxValue - param.minValue) * 1e-3f, "fullState restored %s: %g, want %g", param.identifier.UTF8String, restored, saved);
        printf("state: %s restored to %g\n", param.identifier.UTF8String, restored);

        // Rendering through the render block, input from the pull block.
        AVAudioFormat* format = [[AVAudioFormat alloc] initStandardFormatWithSampleRate:48000 channels:2];
        for (AUAudioUnitBus* bus in unit.outputBusses) {
            if (bus.format.channelCount == 2) CHECK([bus setFormat:format error:&error], "output format: %s", error.description.UTF8String);
        }
        // A host enables the input busses it feeds; the v2 bridge starts
        // with them disabled and pulls only enabled ones.
        for (AUAudioUnitBus* bus in unit.inputBusses) {
            if (bus.format.channelCount == 2) CHECK([bus setFormat:format error:&error], "input format: %s", error.description.UTF8String);
            bus.enabled = YES;
        }
        unit.maximumFramesToRender = kFrames;
        CHECK([unit allocateRenderResourcesAndReturnError:&error], "allocate render resources: %s", error.description.UTF8String);
        AURenderPullInputBlock pull = ^AUAudioUnitStatus(AudioUnitRenderActionFlags* flags, const AudioTimeStamp* ts, AUAudioFrameCount frames, NSInteger bus, AudioBufferList* io) {
            (void)flags; (void)ts; (void)bus;
            for (UInt32 b = 0; b < io->mNumberBuffers; b++) {
                float* data = (float*)io->mBuffers[b].mData;
                for (UInt32 i = 0; i < frames; i++) data[i] = 0.5f;
            }
            return noErr;
        };
        UInt32 outChannels = unit.outputBusses.count ? unit.outputBusses[0].format.channelCount : 0;
        float* storage = calloc(kFrames * 8, sizeof(float));
        AudioBufferList* list = calloc(1, sizeof(AudioBufferList) + 8 * sizeof(AudioBuffer));
        AudioTimeStamp ts = { 0 };
        ts.mFlags = kAudioTimeStampSampleTimeValid;
        for (int block = 0; block < 8; block++) {
            list->mNumberBuffers = outChannels;
            for (UInt32 b = 0; b < outChannels; b++) list->mBuffers[b] = (AudioBuffer){ 1, kFrames * sizeof(float), storage + b * kFrames };
            AudioUnitRenderActionFlags flags = 0;
            AUAudioUnitStatus status = unit.renderBlock(&flags, &ts, kFrames, 0, list, unit.inputBusses.count ? pull : nil);
            CHECK(status == noErr, "render block %d: %d", block, (int)status);
            ts.mSampleTime += kFrames;
        }
        printf("render: 8 blocks of %d frames, %u output channels\n", kFrames, (unsigned)outChannels);
        [unit deallocateRenderResources];
        free(list);
        free(storage);

        // The extension's view controller shows the editor.
#if TARGET_OS_OSX
        typedef NSViewController ViewController;
#else
        typedef UIViewController ViewController;
#endif
        __block ViewController* controller = nil;
        done = NO;
        [unit requestViewControllerWithCompletionHandler:^(ViewController* vc) {
            controller = vc;
            done = YES;
        }];
        for (int i = 0; i < 1000 && !done; i++) run(0.01);
        CHECK(controller, "view controller");
#if TARGET_OS_OSX
        NSWindow* window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 900, 640) styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:NO];
        window.releasedWhenClosed = NO;
        window.contentViewController = controller;
        [window orderFrontRegardless];
#else
        [controller loadViewIfNeeded];
#endif
        CGSize size = { 0, 0 };
        for (int i = 0; i < 200 && (size.width == 0 || size.height == 0); i++) {
            run(0.05);
            size = controller.preferredContentSize;
        }
        CHECK(size.width > 0 && size.height > 0, "editor size %gx%g", size.width, size.height);
        CHECK(argc == 4 || (size.width == wantWidth && size.height == wantHeight), "editor size %gx%g, want %s", size.width, size.height, argv[4]);
        printf("editor: %gx%g\n", size.width, size.height);
#if TARGET_OS_OSX
        [window close];
#endif
        printf("auv3check ok\n");
    }
    return 0;
}

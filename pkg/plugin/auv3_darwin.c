//go:build darwin && auv3

// AUv3 extension front end (macOS and iOS), built with the auv3 tag into
// the extension's executable (vst3go build -format auv3). The principal
// class creates each audio unit as an AUAudioUnitV2Bridge around the
// Audio Unit front end (au_darwin.c), registered in the extension's
// process, and shows its editor as the extension's view.

#include <TargetConditionals.h>
#import <AudioToolbox/AudioToolbox.h>
#import <CoreAudioKit/CoreAudioKit.h>
#if TARGET_OS_OSX
#import <Cocoa/Cocoa.h>
typedef NSView AUNativeView;
#else
#import <UIKit/UIKit.h>
typedef UIView AUNativeView;
#endif

#ifndef VST3GO_AU_VIEW_CLASS
#define VST3GO_AU_VIEW_CLASS VST3GoAUView
#endif
#define AUV3_CAT2(a, b) a##b
#define AUV3_CAT(a, b) AUV3_CAT2(a, b)
// The extension's NSExtensionPrincipalClass.
#define AUV3_CLASS AUV3_CAT(VST3GO_AU_VIEW_CLASS, Extension)

extern bool vst3go_au_register(const AudioComponentDescription* desc, AudioComponentDescription* private);
extern AUNativeView* vst3go_au_open_view(AudioUnit unit);
extern NSString* const VST3GoAUEditorResized;

@interface AUV3_CLASS : AUViewController <AUAudioUnitFactory>
@end

// MIDI processors have no audio, but an out-of-process AUv3 cannot
// allocate render resources without an output bus (and Logic Pro requires
// one): this bridge adds a stereo output that the Audio Unit renders
// silent.
#define AUV3_MIDI_CLASS AUV3_CAT(VST3GO_AU_VIEW_CLASS, MIDIProcessor)
@interface AUV3_MIDI_CLASS : AUAudioUnitV2Bridge
@end

@implementation AUV3_MIDI_CLASS {
    AUAudioUnitBusArray* outputs;
}

- (void)dealloc {
    [outputs release];
    [super dealloc];
}

- (AUAudioUnitBusArray*)outputBusses {
    if (!outputs) {
        AVAudioFormat* format = [[[AVAudioFormat alloc] initStandardFormatWithSampleRate:44100 channels:2] autorelease];
        AUAudioUnitBus* bus = [[[AUAudioUnitBus alloc] initWithFormat:format error:nil] autorelease];
        outputs = [[AUAudioUnitBusArray alloc] initWithAudioUnit:self busType:AUAudioUnitBusTypeOutput busses:@[ bus ]];
    }
    return outputs;
}
@end

@implementation AUV3_CLASS {
    AUAudioUnitV2Bridge* unit;           // the unit whose editor the view shows
    AudioComponentDescription unitDesc;  // its public component codes
    BOOL handedOut;                      // unit has been returned to the host
    AUNativeView* editor;
}

static AUAudioUnitV2Bridge* new_unit(AudioComponentDescription desc, NSError** error) {
    AudioComponentDescription inner;
    if (!vst3go_au_register(&desc, &inner)) {
        if (error) *error = [NSError errorWithDomain:NSOSStatusErrorDomain code:kAudioUnitErr_FailedInitialization userInfo:nil];
        return nil;
    }
    Class cls = desc.componentType == kAudioUnitType_MIDIProcessor ? [AUV3_MIDI_CLASS class] : [AUAudioUnitV2Bridge class];
    return [[cls alloc] initWithComponentDescription:inner error:error];
}

static OSType four_cc(id value) {
    if (![value isKindOfClass:[NSString class]] || [(NSString*)value length] != 4) return 0;
    const char* s = [(NSString*)value UTF8String];
    return (OSType)(uint8_t)s[0] << 24 | (OSType)(uint8_t)s[1] << 16 | (OSType)(uint8_t)s[2] << 8 | (OSType)(uint8_t)s[3];
}

// sole_component reads the extension's component from its Info.plist,
// false when it has several.
static bool sole_component(AudioComponentDescription* desc) {
    NSDictionary* extension = [[NSBundle mainBundle] objectForInfoDictionaryKey:@"NSExtension"];
    NSArray* components = extension[@"NSExtensionAttributes"][@"AudioComponents"];
    if (![components isKindOfClass:[NSArray class]] || components.count != 1) return false;
    NSDictionary* c = components[0];
    *desc = (AudioComponentDescription){ four_cc(c[@"type"]), four_cc(c[@"subtype"]), four_cc(c[@"manufacturer"]), 0, 0 };
    return desc->componentType && desc->componentSubType && desc->componentManufacturer;
}

static bool same_component(AudioComponentDescription a, AudioComponentDescription b) {
    return a.componentType == b.componentType && a.componentSubType == b.componentSubType && a.componentManufacturer == b.componentManufacturer;
}

// The host takes the view's preferred size when the view is delivered,
// which happens on connection, before it asks for the audio unit: the
// view creates the unit then, to open the editor at its size, and the
// first request for that component returns it.
- (AUAudioUnit*)createAudioUnitWithComponentDescription:(AudioComponentDescription)desc error:(NSError**)error {
    @synchronized(self) {
        if (unit && !handedOut && same_component(unitDesc, desc)) {
            handedOut = YES;
            return [[unit retain] autorelease];
        }
    }
    AUAudioUnitV2Bridge* created = new_unit(desc, error);
    if (!created) return nil;
    BOOL show = NO;
    @synchronized(self) {
        if (!unit) {
            unit = [created retain];
            unitDesc = desc;
            handedOut = YES;
            show = YES;
        }
    }
    if (show) {
        dispatch_async(dispatch_get_main_queue(), ^{
            [self showEditor];
        });
    }
    return [created autorelease];
}

- (void)loadView {
    self.view = [[[AUNativeView alloc] initWithFrame:CGRectMake(0, 0, 0, 0)] autorelease];
    AudioComponentDescription desc;
    @synchronized(self) {
        if (!unit && sole_component(&desc)) {
            unit = new_unit(desc, NULL);
            unitDesc = desc;
        }
    }
}

- (void)viewDidLoad {
    [super viewDidLoad];
    [self showEditor];
}

// showEditor opens the editor once the audio unit and the view exist.
- (void)showEditor {
    if (editor || !unit || !self.isViewLoaded) return;
    AUNativeView* view = vst3go_au_open_view(unit.audioUnit);
    if (!view) return;
    editor = [view retain];
    CGRect frame = editor.frame;
    self.view.frame = frame;
    [self.view addSubview:editor];
    self.preferredContentSize = frame.size;
    [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(editorResized:) name:VST3GoAUEditorResized object:editor];
}

// The plugin resized its editor: ask the host for the new size.
- (void)editorResized:(NSNotification*)note {
    (void)note;
    self.preferredContentSize = editor.frame.size;
}

// The host laid out the view: the editor fills it.
- (void)fitEditor {
    CGRect bounds = self.view.bounds;
    if (editor && bounds.size.width > 0 && bounds.size.height > 0) editor.frame = bounds;
}

#if TARGET_OS_OSX
- (void)viewDidLayout {
    [super viewDidLayout];
    [self fitEditor];
}
#else
- (void)viewDidLayoutSubviews {
    [super viewDidLayoutSubviews];
    [self fitEditor];
}
#endif

- (void)dealloc {
    [[NSNotificationCenter defaultCenter] removeObserver:self];
    [editor removeFromSuperview];
    [editor release];
    [unit release];
    [super dealloc];
}
@end

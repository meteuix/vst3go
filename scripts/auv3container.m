// The container application of an AUv3 extension (vst3go build -format
// auv3, macOS and iOS). Audio Unit extensions ship inside an application;
// this one only tells the user that the extension is installed.
#include <TargetConditionals.h>

#if TARGET_OS_OSX
#import <Cocoa/Cocoa.h>

int main(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        [NSApp activateIgnoringOtherApps:YES];
        NSAlert* alert = [[NSAlert alloc] init];
        alert.messageText = NSBundle.mainBundle.infoDictionary[@"CFBundleName"];
        alert.informativeText = @"This application installs the Audio Unit extension. Open a host application to use it.";
        [alert runModal];
        [alert release];
    }
    return 0;
}

#else
#import <UIKit/UIKit.h>

@interface VST3GoContainerDelegate : UIResponder <UIApplicationDelegate>
@property (nonatomic, retain) UIWindow* window;
@end

@implementation VST3GoContainerDelegate
- (BOOL)application:(UIApplication*)application didFinishLaunchingWithOptions:(NSDictionary*)options {
    (void)application;
    (void)options;
    UIViewController* controller = [[[UIViewController alloc] init] autorelease];
    UILabel* label = [[[UILabel alloc] init] autorelease];
    label.text = [NSString stringWithFormat:@"%@\n\nThis application installs the Audio Unit extension. Open a host application to use it.",
                                            NSBundle.mainBundle.infoDictionary[@"CFBundleName"]];
    label.numberOfLines = 0;
    label.textAlignment = NSTextAlignmentCenter;
    label.backgroundColor = UIColor.systemBackgroundColor;
    controller.view = label;
    self.window = [[[UIWindow alloc] initWithFrame:UIScreen.mainScreen.bounds] autorelease];
    self.window.rootViewController = controller;
    [self.window makeKeyAndVisible];
    return YES;
}
@end

int main(int argc, char** argv) {
    @autoreleasepool {
        return UIApplicationMain(argc, argv, nil, NSStringFromClass([VST3GoContainerDelegate class]));
    }
}
#endif

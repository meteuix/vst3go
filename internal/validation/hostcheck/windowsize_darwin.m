// windowsize_darwin prints the size of the on-screen windows of a process
// as WIDTHxHEIGHT lines (frame size, title bar included), for
// scripts/check_standalone.sh. It needs no screen recording permission.
//
// usage: windowsize_darwin <pid>
#import <Cocoa/Cocoa.h>

int main(int argc, char** argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: windowsize_darwin <pid>\n");
        return 2;
    }
    int pid = atoi(argv[1]);
    CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly, kCGNullWindowID);
    for (NSDictionary* w in (__bridge NSArray*)list) {
        if ([w[(id)kCGWindowOwnerPID] intValue] != pid || [w[(id)kCGWindowLayer] intValue] != 0) continue;
        NSDictionary* bounds = w[(id)kCGWindowBounds];
        printf("%dx%d\n", [bounds[@"Width"] intValue], [bounds[@"Height"] intValue]);
    }
    CFRelease(list);
    return 0;
}

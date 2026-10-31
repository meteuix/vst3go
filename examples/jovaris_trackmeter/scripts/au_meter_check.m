#define main upstream_main
#include "../../../internal/validation/hostcheck/aucheck_darwin.m"
#undef main
#include <dlfcn.h>
static void blocks(int n) { for(int i=0;i<n;i++){render(2);run(.002);} run(.15); }
static void dump(WKWebView *web, const char *label) {
 printf("%s: %s\n",label,eval(web,@"JSON.stringify({peak:vst3go.param(14).plain,vu:vst3go.param(12).plain,clip:vst3go.param(16).plain,dom:document.getElementById('peak-L').textContent})").UTF8String);fflush(stdout);
}
int main(int argc,char**argv){ @autoreleasepool {
 CHECK(argc==2,"bundle path");
 [NSApplication sharedApplication];[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
 NSBundle *bundle=[NSBundle bundleWithPath:[NSString stringWithUTF8String:argv[1]]];
 CHECK(bundle,"bundle"); void *lib=dlopen(bundle.executablePath.UTF8String,RTLD_NOW|RTLD_LOCAL);CHECK(lib,"dlopen %s",dlerror());
 NSDictionary *entry=[bundle.infoDictionary[@"AudioComponents"] firstObject];
 AudioComponentDescription desc={fourCC([entry[@"type"] UTF8String]),fourCC([entry[@"subtype"] UTF8String]),fourCC([entry[@"manufacturer"] UTF8String]),0,0};
 AudioComponentFactoryFunction factory=(AudioComponentFactoryFunction)dlsym(lib,"VST3GoAUFactory");
 AudioComponent comp=AudioComponentRegister(&desc,CFSTR("TrackMeter test"),0,factory);CHECK(comp,"register");
 OK(AudioComponentInstanceNew(comp,&unit),"instance");setup();OK(AudioUnitInitialize(unit),"init");
 AudioUnitCocoaViewInfo info;UInt32 size=sizeof(info);OK(AudioUnitGetProperty(unit,kAudioUnitProperty_CocoaUI,kAudioUnitScope_Global,0,&info,&size),"CocoaUI");
 NSBundle *vb=[NSBundle bundleWithURL:(__bridge NSURL*)info.mCocoaAUViewBundleLocation];
 id<AUCocoaUIBase> vf=[[[vb classNamed:(__bridge NSString*)info.mCocoaAUViewClass[0]] alloc] init];
 NSView *view=[vf uiViewForAudioUnit:unit withSize:NSMakeSize(800,380)];CHECK(view,"view");
 NSWindow *window=[[NSWindow alloc]initWithContentRect:NSMakeRect(30,30,800,380) styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:NO];window.releasedWhenClosed=NO;[window.contentView addSubview:view];[window orderFrontRegardless];
 WKWebView *web=nil;for(int i=0;i<100&&!web;i++){run(.05);WKWebView*w=findWebView(view);if(w&&[eval(w,@"String(!!window.vst3go && !!document.getElementById('reset'))") isEqual:@"true"])web=w;}CHECK(web,"web ready");
 inputLevel=.2;blocks(150);dump(web,"input .2");
 inputLevel=.02;blocks(150);dump(web,"input .02");
 CHECK([eval(web,@"String(vst3go.param(14).plain < -30)") isEqual:@"true"],"dynamic meter follows quieter input");
 eval(web,@"document.getElementById('hold').click()");run(.1);inputLevel=1.25;blocks(10);inputLevel=0;blocks(150);dump(web,"held clip");
 eval(web,@"document.getElementById('reset').click()");run(.1);blocks(10);dump(web,"reset playing silence");
 CHECK([eval(web,@"String(vst3go.param(14).plain < -100 && vst3go.param(16).plain === 0)") isEqual:@"true"],"Reset clears held peak/clip");
 inputLevel=1.25;blocks(10);dump(web,"before stopped reset");
 eval(web,@"document.getElementById('reset').click()");run(.4);dump(web,"stopped reset (no render)");
 CHECK([eval(web,@"String(document.getElementById('peak-L').textContent === '−∞' && !document.getElementById('clip-L').classList.contains('active'))") isEqual:@"true"],"stopped Reset visibly clears peak/clip before any render");
 run(1);CHECK([eval(web,@"String(document.getElementById('version').textContent.includes('IDLE') && document.getElementById('vu-L').textContent.includes('—'))") isEqual:@"true"],"stopped audio marked IDLE");
 inputLevel=0;blocks(10);CHECK([eval(web,@"String(document.getElementById('peak-L').textContent === '−∞')") isEqual:@"true"],"resume consumes reset");
 eval(web,@"document.getElementById('hold').click();window.savedReceive=window.__vst3goReceive;window.__vst3goReceive=m=>{if(m.type!=='param-change')savedReceive(m)}");run(.1);
 inputLevel=.05;blocks(150);dump(web,"callback loss fallback");
 CHECK([eval(web,@"String(document.getElementById('peak-L').textContent === '−26.0')") isEqual:@"true"],"snapshot route updates DOM without parameter push messages");
 eval(web,@"window.__vst3goReceive=window.savedReceive;document.getElementById('layout-v').click()");run(.3);
 CHECK(view.frame.size.width==400 && view.frame.size.height==940,"vertical native resize %gx%g",view.frame.size.width,view.frame.size.height);
 NSBitmapImageRep *rep=[view bitmapImageRepForCachingDisplayInRect:view.bounds];[view cacheDisplayInRect:view.bounds toBitmapImageRep:rep];[[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:@"evidence/native_vertical.png" atomically:YES];
 eval(web,@"document.getElementById('layout-h').click()");run(.3);
 rep=[view bitmapImageRepForCachingDisplayInRect:view.bounds];[view cacheDisplayInRect:view.bounds toBitmapImageRep:rep];[[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:@"evidence/native_horizontal.png" atomically:YES];
 puts("PASS native AU/WebKit dynamic input and Reset");return 0;
}}

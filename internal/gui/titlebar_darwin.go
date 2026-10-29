//go:build darwin

package gui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <crt_externs.h>
#import <mach-o/loader.h>
#import <objc/runtime.h>

static const NSWindowButton lightKinds[3] = {NSWindowCloseButton, NSWindowMiniaturizeButton, NSWindowZoomButton};

@interface LoomLights : NSObject {
@public
	NSWindow *win;
	CGFloat height;
	CGFloat x[3], top[3];
	BOOL busy, pending;
}
@end

@implementation LoomLights
- (void)apply {
	if (busy || win == nil || (win.styleMask & NSWindowStyleMaskFullScreen)) return;
	NSView *bar = [win standardWindowButton:NSWindowCloseButton].superview, *box = bar.superview, *frame = box.superview;
	if (frame == nil) return;
	busy = YES;
	BOOL moved = NO;
	NSRect f = box.frame;
	NSRect want = NSMakeRect(f.origin.x, frame.isFlipped ? 0 : NSHeight(frame.bounds) - height, f.size.width, height);
	if (!NSEqualRects(f, want)) {
		box.frame = want;
		moved = YES;
	}
	for (int i = 0; i < 3; i++) {
		NSButton *b = [win standardWindowButton:lightKinds[i]];
		if (b.superview != bar) continue;
		NSAutoresizingMaskOptions keepTop = bar.isFlipped ? NSViewMaxYMargin : NSViewMinYMargin;
		if (b.autoresizingMask != keepTop) b.autoresizingMask = keepTop;
		NSPoint o = NSMakePoint(x[i], bar.isFlipped ? top[i] : NSHeight(bar.bounds) - top[i] - NSHeight(b.frame));
		if (!NSEqualPoints(b.frame.origin, o)) {
			[b setFrameOrigin:o];
			moved = YES;
		}
	}
	if (moved && !frame.inLiveResize) [frame viewDidEndLiveResize];
	busy = NO;
}

- (void)later {
	if (pending) return;
	pending = YES;
	CFRunLoopPerformBlock(CFRunLoopGetMain(), kCFRunLoopCommonModes, ^{
		pending = NO;
		[self apply];
	});
	CFRunLoopWakeUp(CFRunLoopGetMain());
}
- (void)changed:(NSNotification *)n {
	if (!busy) [self later];
}
- (void)resized:(NSNotification *)n {
	[self apply];
	[self later];
}
- (void)closing:(NSNotification *)n {
	[[NSNotificationCenter defaultCenter] removeObserver:self];
	win = nil;
}
- (void)dealloc {
	[[NSNotificationCenter defaultCenter] removeObserver:self];
	[super dealloc];
}
@end

static char lightsKey;

static NSRect sheetBelowTitlebar(id self, SEL _cmd, NSWindow *w, NSWindow *sheet, NSRect r) {
	LoomLights *l = objc_getAssociatedObject(w, &lightsKey);
	if (l != nil && l->win != nil && !(w.styleMask & NSWindowStyleMaskFullScreen)) r.origin.y = NSHeight(w.frame) - l->height;
	return r;
}

static void runJSAlert(id self, SEL _cmd, WKWebView *webView, NSString *message, WKFrameInfo *frame, void (^completionHandler)(void)) {
	NSAlert *alert = [[[NSAlert alloc] init] autorelease];
	alert.messageText = message ?: @"";
	[alert addButtonWithTitle:@"好"];
	NSWindow *w = webView.window;
	if (w != nil) {
		[alert beginSheetModalForWindow:w completionHandler:^(NSModalResponse returnCode) {
			completionHandler();
		}];
	} else {
		[alert runModal];
		completionHandler();
	}
}

static void runJSConfirm(id self, SEL _cmd, WKWebView *webView, NSString *message, WKFrameInfo *frame, void (^completionHandler)(BOOL)) {
	NSAlert *alert = [[[NSAlert alloc] init] autorelease];
	alert.messageText = message ?: @"";
	[alert addButtonWithTitle:@"确认"];
	[alert addButtonWithTitle:@"取消"];
	NSWindow *w = webView.window;
	if (w != nil) {
		[alert beginSheetModalForWindow:w completionHandler:^(NSModalResponse returnCode) {
			completionHandler(returnCode == NSAlertFirstButtonReturn);
		}];
	} else {
		NSModalResponse returnCode = [alert runModal];
		completionHandler(returnCode == NSAlertFirstButtonReturn);
	}
}

static NSEvent *firstPress(NSEvent *e) {
	NSWindow *w = e.window;
	LoomLights *l = w == nil ? nil : objc_getAssociatedObject(w, &lightsKey);
	if (l == nil || l->win == nil || w.isKeyWindow || !w.isMovable || e.clickCount != 1 || (w.styleMask & NSWindowStyleMaskFullScreen)) return e;
	NSView *frame = w.contentView.superview;
	NSPoint p = e.locationInWindow;
	if (frame == nil || !NSPointInRect(p, w.contentLayoutRect) || NSHeight(frame.bounds) - p.y >= l->height) return e;
	NSView *hit = [frame hitTest:p];
	if (hit == nil || [hit acceptsFirstMouse:e]) return e;
	[w performWindowDragWithEvent:e];
	return e;
}

static BOOL designOf26(void) {
	if (![NSProcessInfo.processInfo isOperatingSystemAtLeastVersion:(NSOperatingSystemVersion){26, 0, 0}]) return NO;
	id compat = [NSBundle.mainBundle objectForInfoDictionaryKey:@"UIDesignRequiresCompatibility"];
	if ([compat respondsToSelector:@selector(boolValue)] && [compat boolValue]) return NO;
	const struct mach_header_64 *h = _NSGetMachExecuteHeader();
	const struct load_command *c = (const struct load_command *)(h + 1);
	for (uint32_t i = 0; i < h->ncmds; i++, c = (const struct load_command *)((const char *)c + c->cmdsize)) {
		if (c->cmd == LC_BUILD_VERSION) return ((const struct build_version_command *)c)->sdk >= 0x1A0000;
		if (c->cmd == LC_VERSION_MIN_MACOSX) return ((const struct version_min_command *)c)->sdk >= 0x1A0000;
	}
	return NO;
}

static void insetLights(void *p) {
	NSWindow *w = (NSWindow *)p;
	if (w == nil) return;
	id d = w.delegate;
	if (d != nil) {
		Class cls = [d class];
		SEL alertSel = @selector(webView:runJavaScriptAlertPanelWithMessage:initiatedByFrame:completionHandler:);
		if (![d respondsToSelector:alertSel])
			class_addMethod(cls, alertSel, (IMP)runJSAlert, protocol_getMethodDescription(@protocol(WKUIDelegate), alertSel, NO, YES).types);
		SEL confirmSel = @selector(webView:runJavaScriptConfirmPanelWithMessage:initiatedByFrame:completionHandler:);
		if (![d respondsToSelector:confirmSel])
			class_addMethod(cls, confirmSel, (IMP)runJSConfirm, protocol_getMethodDescription(@protocol(WKUIDelegate), confirmSel, NO, YES).types);
	}
	if (!designOf26()) return;
	if (w.toolbar == nil || objc_getAssociatedObject(w, &lightsKey) != nil) return;
	if (w.windowTitlebarLayoutDirection == NSUserInterfaceLayoutDirectionRightToLeft) return;
	NSView *bar = [w standardWindowButton:NSWindowCloseButton].superview, *box = bar.superview, *frame = box.superview;
	if (frame == nil) return;
	[frame layoutSubtreeIfNeeded];
	LoomLights *l = [[[LoomLights alloc] init] autorelease];
	l->win = w;
	l->height = NSHeight(box.frame);
	NSView *views[5] = {box, bar};
	for (int i = 0; i < 3; i++) {
		NSButton *b = [w standardWindowButton:lightKinds[i]];
		if (b == nil || b.superview != bar) return;
		l->x[i] = b.frame.origin.x;
		l->top[i] = bar.isFlipped ? NSMinY(b.frame) : NSHeight(bar.bounds) - NSMaxY(b.frame);
		views[2 + i] = b;
	}
	objc_setAssociatedObject(w, &lightsKey, l, OBJC_ASSOCIATION_RETAIN);
	NSNotificationCenter *nc = [NSNotificationCenter defaultCenter];
	for (int i = 0; i < 5; i++) {
		views[i].postsFrameChangedNotifications = YES;
		[nc addObserver:l selector:@selector(changed:) name:NSViewFrameDidChangeNotification object:views[i]];
	}
	[nc addObserver:l selector:@selector(resized:) name:NSWindowDidResizeNotification object:w];
	[nc addObserver:l selector:@selector(closing:) name:NSWindowWillCloseNotification object:w];
	static BOOL watchingPresses;
	if (!watchingPresses) {
		watchingPresses = YES;
		[NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown handler:^NSEvent *(NSEvent *e) { return firstPress(e); }];
	}
	SEL s = @selector(window:willPositionSheet:usingRect:);
	if (d != nil && ![d respondsToSelector:s])
		class_addMethod([d class], s, (IMP)sheetBelowTitlebar, protocol_getMethodDescription(@protocol(NSWindowDelegate), s, NO, YES).types);
	w.toolbar = nil;
	[l apply];
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

func plainTitlebar(w *application.WebviewWindow) {
	application.InvokeSync(func() {
		if p := w.NativeWindow(); p != nil {
			C.insetLights(p)
		}
	})
}

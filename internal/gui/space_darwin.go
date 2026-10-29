//go:build darwin

package gui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

static void setMovesToActiveSpace(void *win, int on) {
	NSWindow *w = (NSWindow *)win;
	if (on) w.collectionBehavior |= NSWindowCollectionBehaviorMoveToActiveSpace;
	else w.collectionBehavior &= ~NSWindowCollectionBehaviorMoveToActiveSpace;
}

static int windowOpen(void *win) {
	NSWindow *w = (NSWindow *)win;
	return w.isVisible && !w.isMiniaturized;
}

static void activateApp(void) {
	[NSApp activateIgnoringOtherApps:YES];
}

static int appRegular(void) {
	return [NSApp activationPolicy] == NSApplicationActivationPolicyRegular;
}

static void setRegular(int on) {
	[NSApp setActivationPolicy:on ? NSApplicationActivationPolicyRegular : NSApplicationActivationPolicyAccessory];
}

static void activateAgain(void) {
	NSRunningApplication *dock = [NSRunningApplication runningApplicationsWithBundleIdentifier:@"com.apple.dock"].firstObject;
	if (dock != nil) [dock activateWithOptions:0];
	[NSApp activateIgnoringOtherApps:YES];
}

static void setPageZoom(void *w, double z) {
	NSWindow *win = (NSWindow *)w;
	dispatch_async(dispatch_get_main_queue(), ^{
		if (![win respondsToSelector:@selector(webView)]) return;
		id web = [win performSelector:@selector(webView)];
		if (![web isKindOfClass:[WKWebView class]]) return;
		if (@available(macOS 11.0, *)) {
			((WKWebView *)web).pageZoom = z;
		}
	});
}
*/
import "C"

import (
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const spaceSettle = 500 * time.Millisecond

func setMovesToActiveSpace(w *application.WebviewWindow, on bool) {
	if p := w.NativeWindow(); p != nil {
		v := 0
		if on {
			v = 1
		}
		C.setMovesToActiveSpace(p, C.int(v))
	}
}

func windowOpen(w *application.WebviewWindow) bool {
	p := w.NativeWindow()
	return p != nil && C.windowOpen(p) != 0
}

func activateApp() { C.activateApp() }

func setDockVisible(on, front bool) {
	application.InvokeSync(func() {
		regular := C.appRegular() != 0
		if regular == on {
			return
		}
		v := 0
		if on {
			v = 1
		}
		C.setRegular(C.int(v))
		if on && front {
			C.activateAgain()
		} else if front {
			C.activateApp()
		}
	})
}

func setWindowZoom(w *application.WebviewWindow, percent int) {
	if w == nil {
		return
	}
	z := float64(percent) / 100.0
	if z < 1.0 || z > 2.0 {
		z = 1.0
	}
	application.InvokeSync(func() {
		if p := w.NativeWindow(); p != nil {
			C.setPageZoom(p, C.double(z))
		}
	})
}

func showHere(w *application.WebviewWindow) {
	setMovesToActiveSpace(w, true)
	w.Show()
	w.Focus()
	time.AfterFunc(spaceSettle, func() {
		application.InvokeAsync(func() { setMovesToActiveSpace(w, false) })
	})
}

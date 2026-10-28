//go:build darwin

package gui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

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

func showHere(w *application.WebviewWindow) {
	setMovesToActiveSpace(w, true)
	w.Show()
	w.Focus()
	time.AfterFunc(spaceSettle, func() {
		application.InvokeAsync(func() { setMovesToActiveSpace(w, false) })
	})
}

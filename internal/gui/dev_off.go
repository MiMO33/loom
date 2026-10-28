//go:build !dev

package gui

import "io/fs"

func staticFS() fs.FS {
	f, _ := fs.Sub(assets, "assets")
	return f
}

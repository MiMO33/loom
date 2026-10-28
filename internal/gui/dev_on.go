//go:build dev

package gui

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

func staticFS() fs.FS {
	_, file, _, _ := runtime.Caller(0)
	return os.DirFS(filepath.Join(filepath.Dir(file), "assets"))
}

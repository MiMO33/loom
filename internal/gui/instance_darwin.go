//go:build darwin

package gui

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

var heldInstance *os.File

func instanceLock(dir, exe string) string {
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if strings.Contains(filepath.ToSlash(exe), ".app/Contents/MacOS/") {
		exe = "installed"
	}
	sum := sha256.Sum256([]byte(dir + "\x00" + exe))
	return filepath.Join(dir, "app-"+hex.EncodeToString(sum[:6])+".lock")
}

func holdInstance(path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		_ = f.Close()
		return false, nil
	}
	if err != nil {
		_ = f.Close()
		return false, err
	}
	heldInstance = f
	return true, nil
}

func runningAlready(dataDir string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	lock := instanceLock(dataDir, exe)
	held, err := holdInstance(lock)
	if err != nil {
		log.Println("loom instance lock:", err)
		return false
	}
	if held {
		return false
	}
	log.Println("loom is running already; handing over to it")
	if strings.Contains(filepath.ToSlash(exe), ".app/Contents/MacOS/") {
		_ = exec.Command("open", "-b", "com.loom.app").Run()
	}
	return true
}

//go:build darwin

package proc

import (
	"os"
	"strings"
	"testing"
)

func TestUserPath(t *testing.T) {
	UserPath()
	p := os.Getenv("PATH")
	if !strings.Contains(p, "/usr/bin") {
		t.Fatalf("expected /usr/bin in PATH, got %s", p)
	}
}

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSavedFilesAreQuarantined(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tool.zip")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := markDownloaded(p); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 128)
	n, err := unix.Getxattr(p, "com.apple.quarantine", buf)
	if err != nil || !strings.HasPrefix(string(buf[:n]), "0081;") || !strings.Contains(string(buf[:n]), ";tuon;") {
		t.Fatalf("quarantine = %q, %v", buf[:n], err)
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSaveToDownloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, "Downloads"), 0o700)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir := filepath.Join(tmp, "tuon")
	_ = os.MkdirAll(dir, 0o700)

	src := filepath.Join(dir, "save-1")
	_ = os.WriteFile(src, []byte("PDFDATA"), 0o600)
	dst, err := saveToDownloads(src, "../../.zshrc‮gnp.exe")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dst) != filepath.Join(home, "Downloads") || strings.ContainsAny(filepath.Base(dst), "/‮") || strings.HasPrefix(filepath.Base(dst), ".") {
		t.Fatalf("saved as %q", dst)
	}
	if b, _ := os.ReadFile(dst); string(b) != "PDFDATA" {
		t.Fatalf("contents %q", b)
	}
	q := make([]byte, 64)
	if n, err := unix.Getxattr(dst, "com.apple.quarantine", q); err != nil || !strings.HasPrefix(string(q[:n]), "0081;") {
		t.Fatalf("no quarantine mark: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("the temp copy should be gone")
	}
	// a second save with the same name never overwrites
	_ = os.WriteFile(src, []byte("TWO"), 0o600)
	dst2, err := saveToDownloads(src, "../../.zshrc‮gnp.exe")
	if err != nil || dst2 == dst {
		t.Fatalf("second save: %q, %v", dst2, err)
	}

	// refused: a file outside the app's temp folder, a link, a folder
	outside := filepath.Join(home, "secret")
	_ = os.WriteFile(outside, []byte("x"), 0o600)
	if _, err := saveToDownloads(outside, "a"); err == nil {
		t.Fatal("saved a file from outside tuon's temp")
	}
	link := filepath.Join(dir, "save-link")
	_ = os.Symlink(outside, link)
	if _, err := saveToDownloads(link, "a"); err == nil {
		t.Fatal("saved through a link")
	}
	if _, err := saveToDownloads(dir, "a"); err == nil {
		t.Fatal("saved a folder")
	}
}

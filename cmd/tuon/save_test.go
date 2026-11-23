package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ralph/tuon/internal/sandbox"
)

func TestSaveToDownloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, "Downloads"), 0o700)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", "")
	tmp, err := sandbox.TempBase() // the app's temp folder (its own on Linux)
	if err != nil {
		t.Fatal(err)
	}
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
	if q, err := downloadMark(dst); runtime.GOOS == "darwin" && (err != nil || !strings.HasPrefix(q, "0081;")) {
		t.Fatalf("no quarantine mark: %v", err)
	}
	if st, _ := os.Stat(dst); st.Mode().Perm()&0o111 != 0 {
		t.Fatalf("saved file is executable: %v", st.Mode())
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

package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNoWriteThroughLinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "victim")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"login.enc.tmp", "tuon.log"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteFile(filepath.Join(dir, "login.enc.tmp"), []byte("sealed"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Append(filepath.Join(dir, "tuon.log"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("log line")
	f.Close()
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatalf("wrote through a link: %q", b)
	}
	for _, name := range []string{"login.enc.tmp", "tuon.log"} {
		if st, err := os.Lstat(filepath.Join(dir, name)); err != nil || !st.Mode().IsRegular() {
			t.Fatalf("%s is not a plain file", name)
		}
	}
}

// TestAppendNeverThroughLink: a link planted where a log goes (the app can
// write tuon's folder) is replaced, never written through.
func TestAppendNeverThroughLink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "zshrc")
	_ = os.WriteFile(victim, []byte("keep\n"), 0o600)
	log := filepath.Join(dir, "keytrace-launcher.log")
	if err := os.Symlink(victim, log); err != nil {
		t.Fatal(err)
	}
	f, err := Append(log, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("trace\n")
	f.Close()
	if b, _ := os.ReadFile(victim); string(b) != "keep\n" {
		t.Fatalf("wrote through the link: %q", b)
	}
	if st, err := os.Lstat(log); err != nil || st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the link should be replaced by a real file")
	}
}

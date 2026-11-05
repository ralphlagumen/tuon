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

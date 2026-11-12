package safefile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Shred zeroes the file's bytes before removing it: a second link to the
// same file (what's left of the blocks) reads back zeros.
func TestShredZeroesThenRemoves(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret")
	if err := os.WriteFile(p, []byte("the session cookie"), 0o600); err != nil {
		t.Fatal(err)
	}
	peek := filepath.Join(dir, "peek")
	if err := os.Link(p, peek); err != nil { // sees the same blocks
		t.Fatal(err)
	}
	if err := Shred(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
	b, _ := os.ReadFile(peek)
	if !bytes.Equal(b, make([]byte, len("the session cookie"))) {
		t.Fatalf("bytes left: %q", b)
	}
}

// ShredAll never follows a link out of the folder.
func TestShredAllDoesNotFollowLinks(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("not tuon's"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "tuon")
	_ = os.MkdirAll(filepath.Join(dir, "cache"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "cache", "a"), []byte("photo"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "planted")); err != nil {
		t.Fatal(err)
	}
	if err := ShredAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("folder not removed")
	}
	if b, _ := os.ReadFile(outside); string(b) != "not tuon's" {
		t.Fatalf("followed the link: %q", b)
	}
}

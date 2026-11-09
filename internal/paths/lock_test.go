//go:build !windows

package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockDoesNotFollowLinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(Dir(), 0o700)
	target := filepath.Join(home, "elsewhere")
	if err := os.Symlink(target, filepath.Join(Dir(), "tuon.lock")); err != nil {
		t.Fatal(err)
	}
	if release, err := Lock(); err == nil {
		release()
		t.Fatal("locked through a planted link")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("the link's target was created")
	}
}

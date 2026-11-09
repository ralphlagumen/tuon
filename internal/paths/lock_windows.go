//go:build windows

package paths

import (
	"os"
	"path/filepath"
)

// Lock makes sure only one tuon uses the data folder at a time. On Windows
// an open file can't be removed, so a stale lock from a crash is cleared by
// deleting it first; if another tuon holds it open, that fails.
func Lock() (release func(), err error) {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return nil, err
	}
	p := filepath.Join(Dir(), "tuon.lock")
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return nil, ErrRunning
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, ErrRunning
	}
	return func() { f.Close(); os.Remove(p) }, nil
}

//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"syscall"
)

// Lock makes sure only one tuon uses ~/.config/tuon at a time. Two copies
// sharing the encryption keys break each other's sessions, and messages
// then can't be decrypted. The lock goes away when the process exits.
func Lock() (release func(), err error) {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(Dir(), "tuon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrRunning
	}
	return func() { f.Close() }, nil
}

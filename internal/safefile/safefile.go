// Package safefile writes files in tuon's folder without following links.
//
// The sandboxed chat app can write in ~/.config/tuon, and the launcher
// (outside the sandbox) writes there too. A link planted under a name the
// launcher writes would otherwise send that write anywhere you can write.
package safefile

import (
	"os"
)

// Create makes path as a new file for writing: whatever was there (file or
// link) is removed first, and O_EXCL refuses to follow a link that appears
// in between.
func Create(path string, perm os.FileMode) (*os.File, error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
}

// WriteFile writes data to a new file at path (see Create) and syncs it.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	f, err := Create(path, perm)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// Append opens path for appending, but never through a link: a link there
// is removed and a fresh file started.
func Append(path string, perm os.FileMode) (*os.File, error) {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, perm)
	if err != nil {
		return nil, err
	}
	// a link swapped in after the check: refuse what we opened
	if fst, err1 := f.Stat(); err1 == nil {
		if lst, err2 := os.Lstat(path); err2 != nil || !os.SameFile(fst, lst) {
			f.Close()
			return nil, os.ErrExist
		}
	}
	return f, nil
}

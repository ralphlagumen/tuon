package paths

import (
	"errors"
	"os"
	"path/filepath"
)

// WorkDir makes a fresh folder for the launcher's own work (an update's
// build, the login browser's throwaway profile) in ~/Library/Caches/tuon
// (the user cache folder elsewhere). The sandboxed chat app can write to
// tuon's data folder, Downloads and the temp folder, but not here, so it
// can't change what the launcher builds or the browser it runs.
func WorkDir(prefix string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	base = filepath.Join(base, "tuon")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	if st, err := os.Lstat(base); err != nil {
		return "", err
	} else if !st.IsDir() {
		return "", errors.New(base + " isn't a folder")
	}
	return os.MkdirTemp(base, prefix)
}

// WorkBase is the folder WorkDir makes its folders in.
func WorkBase() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "tuon")
}

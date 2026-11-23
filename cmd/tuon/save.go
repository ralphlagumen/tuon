package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/ralph/tuon/internal/safefile"
)

// maxSave: the biggest file tuon downloads (Messenger files, 100 MB).
const maxSave = 100 << 20

// saveToDownloads moves a file the app downloaded (into its own temp
// folder) to ~/Downloads, marked as downloaded from the internet so macOS
// warns before it runs. The app can't write Downloads itself: a bad
// library could otherwise drop a file there without that mark.
func saveToDownloads(src, name string) (string, error) {
	dir, err := photoDir() // the app's temp folder, $TMPDIR/tuon
	if err != nil {
		return "", errors.New("no file to save")
	}
	// the file must sit right in that folder, and not be a link
	if q, err := filepath.EvalSymlinks(filepath.Dir(src)); err != nil || q != dir {
		return "", errors.New("not a file from tuon")
	}
	in, err := os.OpenFile(src, os.O_RDONLY|safefile.NoFollow, 0)
	if err != nil {
		return "", errors.New("not a file from tuon")
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxSave {
		return "", errors.New("not a file tuon can save")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dl := filepath.Join(home, "Downloads")
	name = cleanName(name)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	var out *os.File
	var dst string
	for i := 1; i < 1000 && out == nil; i++ {
		dst = filepath.Join(dl, name)
		if i > 1 {
			dst = filepath.Join(dl, fmt.Sprintf("%s (%d)%s", base, i, ext))
		}
		// a new file only, never through a link, never over an existing one
		out, err = os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|safefile.NoFollow, 0o600)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	if out == nil {
		return "", errors.New("too many files with that name in Downloads")
	}
	n, err := io.Copy(out, io.LimitReader(in, maxSave+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil || n > maxSave {
		os.Remove(dst)
		return "", errors.New("couldn't save the file")
	}
	if err := markDownloaded(dst); err != nil {
		os.Remove(dst)
		return "", errors.New("couldn't mark the file as downloaded, so it wasn't kept")
	}
	_ = os.Remove(src)
	return dst, nil
}

// cleanName: a plain file name (the sender chose it).
func cleanName(name string) string {
	name = filepath.Base(strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return '_'
		}
		return r
	}, name))
	name = strings.TrimLeft(strings.TrimSpace(name), ".-")
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	if name == "" || name == "_" {
		name = "file"
	}
	return name
}

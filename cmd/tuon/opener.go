package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ralph/tuon/internal/opener"
	"github.com/ralph/tuon/internal/sandbox"
)

// handleOpen does what the sandboxed app asks through opener, after
// checking it: the app may be running a malicious library, so nothing it
// sends is trusted. Links open in the browser, photos only in Preview,
// saved files are only shown in Finder.
func handleOpen(req opener.Request) opener.Reply {
	fail := func(err error) opener.Reply { return opener.Reply{Err: err.Error()} }
	wait := 8 * time.Second
	if req.Op == opener.OpPickFile {
		wait = 10 * time.Minute // you're choosing in the dialog
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	switch req.Op {
	case opener.OpURL:
		if len(req.Args) != 1 {
			return fail(errors.New("one link"))
		}
		u, err := webLink(req.Args[0])
		if err != nil {
			return fail(err)
		}
		return fail0(sysOpenURL(ctx, u))
	case opener.OpPhotos:
		if len(req.Args) == 0 || len(req.Args) > 100 {
			return fail(errors.New("1 to 100 photos"))
		}
		dir, err := photoDir()
		if err != nil {
			return fail(err)
		}
		var paths []string
		for _, p := range req.Args {
			q, err := photoFile(dir, p)
			if err != nil {
				return fail(err)
			}
			paths = append(paths, q)
		}
		return fail0(sysOpenPhotos(ctx, paths))
	case opener.OpReveal:
		if len(req.Args) != 1 {
			return fail(errors.New("one file"))
		}
		q, err := downloadsFile(req.Args[0])
		if err != nil {
			return fail(err)
		}
		return fail0(sysReveal(ctx, q)) // shown, not opened
	case opener.OpClipImage:
		b := sysClipImage(ctx)
		if len(b) == 0 {
			return fail(errors.New("no image"))
		}
		return opener.Reply{Data: b}
	case opener.OpClipFile:
		// the file you copied in your file manager: the app can't read most
		// of your home folder, so the launcher hands over its contents
		p := sysClipFile(ctx)
		if _, err := os.Stat(p); p == "" || errors.Is(err, os.ErrNotExist) {
			return opener.Reply{} // no file copied (text on the clipboard reads as a made-up path)
		}
		return handFile(p)
	case opener.OpCopy:
		if len(req.Args) != 1 || len(req.Args[0]) > opener.MaxText {
			return fail(errors.New("one text, at most 1 MB"))
		}
		return fail0(sysCopy(ctx, req.Args[0]))
	case opener.OpPaste:
		t := sysPaste(ctx)
		if len(t) > opener.MaxText {
			t = t[:opener.MaxText]
		}
		return opener.Reply{Text: t}
	case opener.OpSound:
		if len(req.Args) != 2 || !soundName(req.Args[0]) {
			return fail(errors.New("a sound's name and a volume"))
		}
		vol, err := strconv.Atoi(req.Args[1])
		if err != nil || vol < 1 || vol > 200 {
			return fail(errors.New("volume 1 to 200"))
		}
		return fail0(sysSound(req.Args[0], vol))
	case opener.OpSave:
		if len(req.Args) != 2 {
			return fail(errors.New("a file and its name"))
		}
		dst, err := saveToDownloads(req.Args[0], req.Args[1])
		if err != nil {
			return fail(err)
		}
		_ = sysReveal(ctx, dst) // shown, not opened
		return opener.Reply{Text: dst}
	case opener.OpPickFile:
		// only a file you pick in the launcher's own Open dialog: the app
		// (which may run a bad library) can't choose what gets read
		p := sysPickFile(ctx)
		if p == "" {
			return fail(errors.New("no file picked"))
		}
		return handFile(p)
	}
	return fail(errors.New("unknown request"))
}

// handFile reads a file you chose (copied or picked) for the app: a
// regular file, at most opener.MaxFile.
func handFile(p string) opener.Reply {
	f, err := os.Open(p)
	if err != nil {
		return opener.Reply{Err: "can't read " + filepath.Base(p)}
	}
	defer f.Close()
	st, err := f.Stat()
	switch {
	case err != nil || st.IsDir():
		return opener.Reply{Err: filepath.Base(p) + " is a folder"}
	case !st.Mode().IsRegular():
		return opener.Reply{Err: filepath.Base(p) + " isn't a file"}
	case st.Size() > opener.MaxFile:
		return opener.Reply{Err: filepath.Base(p) + " is over 25 MB"}
	}
	data, err := io.ReadAll(io.LimitReader(f, opener.MaxFile+1))
	if err != nil || len(data) > opener.MaxFile {
		return opener.Reply{Err: "can't read " + filepath.Base(p)}
	}
	return opener.Reply{Text: filepath.Base(p), Data: data}
}

func fail0(err error) opener.Reply {
	if err != nil {
		return opener.Reply{Err: err.Error()}
	}
	return opener.Reply{}
}

// webLink: only http(s) with a host, nothing that could read as a flag.
func webLink(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("not a web link")
	}
	return u.String(), nil
}

// photoDir is the app's photo folder for the viewer, $TMPDIR/tuon.
func photoDir() (string, error) {
	base, err := sandbox.TempBase() // the app's temp folder (its own on Linux)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(filepath.Join(base, "tuon"))
}

var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// photoFile: a real image file inside dir (links resolved first).
func photoFile(dir, p string) (string, error) {
	q, err := filepath.EvalSymlinks(p)
	if err != nil || !within(dir, q) {
		return "", errors.New("not a photo from tuon")
	}
	if !imageExt[strings.ToLower(filepath.Ext(q))] {
		return "", errors.New("not a photo")
	}
	st, err := os.Lstat(q)
	if err != nil || !st.Mode().IsRegular() {
		return "", errors.New("not a photo")
	}
	head := make([]byte, 12)
	f, err := os.Open(q)
	if err != nil {
		return "", err
	}
	n, _ := f.Read(head)
	f.Close()
	if !isImage(head[:n]) {
		return "", errors.New("not a photo")
	}
	return q, nil
}

func isImage(b []byte) bool {
	switch {
	case len(b) >= 8 && bytes.Equal(b[:8], []byte("\x89PNG\r\n\x1a\n")):
	case len(b) >= 3 && bytes.Equal(b[:3], []byte{0xff, 0xd8, 0xff}):
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
	default:
		return false
	}
	return true
}

// downloadsFile: a file inside ~/Downloads (links resolved first).
func downloadsFile(p string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(home, "Downloads"))
	if err != nil {
		return "", err
	}
	q, err := filepath.EvalSymlinks(p)
	if err != nil || !within(dir, q) {
		return "", errors.New("not a file in Downloads")
	}
	return q, nil
}

// within: p is inside dir (both already resolved).
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// soundName: a plain sound name (letters, digits, - and _), never a path.
func soundName(n string) bool {
	if n == "" || len(n) > 64 {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

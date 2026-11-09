package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ralph/tuon/internal/opener"
)

// handleOpen does what the sandboxed app asks through opener, after
// checking it: the app may be running a malicious library, so nothing it
// sends is trusted. Links open in the browser, photos only in Preview,
// saved files are only shown in Finder.
func handleOpen(req opener.Request) opener.Reply {
	fail := func(err error) opener.Reply { return opener.Reply{Err: err.Error()} }
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
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
		return fail0(exec.CommandContext(ctx, "/usr/bin/open", u).Run())
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
		// Preview, never the file's default app: a "photo" can't run anything
		return fail0(exec.CommandContext(ctx, "/usr/bin/open", append([]string{"-a", "Preview"}, paths...)...).Run())
	case opener.OpReveal:
		if len(req.Args) != 1 {
			return fail(errors.New("one file"))
		}
		q, err := downloadsFile(req.Args[0])
		if err != nil {
			return fail(err)
		}
		return fail0(exec.CommandContext(ctx, "/usr/bin/open", "-R", q).Run()) // shown, not opened
	case opener.OpClipImage:
		out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", "the clipboard as «class PNGf»").Output()
		if err != nil {
			return fail(errors.New("no image"))
		}
		i := bytes.Index(out, []byte("PNGf"))
		if i < 0 {
			return fail(errors.New("no image"))
		}
		b, err := hex.DecodeString(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(string(out[i+4:])), "»")))
		if err != nil {
			return fail(errors.New("no image"))
		}
		return opener.Reply{Data: b}
	case opener.OpClipFile:
		out, _ := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", "POSIX path of (the clipboard as «class furl»)").Output()
		return opener.Reply{Text: strings.TrimSpace(string(out))}
	}
	return fail(errors.New("unknown request"))
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
	return filepath.EvalSymlinks(filepath.Join(os.TempDir(), "tuon"))
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

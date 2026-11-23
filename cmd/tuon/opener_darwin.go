package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
)

// macOS: open, osascript, pbcopy, afplay.

func sysOpenURL(ctx context.Context, u string) error {
	return exec.CommandContext(ctx, "/usr/bin/open", u).Run()
}

// sysOpenPhotos: Preview, never the file's default app (a "photo" can't
// run anything); several open together as one gallery.
func sysOpenPhotos(ctx context.Context, paths []string) error {
	return exec.CommandContext(ctx, "/usr/bin/open", append([]string{"-a", "Preview"}, paths...)...).Run()
}

func sysReveal(ctx context.Context, path string) error {
	return exec.CommandContext(ctx, "/usr/bin/open", "-R", path).Run()
}

func sysClipImage(ctx context.Context) []byte {
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", "the clipboard as «class PNGf»").Output()
	if err != nil {
		return nil
	}
	i := bytes.Index(out, []byte("PNGf"))
	if i < 0 {
		return nil
	}
	b, err := hex.DecodeString(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(string(out[i+4:])), "»")))
	if err != nil {
		return nil
	}
	return b
}

// sysClipFile: the file copied in Finder ("" if none).
func sysClipFile(ctx context.Context) string {
	out, _ := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", "POSIX path of (the clipboard as «class furl»)").Output()
	return strings.TrimSpace(string(out))
}

func sysPickFile(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript",
		"-e", "tell me to activate",
		"-e", `POSIX path of (choose file with prompt "Attach a file to send in tuon")`).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func sysCopy(ctx context.Context, text string) error {
	c := exec.CommandContext(ctx, "/usr/bin/pbcopy")
	c.Stdin = strings.NewReader(text)
	return c.Run()
}

func sysPaste(ctx context.Context) string {
	out, _ := exec.CommandContext(ctx, "/usr/bin/pbpaste").Output()
	return string(out)
}

func sysSound(name string, vol int) error {
	return exec.Command("/usr/bin/afplay", "-v", fmt.Sprintf("%.2f", float64(vol)/100), "/System/Library/Sounds/"+name+".aiff").Start()
}

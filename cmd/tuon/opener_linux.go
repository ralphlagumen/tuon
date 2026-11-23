package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Linux: xdg-open, wl-clipboard or xclip/xsel, zenity or kdialog, paplay.
// Each is looked up on PATH, as your desktop would.

// firstTool runs the first of the commands whose program exists.
func firstTool(ctx context.Context, stdin string, cmds ...[]string) ([]byte, error) {
	for _, c := range cmds {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, c[0], c[1:]...)
		cmd.WaitDelay = 2 * time.Second // a tool that leaves a helper running can't hold us
		if stdin != "" {
			// copying: xclip and wl-copy stay in the background to serve the
			// clipboard; their output isn't read, so nothing waits for them
			cmd.Stdin = strings.NewReader(stdin)
			return nil, cmd.Run()
		}
		return cmd.Output()
	}
	return nil, errors.New("no tool for this here (install xdg-utils, wl-clipboard or xclip, zenity)")
}

// wayland: wl-clipboard only works there; xclip/xsel need X11.
func wayland() bool { return os.Getenv("WAYLAND_DISPLAY") != "" }

func sysOpenURL(ctx context.Context, u string) error {
	_, err := firstTool(ctx, "", []string{"xdg-open", u})
	return err
}

// sysOpenPhotos: the desktop's image viewer for the first photo (the
// others are in the same folder for its next/previous). photoFile has
// checked each is an image file by its name.
func sysOpenPhotos(ctx context.Context, paths []string) error {
	_, err := firstTool(ctx, "", []string{"xdg-open", paths[0]})
	return err
}

// sysReveal opens the file's folder (shown, the file itself not opened).
func sysReveal(ctx context.Context, path string) error {
	_, err := firstTool(ctx, "", []string{"xdg-open", filepath.Dir(path)})
	return err
}

func sysClipImage(ctx context.Context) []byte {
	var out []byte
	if wayland() {
		out, _ = firstTool(ctx, "", []string{"wl-paste", "--no-newline", "--type", "image/png"})
	} else {
		out, _ = firstTool(ctx, "", []string{"xclip", "-selection", "clipboard", "-target", "image/png", "-out"})
	}
	if len(out) < 8 || string(out[1:4]) != "PNG" {
		return nil
	}
	return out
}

// sysClipFile: the first file copied in the file manager ("" if none).
func sysClipFile(ctx context.Context) string {
	var out []byte
	if wayland() {
		out, _ = firstTool(ctx, "", []string{"wl-paste", "--no-newline", "--type", "text/uri-list"})
	} else {
		out, _ = firstTool(ctx, "", []string{"xclip", "-selection", "clipboard", "-target", "text/uri-list", "-out"})
	}
	for _, line := range strings.Split(string(out), "\n") {
		u, err := url.Parse(strings.TrimSpace(line))
		if err == nil && u.Scheme == "file" && filepath.IsAbs(u.Path) {
			return u.Path
		}
	}
	return ""
}

func sysPickFile(ctx context.Context) string {
	out, err := firstTool(ctx, "",
		[]string{"zenity", "--file-selection", "--title=Attach a file to send in tuon"},
		[]string{"kdialog", "--getopenfilename", ".", "--title", "Attach a file to send in tuon"})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func sysCopy(ctx context.Context, text string) error {
	if wayland() {
		_, err := firstTool(ctx, text, []string{"wl-copy"})
		return err
	}
	_, err := firstTool(ctx, text, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	return err
}

func sysPaste(ctx context.Context) string {
	var out []byte
	if wayland() {
		out, _ = firstTool(ctx, "", []string{"wl-paste", "--no-newline"})
	} else {
		out, _ = firstTool(ctx, "", []string{"xclip", "-selection", "clipboard", "-out"}, []string{"xsel", "--clipboard", "--output"})
	}
	return string(out)
}

// sysSound plays a freedesktop sound by name (checked: no path).
func sysSound(name string, vol int) error {
	file := "/usr/share/sounds/freedesktop/stereo/" + name + ".oga"
	if _, err := os.Stat(file); err == nil {
		if p, err := exec.LookPath("paplay"); err == nil {
			return exec.Command(p, fmt.Sprintf("--volume=%d", vol*65536/100), file).Start()
		}
	}
	if p, err := exec.LookPath("canberra-gtk-play"); err == nil {
		return exec.Command(p, "-i", name).Start()
	}
	return errors.New("no sound player")
}

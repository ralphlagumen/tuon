package ui

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ralph/tuon/internal/connector"
)

// Everything that talks to the operating system (clipboard, sound, opening
// files) lives here, with a version for macOS, Linux and Windows.

// run runs a command and returns its output, or an error if it's missing.
func runTool(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd.Output()
}

// firstOK tries each command in order until one works.
func firstOK(ctx context.Context, stdin string, cmds ...[]string) ([]byte, error) {
	err := errors.New("no tool available")
	for _, c := range cmds {
		out, e := runTool(ctx, stdin, c[0], c[1:]...)
		if e == nil {
			return out, nil
		}
		err = e
	}
	return nil, err
}

func timeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 8*time.Second)
}

// ---- clipboard ----

// clipboardImage returns a PNG on the clipboard, if there is one.
func clipboardImage() ([]byte, error) {
	ctx, cancel := timeout()
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		out, err := runTool(ctx, "", "osascript", "-e", "the clipboard as «class PNGf»")
		if err != nil {
			return nil, err
		}
		s := string(out)
		i := strings.Index(s, "PNGf")
		if i < 0 {
			return nil, errors.New("no image")
		}
		return hex.DecodeString(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s[i+4:]), "»")))
	case "windows":
		out, err := runTool(ctx, "", "powershell", "-NoProfile", "-Command",
			`Add-Type -AssemblyName System.Windows.Forms; $i=[Windows.Forms.Clipboard]::GetImage(); if($i){$m=New-Object IO.MemoryStream; $i.Save($m,[Drawing.Imaging.ImageFormat]::Png); [Convert]::ToBase64String($m.ToArray())}`)
		if err != nil || len(strings.TrimSpace(string(out))) == 0 {
			return nil, errors.New("no image")
		}
		return base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	default:
		out, err := firstOK(ctx, "",
			[]string{"wl-paste", "--no-newline", "--type", "image/png"},
			[]string{"xclip", "-selection", "clipboard", "-target", "image/png", "-out"})
		if err != nil || len(out) == 0 {
			return nil, errors.New("no image")
		}
		return out, nil
	}
}

// clipboardFile returns the path of a file copied in the file manager.
func clipboardFile() string {
	ctx, cancel := timeout()
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		out, _ := runTool(ctx, "", "osascript", "-e", "POSIX path of (the clipboard as «class furl»)")
		return strings.TrimSpace(string(out))
	case "windows":
		out, _ := runTool(ctx, "", "powershell", "-NoProfile", "-Command", "(Get-Clipboard -Format FileDropList | Select-Object -First 1).FullName")
		return strings.TrimSpace(string(out))
	default:
		out, _ := firstOK(ctx, "",
			[]string{"wl-paste", "--no-newline", "--type", "text/uri-list"},
			[]string{"xclip", "-selection", "clipboard", "-target", "text/uri-list", "-out"})
		line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		return strings.TrimPrefix(line, "file://")
	}
}

func clipboardText() string {
	ctx, cancel := timeout()
	defer cancel()
	var out []byte
	switch runtime.GOOS {
	case "darwin":
		out, _ = runTool(ctx, "", "pbpaste")
	case "windows":
		out, _ = runTool(ctx, "", "powershell", "-NoProfile", "-Command", "Get-Clipboard -Raw")
	default:
		out, _ = firstOK(ctx, "", []string{"wl-paste", "--no-newline"}, []string{"xclip", "-selection", "clipboard", "-out"}, []string{"xsel", "--clipboard", "--output"})
	}
	return strings.TrimRight(string(out), "\r\n")
}

// copyToClipboard uses the system tool, or the terminal's own clipboard
// (OSC 52, which Ghostty, kitty, WezTerm and Windows Terminal understand).
func copyToClipboard(text string) error {
	ctx, cancel := timeout()
	defer cancel()
	var err error
	switch runtime.GOOS {
	case "darwin":
		_, err = runTool(ctx, text, "pbcopy")
	case "windows":
		_, err = runTool(ctx, text, "powershell", "-NoProfile", "-Command", "$input | Set-Clipboard")
	default:
		_, err = firstOK(ctx, text, []string{"wl-copy"}, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	if err != nil {
		fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	}
	return nil
}

// ---- opening files ----

// previewFiles shows photos in the system's photo viewer. On macOS they open
// together in one Preview window (a gallery with a sidebar). Elsewhere the
// first one opens; the photos sit in their own folder, so the viewer's
// arrow keys move through the rest.
var previewFiles = openFilesOS // tests swap this out

func openFilesOS(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("open", paths...).Start()
	}
	return openFile(paths[0])
}

var openURL = openWebLink // tests swap this

// openWebLink opens a link from a message in the browser. Only http and
// https: a link must never launch an app or open a local file.
func openWebLink(raw string) error {
	if _, err := safeWebURL(raw); err != nil {
		return err
	}
	return openFile(raw)
}

func safeWebURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("not a web link, so it wasn't opened")
	}
	return u, nil
}

// openFile opens a file or web link in its default app. No shell is
// involved, so characters in the name can't run commands.
func openFile(target string) error {
	if strings.HasPrefix(target, "-") {
		return fmt.Errorf("refusing to open %q", target)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// tempAlbum writes photos into a fresh folder, numbered in order.
func tempAlbum(photos [][]byte) ([]string, error) {
	dir := filepath.Join(os.TempDir(), "tuon", "Photos "+time.Now().Format("2006-01-02 15.04.05.000"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var paths []string
	for i, b := range photos {
		p := filepath.Join(dir, fmt.Sprintf("Photo %02d%s", i+1, extFor(b)))
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// tempPhoto writes photo bytes to a temp file for viewing.
func tempPhoto(b []byte) (string, error) {
	dir := filepath.Join(os.TempDir(), "tuon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "Photo "+time.Now().Format("2006-01-02 15.04.05.000")+extFor(b))
	return path, os.WriteFile(path, b, 0o600)
}

// safeFileName makes a sender's file name safe to save: no folders, no
// hidden or dash-leading names, no control characters, a sane length.
func safeFileName(name string) string {
	name = connector.SafeText(name, false)
	name = strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".-")
	if r := []rune(name); len(r) > 120 {
		ext := filepath.Ext(name)
		if len([]rune(ext)) > 10 {
			ext = ""
		}
		name = string(r[:120-len([]rune(ext))]) + ext
	}
	if name == "" {
		name = "file"
	}
	return name
}

// uniquePath adds " (2)", " (3)"… so a save never overwrites a file.
func uniquePath(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		q := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(q); os.IsNotExist(err) {
			return q
		}
	}
}

// revealFile shows a saved file in the file manager without opening it.
var revealFile = func(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	case "windows":
		return exec.Command("explorer", "/select,"+path).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}

func downloadsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// ---- sound ----

// soundNames lists the notification sounds this system has.
func soundNames() []string {
	switch runtime.GOOS {
	case "darwin":
		entries, err := os.ReadDir("/System/Library/Sounds")
		if err != nil {
			return []string{"Tink"}
		}
		var out []string
		for _, e := range entries {
			if n, ok := strings.CutSuffix(e.Name(), ".aiff"); ok {
				out = append(out, n)
			}
		}
		return out
	case "windows":
		return []string{"Asterisk", "Beep", "Exclamation", "Question"}
	default:
		return []string{"message", "message-new-instant", "bell", "complete", "dialog-information"}
	}
}

const defaultSound = "Tink"

var playSound = playSoundOS // tests swap this out

// playSoundOS plays a named sound at a volume (100 = normal, up to 200).
func playSoundOS(name string, volume int) {
	vol := float64(max(1, min(volume, 200))) / 100
	switch runtime.GOOS {
	case "darwin":
		path := "/System/Library/Sounds/" + name + ".aiff"
		if _, err := os.Stat(path); err != nil {
			path = "/System/Library/Sounds/" + defaultSound + ".aiff"
		}
		if exec.Command("afplay", "-v", fmt.Sprintf("%.2f", vol), path).Start() == nil {
			return
		}
	case "windows": // system sounds play at the system volume
		if exec.Command("powershell", "-NoProfile", "-Command", "[System.Media.SystemSounds]::"+name+".Play()").Start() == nil {
			return
		}
	default:
		file := "/usr/share/sounds/freedesktop/stereo/" + name + ".oga"
		if _, err := os.Stat(file); err != nil {
			file = "" // no such sound here: let canberra or the bell try
		}
		if _, err := exec.LookPath("paplay"); err == nil && file != "" {
			if exec.Command("paplay", fmt.Sprintf("--volume=%d", int(vol*65536)), file).Start() == nil {
				return
			}
		}
		if _, err := exec.LookPath("canberra-gtk-play"); err == nil && exec.Command("canberra-gtk-play", "-i", name).Start() == nil {
			return
		}
	}
	fmt.Print("\a") // the terminal's own bell
}

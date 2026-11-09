package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/ralph/tuon/internal/opener"
	"github.com/ralph/tuon/internal/paths"
	"github.com/ralph/tuon/internal/sandbox"
	"github.com/ralph/tuon/internal/store"
)

// sandboxNote: why the sandbox isn't on, if it isn't (shown in the header).
var sandboxNote string

// sandboxTest checks, from inside tuon's sandbox, that secrets can't be
// read and files can't be planted, while tuon's own folders still work.
func sandboxTest() int {
	if runtime.GOOS != "darwin" {
		fmt.Println("  No sandbox on this system yet.")
		return 0
	}
	if !sandbox.Active() {
		fmt.Println("  Not sandboxed:", sandboxNote)
		return 1
	}
	home, _ := os.UserHomeDir()
	fails := 0
	check := func(what string, wantOK bool, err error) {
		ok := err == nil
		mark := "ok  "
		if ok != wantOK {
			mark, fails = "FAIL", fails+1
		}
		res := "allowed"
		if !ok {
			res = "refused"
		}
		fmt.Printf("  %s %-46s %s\n", mark, what, res)
	}
	read := func(p string) error {
		_, err := os.ReadDir(p)
		if err != nil {
			_, err = os.ReadFile(p)
		}
		return err
	}
	write := func(p string) error {
		err := os.WriteFile(p, []byte("x"), 0o600)
		if err == nil {
			os.Remove(p)
		}
		return err
	}
	fmt.Println("  inside tuon's sandbox:")
	check("read ~/.ssh", false, read(filepath.Join(home, ".ssh")))
	check("read ~/Library/Keychains", false, read(filepath.Join(home, "Library", "Keychains")))
	check("read Chrome profiles", false, read(filepath.Join(home, "Library", "Application Support", "Google", "Chrome")))
	check("write ~/.zshrc-tuon-probe (shell startup)", false, write(filepath.Join(home, ".zshrc-tuon-probe")))
	check("write ~/.local/bin/tuon-probe (tuon's binary)", false, write(filepath.Join(home, ".local", "bin", "tuon-probe")))
	check("write ~/Desktop/tuon-probe", false, write(filepath.Join(home, "Desktop", "tuon-probe")))
	check("write ~/.config/tuon (its own data)", true, write(filepath.Join(store.Dir(), "sandbox-probe")))
	check("write ~/Downloads (saved files)", true, write(filepath.Join(home, "Downloads", ".tuon-probe")))
	check("write temp folder (photos for the viewer)", true, write(filepath.Join(os.TempDir(), "tuon-probe")))
	check("read ~/Desktop (attachments you pick)", true, read(filepath.Join(home, "Desktop")))
	check("read ~/.claude", false, read(filepath.Join(home, ".claude")))
	check("read other apps' containers", false, read(filepath.Join(home, "Library", "Containers")))
	check("write /private/tmp (shared temp)", false, write("/private/tmp/tuon-probe"))
	if base := paths.WorkBase(); base != "" { // the launcher's update and login folders
		_ = os.MkdirAll(base, 0o700) // refused in here; the launcher makes it
		check("write ~/Library/Caches/tuon (update, login)", false, write(filepath.Join(base, "sandbox-probe")))
		check("read ~/Library/Caches/tuon (login browser)", false, read(base))
	}
	run := func(name string, args ...string) error { return exec.Command(name, args...).Run() }
	check("start /bin/sh", false, run("/bin/sh", "-c", "true"))
	if exe, err := os.Executable(); err == nil { // tuon runs unsandboxed: never from in here
		exe, _ = filepath.EvalSymlinks(exe)
		check("start the launcher (tuon)", false, run(filepath.Join(filepath.Dir(exe), "tuon"), "--help"))
	}
	check("start /usr/bin/curl", false, run("/usr/bin/curl", "--version"))
	check("start a copied program from temp", false, func() error {
		p := filepath.Join(os.TempDir(), "tuon-probe-true")
		b, err := os.ReadFile("/usr/bin/true")
		if err == nil {
			err = os.WriteFile(p, b, 0o700)
		}
		if err != nil {
			return nil // couldn't set up: count as allowed, so the check fails loudly
		}
		defer os.Remove(p)
		return run(p)
	}())
	check("script Finder (Apple Events)", false, run("/usr/bin/osascript", "-e", `tell application "Finder" to count windows`))
	check("start fdesetup (FileVault check)", true, run("/usr/bin/fdesetup", "isactive"))
	check("start pbpaste (paste)", true, run("/usr/bin/pbpaste"))
	check("start afplay (new-message sound, muted here)", true, run("/usr/bin/afplay", "-v", "0", "/System/Library/Sounds/Pop.aiff"))
	check("start osascript", false, run("/usr/bin/osascript", "-e", "1"))
	check("start open", false, run("/usr/bin/open", "-g", "https://example.com"))
	if c := opener.Connect(); c != nil {
		_, err := c.Do(opener.OpClipFile)
		check("paste files through the launcher", true, err)
		_, err = c.Do(opener.OpURL, "file:///etc/hosts")
		check("launcher opens a file:// link", false, err)
		_, err = c.Do(opener.OpPhotos, "/etc/hosts")
		check("launcher opens a non-photo", false, err)
		_, err = c.Do(opener.OpReveal, filepath.Join(home, ".zshrc"))
		check("launcher shows a file outside Downloads", false, err)
	} else {
		check("the launcher's opener is connected", true, errors.New("not connected"))
	}
	// escapes through macOS itself: a program the app asks LaunchServices
	// to start runs outside the sandbox. The probe is a tiny hidden app
	// that marks your home folder (which the app can't write) for a few
	// seconds, then removes the mark.
	check("start an app through `open`", false, escaped(home, "open", func(app string) error {
		return run("/usr/bin/open", "-g", "-j", app)
	}))
	check("start an app through osascript (JXA, NSWorkspace)", false, escaped(home, "jxa", func(app string) error {
		return run("/usr/bin/osascript", "-l", "JavaScript", "-e",
			`ObjC.import("AppKit"); $.NSWorkspace.sharedWorkspace.openURL($.NSURL.fileURLWithPath(`+strconv.Quote(app)+`))`)
	}))
	check("start an app through osascript (JXA, LaunchServices)", false, escaped(home, "jxa-ls", func(app string) error {
		return run("/usr/bin/osascript", "-l", "JavaScript", "-e",
			`ObjC.import("CoreServices"); $.LSOpenCFURLRef($.NSURL.fileURLWithPath(`+strconv.Quote(app)+`), null)`)
	}))
	if fails > 0 {
		fmt.Printf("  %d check(s) failed.\n", fails)
		return 1
	}
	fmt.Println("  All checks passed.")
	return 0
}

// escaped builds the probe app, asks start to launch it, and reports nil
// (allowed) if it ran outside the sandbox.
func escaped(home, name string, start func(app string) error) error {
	dir := filepath.Join(os.TempDir(), "tuon", "escape-probe-"+name)
	defer os.RemoveAll(dir)
	app := filepath.Join(dir, "TuonProbe.app")
	mark := filepath.Join(home, ".tuon-escape-probe-"+name)
	bin := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return nil // couldn't set up: count as allowed, so the check fails loudly
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>probe</string>
<key>CFBundleIdentifier</key><string>dev.tuon.escape-probe-` + name + `</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSUIElement</key><true/>
</dict></plist>`
	script := "#!/bin/sh\ntouch " + strconv.Quote(mark) + "\nsleep 4\nrm -f " + strconv.Quote(mark) + "\n"
	_ = os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o600)
	if os.WriteFile(filepath.Join(bin, "probe"), []byte(script), 0o700) != nil {
		return nil
	}
	_ = start(app)
	for i := 0; i < 30; i++ {
		if _, err := os.Stat(mark); err == nil {
			return nil // it ran out there
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("refused")
}

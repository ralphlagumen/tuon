package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ralph/tuon/internal/netgate"
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
	check("write ~/Downloads (saved files go via the launcher)", false, write(filepath.Join(home, "Downloads", ".tuon-probe")))
	check("write temp folder (photos for the viewer)", true, write(filepath.Join(os.TempDir(), "tuon-probe")))
	check("read ~/Desktop (attachments you pick)", true, read(filepath.Join(home, "Desktop")))
	check("read ~/Downloads", true, read(filepath.Join(home, "Downloads")))
	check("list your home folder", false, read(home))
	check("read ~/Projects (folders not listed)", false, read(filepath.Join(home, "Projects")))
	check("read ~/.zshrc (dotfiles)", false, read(filepath.Join(home, ".zshrc")))
	check("read ~/.config (other tools' settings)", false, read(filepath.Join(home, ".config")))
	check("read ~/.claude", false, read(filepath.Join(home, ".claude")))
	check("read other apps' containers", false, read(filepath.Join(home, "Library", "Containers")))
	check("write /private/tmp (shared temp)", false, write("/private/tmp/tuon-probe"))
	pin := filepath.Join(home, "Library", "Application Support", "tuon", "vault-pin")
	check("read the launcher's vault pin", false, read(pin))
	check("write the launcher's vault pin", false, write(pin+"-probe"))
	check("write other apps' caches (temp C/)", false, write(filepath.Join(filepath.Dir(filepath.Clean(os.TempDir())), "C", "tuon-probe")))
	if base := paths.WorkBase(); base != "" { // the launcher's update and login folders
		_ = os.MkdirAll(base, 0o700) // refused in here; the launcher makes it
		check("write ~/Library/Caches/tuon (update, login)", false, write(filepath.Join(base, "sandbox-probe")))
		check("read ~/Library/Caches/tuon (login browser)", false, read(base))
	}
	// what a bad library could use beyond tuon (audit 14): the SSH agent,
	// tokens in the environment, local sockets (Docker, other tools)
	check("SSH agent passed in (SSH_AUTH_SOCK)", false, envSet("SSH_AUTH_SOCK"))
	check("other secrets in the environment (tokens)", false, secretEnv())
	check("connect to a Unix socket (SSH agent, Docker)", false, unixDial())
	// the network: only Meta, only through the launcher's gate
	if gw := os.Getenv(netgate.Env); gw != "" {
		_, err := net.LookupHost("example.com")
		check("look up a name directly (could carry data out)", false, err)
		c, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second)
		if err == nil {
			c.Close()
		}
		check("connect to the internet directly (1.1.1.1)", false, err)
		check("through the gate: example.com", false, viaGate(gw, "example.com:443"))
		check("through the gate: www.facebook.com", true, viaGate(gw, "www.facebook.com:443"))
	} else {
		check("network gate running (TUON_PROXY)", true, errors.New("not set"))
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
	check("share your terminal (could type into your shell)", false, sharesTerminal())
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
		_, err = c.Do(opener.OpSave, filepath.Join(home, ".config", "tuon", "tuon.db"), "x")
		check("launcher saves a file from outside tuon's temp", false, err)
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

// viaGate asks the gate to CONNECT to target: nil when it agrees.
func viaGate(gw, target string) error {
	c, err := net.DialTimeout("tcp", gw, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return errors.New(resp.Status)
	}
	return nil
}

// envSet: nil (allowed) when the variable reached the app.
func envSet(k string) error {
	if os.Getenv(k) != "" {
		return nil
	}
	return errors.New("absent")
}

// secretEnv: nil when a variable that looks like a token reached the app.
func secretEnv() error {
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		u := strings.ToUpper(k)
		if strings.HasPrefix(u, "TUON_") {
			continue
		}
		for _, w := range []string{"TOKEN", "SECRET", "PASSWORD", "API_KEY", "_SOCK", "CREDENTIAL"} {
			if strings.Contains(u, w) {
				return nil
			}
		}
	}
	return errors.New("none")
}

// unixDial listens on a socket of its own in temp and tries to connect:
// nil when connecting worked.
func unixDial() error {
	sock := filepath.Join(os.TempDir(), "tuon-probe.sock")
	os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return nil // couldn't set up: count as allowed, so the check fails loudly
	}
	defer os.Remove(sock)
	defer l.Close()
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return err
	}
	c.Close()
	return nil
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

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

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
	run := func(name string, args ...string) error { return exec.Command(name, args...).Run() }
	check("start /bin/sh", false, run("/bin/sh", "-c", "true"))
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
	_, err := exec.Command("/usr/bin/osascript", "-e", "the clipboard as text").Output()
	check("osascript clipboard (paste images, files)", true, err)
	if fails > 0 {
		fmt.Printf("  %d check(s) failed.\n", fails)
		return 1
	}
	fmt.Println("  All checks passed.")
	return 0
}

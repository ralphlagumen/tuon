package main

import (
	"fmt"
	"os"
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
	if fails > 0 {
		fmt.Printf("  %d check(s) failed.\n", fails)
		return 1
	}
	fmt.Println("  All checks passed.")
	return 0
}

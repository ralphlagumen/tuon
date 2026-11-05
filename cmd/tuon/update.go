package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ralph/tuon/internal/store"
)

// sourceDir is the git clone tuon was built from. install.sh sets it with
// -ldflags "-X main.sourceDir=…", so `tuon update` knows where to pull.
var sourceDir string

// update pulls the latest tuon into its clone and rebuilds this binary.
func update() error {
	src := sourceDir
	if src == "" {
		if wd, err := os.Getwd(); err == nil && isClone(wd) {
			src = wd // a plain `go build` run from inside the clone
		}
	}
	if src == "" || !isClone(src) {
		return errors.New("this tuon wasn't built by install.sh, so it doesn't know where its source is.\n" +
			"  Run ./install.sh once from your tuon folder; after that `tuon update` works")
	}
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("Go is needed to build tuon: brew install go")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}

	if dirty, err := git(src, "status", "--porcelain", "--untracked-files=no"); err != nil {
		return err
	} else if dirty != "" {
		return fmt.Errorf("%s has uncommitted changes. Commit or stash them, then run `tuon update` again", src)
	}
	before, err := git(src, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	fmt.Printf("\n  Fetching the latest tuon into %s…\n", src)
	if out, err := git(src, "pull", "--ff-only"); err != nil {
		return fmt.Errorf("git pull failed:\n%s", indent(out))
	}
	after, err := git(src, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if before == after {
		fmt.Println("  Already up to date.")
		return nil
	}
	if log, err := git(src, "log", "--oneline", "--no-decorate", before+".."+after); err == nil && log != "" {
		fmt.Println("  New:")
		fmt.Println(indent(log))
	}

	fmt.Println("  Building…")
	tmp := exe + ".new"
	build := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-s -w -X main.sourceDir="+src, "-o", tmp, "./cmd/tuon")
	build.Dir = src
	if out, err := build.CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("build failed:\n%s", indent(strings.TrimSpace(string(out))))
	}
	if runtime.GOOS == "windows" { // a running .exe can be renamed, not replaced
		_ = os.Remove(exe + ".old")
		if err := os.Rename(exe, exe+".old"); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	fmt.Printf("  Updated %s.\n", exe)

	// a tuon that's open keeps running the old version until it restarts
	if release, err := store.Lock(); err == nil {
		release()
	} else if errors.Is(err, store.ErrRunning) {
		fmt.Println("  tuon is open in another window: quit it and start it again to use the update.")
	}
	return nil
}

func isClone(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "cmd", "tuon", "main.go"))
	return err == nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

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
	if out, err := git(src, "fetch", "--quiet"); err != nil {
		return fmt.Errorf("git fetch failed:\n%s", indent(out))
	}
	after, err := git(src, "rev-parse", "@{u}")
	if err != nil {
		return fmt.Errorf("this clone's branch doesn't track a remote: %w", err)
	}
	if before == after {
		fmt.Println("  Already up to date.")
		return nil
	}
	if log, err := git(src, "log", "--oneline", "--no-decorate", before+".."+after); err == nil && log != "" {
		fmt.Println("  New:")
		fmt.Println(indent(log))
	}
	// new or changed dependencies are where a supply-chain attack would
	// come in: show them and ask before building anything
	if deps, err := git(src, "diff", before+".."+after, "--", "go.mod"); err == nil && deps != "" {
		fmt.Println("\n  This update changes tuon's dependencies (go.mod):")
		fmt.Println(indent(changedRequires(deps)))
		fmt.Println("\n  Only continue if you trust these changes (see the release notes or the diff).")
		fmt.Print("  Type yes to continue: ")
		if strings.TrimSpace(readLine()) != "yes" {
			return errors.New("update cancelled: dependency changes not accepted")
		}
	} else if sums, err := git(src, "diff", "--stat", before+".."+after, "--", "go.sum"); err == nil && sums != "" {
		return errors.New("go.sum changed without go.mod: refusing to build; check the repo")
	}
	if out, err := git(src, "merge", "--ff-only", "--quiet", after); err != nil {
		return fmt.Errorf("couldn't fast-forward:\n%s", indent(out))
	}
	// every module must match the checksums Go recorded for it
	verify := exec.Command("go", "mod", "verify")
	verify.Dir = src
	if out, err := verify.CombinedOutput(); err != nil {
		return fmt.Errorf("dependency check failed, not building:\n%s", indent(strings.TrimSpace(string(out))))
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
	if runtime.GOOS == "darwin" { // the Touch ID helper lives next to tuon
		helper := filepath.Join(filepath.Dir(exe), "tuon-keyhelper")
		sw := exec.Command("swiftc", "-O", "cmd/tuon-keyhelper/main.swift", "-o", helper+".new")
		sw.Dir = src
		if out, err := sw.CombinedOutput(); err != nil {
			fmt.Printf("  Couldn't rebuild the Touch ID helper (kept the old one):\n%s\n", indent(strings.TrimSpace(string(out))))
		} else if err := os.Rename(helper+".new", helper); err != nil {
			return err
		}
	}

	// a tuon that's open keeps running the old version until it restarts
	if release, err := store.Lock(); err == nil {
		release()
	} else if errors.Is(err, store.ErrRunning) {
		fmt.Println("  tuon is open in another window: quit it and start it again to use the update.")
	}
	return nil
}

// changedRequires keeps the added/removed module lines of a go.mod diff.
func changedRequires(diff string) string {
	var out []string
	for _, l := range strings.Split(diff, "\n") {
		if (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) && !strings.HasPrefix(l, "+++") && !strings.HasPrefix(l, "---") {
			if t := strings.TrimSpace(l[1:]); t != "" {
				out = append(out, l[:1]+" "+t)
			}
		}
	}
	return strings.Join(out, "\n")
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

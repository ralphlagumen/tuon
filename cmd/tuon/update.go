package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ralph/tuon/internal/store"
)

// sourceDir is the git clone tuon was built from. Older builds had it set
// with -ldflags; now install.sh writes it to ~/.config/tuon/source, so the
// binary itself is the same for everyone (reproducible builds).
var sourceDir string

func sourcePath() string {
	if sourceDir != "" {
		return sourceDir
	}
	b, err := os.ReadFile(filepath.Join(store.Dir(), "source"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// update pulls the latest tuon into its clone and rebuilds this binary.
func update() error {
	src := sourcePath()
	if src == "" {
		if wd, err := os.Getwd(); err == nil && isClone(wd) {
			src = wd // a plain `go build` run from inside the clone
		}
	}
	if src == "" || !isClone(src) {
		return errors.New("tuon doesn't know where its source folder is.\n" +
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
	if out, err := git(src, "fetch", "--quiet", "--tags"); err != nil {
		return fmt.Errorf("git fetch failed:\n%s", indent(out))
	}
	// only a release tag signed by tuon's maintainer is installed: a hacked
	// repo or account can push code, but can't sign it with that key
	tag, after, err := latestSignedRelease(src)
	if err != nil {
		return err
	}
	if before == after {
		fmt.Printf("  Already up to date (%s).\n", tag)
		return nil
	}
	if _, err := git(src, "merge-base", "--is-ancestor", before, after); err != nil {
		if _, ahead := git(src, "merge-base", "--is-ancestor", after, before); ahead == nil {
			fmt.Printf("  This copy is newer than the latest release (%s); nothing to do.\n", tag)
			return nil
		}
		return fmt.Errorf("release %s doesn't follow from this copy's history; refusing to switch", tag)
	}
	fmt.Printf("  Release %s, signed by the maintainer.\n", tag)
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
	build := exec.Command("go", "build", "-trimpath", "-ldflags", releaseLDFlags, "-o", tmp, "./cmd/tuon")
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

// releaseLDFlags: the same flags as install.sh and scripts/release.sh, so
// every build of a release has the same checksum.
const releaseLDFlags = "-s -w -buildid="

//go:embed release_signers
var releaseSigners string

// trustedSigners: the public keys allowed to sign releases (tests swap it).
var trustedSigners = releaseSigners

// latestSignedRelease finds the newest v* tag with a valid signature from a
// trusted key. Newer tags that aren't signed that way are skipped, loudly.
func latestSignedRelease(src string) (tag, commit string, err error) {
	list, err := git(src, "tag", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return "", "", err
	}
	f, err := os.CreateTemp("", "tuon-signers-")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(trustedSigners); err != nil {
		return "", "", err
	}
	f.Close()
	for _, t := range strings.Fields(list) {
		if _, err := git(src, "-c", "gpg.ssh.allowedSignersFile="+f.Name(), "verify-tag", t); err != nil {
			fmt.Printf("  Skipping %s: not signed by tuon's maintainer.\n", t)
			continue
		}
		c, err := git(src, "rev-parse", t+"^{commit}")
		return t, c, err
	}
	return "", "", errors.New("no signed release found yet, so nothing was installed")
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

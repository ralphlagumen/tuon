package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ralph/tuon/internal/paths"
	"github.com/ralph/tuon/internal/safefile"
)

// Update notices. Once a day the launcher looks at the release tags on
// releaseRepo (git ls-remote: names only, nothing downloaded). When a
// newer one is there, tuon asks at the next start whether to update, and
// the app's header says so (`:update` updates from inside tuon). The tag
// names aren't trusted: installing still goes through update(), which
// takes only releases signed by tuon's maintainer. TUON_NO_UPDATE_CHECK=1
// turns the check off. Builds from source ("dev") never check.

const checkEvery = 24 * time.Hour

var releaseTag = regexp.MustCompile(`^v[0-9]+(\.[0-9]+){1,3}$`)

// updateState: when the launcher last looked, what it found, and a
// release you said to skip. Kept in the launcher's own folder, out of the
// sandboxed app's reach.
type updateState struct {
	checked      time.Time
	latest, skip string
}

func statePath() string {
	if base := paths.WorkBase(); base != "" {
		return filepath.Join(base, "update-check")
	}
	return ""
}

func loadState() updateState {
	var st updateState
	b, err := os.ReadFile(statePath())
	if err != nil {
		return st
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "checked":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				st.checked = time.Unix(n, 0)
			}
		case "latest":
			if releaseTag.MatchString(v) {
				st.latest = v
			}
		case "skip":
			if releaseTag.MatchString(v) {
				st.skip = v
			}
		}
	}
	return st
}

func saveState(st updateState) {
	p := statePath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = safefile.WriteFile(p, []byte(fmt.Sprintf("checked=%d\nlatest=%s\nskip=%s\n", st.checked.Unix(), st.latest, st.skip)), 0o600)
}

func checksUpdates() bool {
	return version != "dev" && os.Getenv("TUON_NO_UPDATE_CHECK") == ""
}

// pendingUpdate: a release newer than this one, that you didn't skip.
func pendingUpdate(st updateState) string {
	if !checksUpdates() || st.latest == "" || st.latest == st.skip || !newerTag(st.latest, version) {
		return ""
	}
	return st.latest
}

// latestTag reads the newest v* tag name on releaseRepo.
func latestTag(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always",
		"ls-remote", "--tags", "--refs", releaseRepo, "v*")
	cmd.Env = safeGitEnv()
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return newestTag(string(out)), nil
}

// newestTag picks the highest release tag from ls-remote's output.
func newestTag(lsRemote string) string {
	best := ""
	for _, line := range strings.Split(lsRemote, "\n") {
		_, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		tag := strings.TrimPrefix(ref, "refs/tags/")
		if ok && releaseTag.MatchString(tag) && (best == "" || newerTag(tag, best)) {
			best = tag
		}
	}
	return best
}

// refreshUpdates looks again if a day has passed, in the background, and
// calls found with a newer release (not one you skipped).
func refreshUpdates(found func(tag string)) {
	if !checksUpdates() {
		return
	}
	st := loadState()
	if time.Since(st.checked) < checkEvery {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		tag, err := latestTag(ctx)
		if err != nil {
			return // offline, or git missing: try again next start
		}
		st := loadState()
		was := pendingUpdate(st)
		st.checked, st.latest = time.Now(), tag
		saveState(st)
		if now := pendingUpdate(st); now != "" && now != was {
			found(now)
		}
	}()
}

// offerUpdate asks at start whether to install a newer release. true: it
// was installed and tuon should restart.
func offerUpdate(tag string) bool {
	fmt.Printf("\n  tuon %s is out (you have %s).\n", tag, version)
	fmt.Print("  Update now? [y]es, [n]ot now, [s]kip this version: ")
	switch strings.ToLower(strings.TrimSpace(readLine())) {
	case "y", "yes":
	case "s", "skip":
		st := loadState()
		st.skip = tag
		saveState(st)
		fmt.Printf("  Skipping %s. `tuon update` or :update installs it any time.\n", tag)
		return false
	default:
		return false
	}
	if err := update(); err != nil {
		fmt.Fprintln(os.Stderr, "  tuon: update failed:", err)
		fmt.Println("  Starting the version you have.")
		return false
	}
	return true
}

// restart runs the freshly installed tuon in place of this one.
func restart(args []string) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fail(err)
	}
	fmt.Println("  Starting the new tuon…")
	if runtime.GOOS != "windows" {
		fail(syscall.Exec(exe, append([]string{exe}, args...), os.Environ()))
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if x, ok := err.(*exec.ExitError); ok {
			os.Exit(x.ExitCode())
		}
		fail(err)
	}
	os.Exit(0)
}
